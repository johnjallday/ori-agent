package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// createWorkspaceProjectConnection is the optional project_connection object
// of POST /api/workspaces. It asks Ori to reference a project folder the user
// already has instead of scaffolding a new one. The folder is named only by
// the opaque token Ori's native folder picker issued; a path is never taken
// from the request.
type createWorkspaceProjectConnection struct {
	ModeID         string `json:"mode_id"`
	SelectionToken string `json:"selection_token"`
	EntryName      string `json:"entry_name,omitempty"`
}

// createWorkspaceAttachPlan is a validated attach, computed before anything is
// created. Recording re-scans the folder and refuses if any fact changed.
type createWorkspaceAttachPlan struct {
	declaration *projecttemplates.AttachExistingDeclaration
	root        string
	entry       string
	digest      string
}

// createWorkspaceAttachError carries the HTTP status and the user-facing
// message for a refused attach.
type createWorkspaceAttachError struct {
	status  int
	message string
}

func (e *createWorkspaceAttachError) Error() string { return e.message }

func attachBadRequest(message string) *createWorkspaceAttachError {
	return &createWorkspaceAttachError{status: http.StatusBadRequest, message: message}
}

var (
	errAttachFolderChanged = &createWorkspaceAttachError{
		status:  http.StatusConflict,
		message: "The project folder changed while the workspace was being created. Choose the project folder again.",
	}
	errAttachWorkspaceFolderUnavailable = &createWorkspaceAttachError{
		status:  http.StatusInternalServerError,
		message: "The workspace folder could not be created, so the existing project was not attached.",
	}
)

// planCreateWorkspaceAttach validates project_connection before any agent,
// Home, workspace, or folder exists. It returns nil, nil when the request does
// not attach an existing project.
func (h *Handler) planCreateWorkspaceAttach(req createWorkspaceRequest, kind session.WorkspaceKind, template projecttemplates.Template, templateResolved bool) (*createWorkspaceAttachPlan, *createWorkspaceAttachError) {
	connection := req.ProjectConnection
	if connection == nil {
		return nil, nil
	}
	switch {
	case kind == session.WorkspaceKindGroup:
		return nil, attachBadRequest("An existing project can be attached to a workspace, not a group.")
	case req.Blank || strings.TrimSpace(req.TemplateID) == "" && strings.TrimSpace(req.TemplatePath) == "":
		return nil, attachBadRequest("Choose a blueprint that supports existing projects before attaching one.")
	case strings.TrimSpace(req.TemplatePath) != "":
		return nil, attachBadRequest("An existing project can be attached only with a blueprint from the library.")
	case !templateResolved:
		return nil, attachBadRequest("The selected blueprint is unavailable, so an existing project cannot be attached.")
	case template.ProjectConnection == nil || !template.ProjectConnection.Supports(projecttemplates.ProjectConnectionExistingProject) ||
		template.ProjectConnection.AttachExisting == nil:
		return nil, attachBadRequest("This blueprint does not support using an existing project.")
	case strings.TrimSpace(connection.ModeID) != string(projecttemplates.ProjectConnectionExistingProject):
		return nil, attachBadRequest("project_connection.mode_id must be existing_project.")
	case strings.TrimSpace(req.ProjectName) != "":
		return nil, attachBadRequest("project_name cannot be combined with an existing project; the project file comes from the chosen folder.")
	}
	if h.pathSelections == nil {
		return nil, &createWorkspaceAttachError{status: http.StatusServiceUnavailable, message: "Choosing an existing project folder is unavailable right now."}
	}
	token := strings.TrimSpace(connection.SelectionToken)
	if token == "" {
		return nil, attachBadRequest("Choose the project folder before creating the workspace.")
	}
	selected, err := h.pathSelections.Resolve(token)
	if err != nil {
		return nil, attachBadRequest("The chosen project folder is no longer available to Ori. Choose the project folder again.")
	}
	declaration := template.ProjectConnection.AttachExisting
	scan, err := projectconnection.ScanExistingProject(selected, declaration)
	switch {
	case errors.Is(err, projectconnection.ErrNoProjectEntry):
		return nil, attachBadRequest("No project file ending in " + joinEntryExtensions(declaration.EntryExtensions) + " was found in the chosen folder.")
	case err != nil:
		return nil, attachBadRequest("The chosen project folder cannot be read. Choose another project folder.")
	}
	entry, err := projectconnection.SelectProjectEntry(strings.TrimSpace(connection.EntryName), scan.Candidates)
	switch {
	case err != nil:
		return nil, attachBadRequest("The chosen project file is not in the chosen folder. Choose the project file again.")
	case entry == "":
		return nil, attachBadRequest("The chosen folder has several project files. Choose which one to use.")
	}
	return &createWorkspaceAttachPlan{declaration: declaration, root: scan.Root, entry: entry, digest: scan.Digest}, nil
}

// joinEntryExtensions renders declared extensions for a message: ".a",
// ".a or .b", ".a, .b, or .c".
func joinEntryExtensions(extensions []string) string {
	switch len(extensions) {
	case 0:
		return "a supported extension"
	case 1:
		return extensions[0]
	case 2:
		return extensions[0] + " or " + extensions[1]
	default:
		return strings.Join(extensions[:len(extensions)-1], ", ") + ", or " + extensions[len(extensions)-1]
	}
}

// recordCreateWorkspaceAttachedProject references the planned folder on the
// new workspace in place of scaffolding. It re-scans first and refuses if the
// folder or its project files changed since validation. Both the canonical
// workspace.json and the session row must record it; a failure is fatal to
// the create, unlike best-effort template scaffolding.
func (h *Handler) recordCreateWorkspaceAttachedProject(ctx context.Context, ws *session.Workspace, folderWS *agentworkspace.Workspace, plan *createWorkspaceAttachPlan) error {
	if h.workspaceStore == nil || ws == nil || folderWS == nil {
		return errAttachWorkspaceFolderUnavailable
	}
	scan, err := projectconnection.ScanExistingProject(plan.root, plan.declaration)
	if err != nil || scan.Root != plan.root || scan.Digest != plan.digest || !slices.Contains(scan.Candidates, plan.entry) {
		return errAttachFolderChanged
	}
	if err := projectconnection.RecordAttachedProject(folderWS, ws.Name, scan.Root, plan.entry, uuid.NewString()); err != nil {
		logger.Warn("Existing project could not be recorded", logger.Fields{"id": ws.ID, "error": err})
		return errAttachFolderChanged
	}
	now := time.Now()
	folderWS.UpdatedAt = now
	if err := h.workspaceStore.Save(folderWS); err != nil {
		logger.Error("Failed to persist attached project", logger.Fields{"id": ws.ID, "error": err})
		return errAttachWorkspaceFolderUnavailable
	}
	refsJSON, err := json.Marshal(folderWS.DirectoryReferences)
	if err != nil {
		return errAttachWorkspaceFolderUnavailable
	}
	ws.DirectoryReferencesJSON = refsJSON
	ws.SharedData = folderWS.SharedData
	ws.UpdatedAt = now
	// Unlike a scaffolded project's best-effort mirror, this write is required:
	// later create steps read-modify-write through the session row, so a row
	// without the reference would erase it from workspace.json again.
	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		logger.Error("Failed to sync attached project to the session store", logger.Fields{"id": ws.ID, "error": err})
		return errAttachWorkspaceFolderUnavailable
	}
	logger.Info("Existing project attached", logger.Fields{"workspace_id": ws.ID, "project_entry": plan.entry})
	return nil
}

// rollbackFailedAttachCreate removes a workspace whose existing project could
// not be attached, together with its folder under the workspace root and the
// agents this request seeded, the same way a folder-slug conflict does. The
// user's project folder is only ever referenced, so it is never touched.
func (h *Handler) rollbackFailedAttachCreate(ctx context.Context, workspaceID string, seed seedAgentsResult) {
	var err error
	switch {
	case h.workspaceTaskStore != nil:
		err = h.workspaceTaskStore.Delete(workspaceID)
	case h.store != nil:
		err = h.store.DeleteWorkspace(ctx, workspaceID)
		if h.workspaceStore != nil {
			_ = h.workspaceStore.Delete(workspaceID)
		}
	}
	if err != nil {
		logger.Error("Failed to roll back workspace after attach failure", logger.Fields{"id": workspaceID, "error": err})
		return
	}
	h.rollbackSeededAgents(seed)
}

func respondCreateWorkspaceAttachError(w http.ResponseWriter, err error) {
	var attachErr *createWorkspaceAttachError
	if !errors.As(err, &attachErr) {
		attachErr = errAttachWorkspaceFolderUnavailable
	}
	_ = orihttp.RespondJSON(w, attachErr.status, map[string]any{
		"error":    attachErr.message,
		"conflict": map[string]any{"type": "project_connection"},
	})
}
