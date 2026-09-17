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
	// duplicate names the workspace that already owns the folder.
	duplicate *projectconnection.FolderOwner
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
	errAttachUnavailable = &createWorkspaceAttachError{
		status:  http.StatusServiceUnavailable,
		message: "Ori cannot check existing project folders right now. Try again.",
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
	review, reviewErr := h.reviewExistingProject(template, connection.SelectionToken, connection.EntryName)
	switch {
	case reviewErr != nil:
		return nil, reviewErr
	case review.owner != nil:
		return nil, attachDuplicate(review.owner)
	case review.entry == "":
		return nil, attachBadRequest("The chosen folder has several project files. Choose which one to use.")
	}
	return &createWorkspaceAttachPlan{
		declaration: template.ProjectConnection.AttachExisting, root: review.scan.Root, entry: review.entry, digest: review.scan.Digest,
	}, nil
}

// existingProjectReview is one inert read of a chosen folder for a blueprint.
type existingProjectReview struct {
	scan projectconnection.ExistingProjectScan
	// entry is the chosen project file, or "" while several candidates wait
	// for the user's choice.
	entry string
	// owner is the workspace the folder already belongs to, if any.
	owner *projectconnection.FolderOwner
}

// reviewExistingProject resolves a picker token and reads the folder behind it
// for template: its project files, the chosen one, and whether a workspace
// already owns the folder. It creates and changes nothing. The review endpoint
// and create validation both use it, so they can never disagree.
func (h *Handler) reviewExistingProject(template projecttemplates.Template, selectionToken, entryName string) (existingProjectReview, *createWorkspaceAttachError) {
	if template.ProjectConnection == nil || !template.ProjectConnection.Supports(projecttemplates.ProjectConnectionExistingProject) ||
		template.ProjectConnection.AttachExisting == nil {
		return existingProjectReview{}, attachBadRequest("This blueprint does not support using an existing project.")
	}
	owners := h.attachFolderOwnerStore()
	if h.pathSelections == nil || owners == nil {
		return existingProjectReview{}, errAttachUnavailable
	}
	token := strings.TrimSpace(selectionToken)
	if token == "" {
		return existingProjectReview{}, attachBadRequest("Choose the project folder before creating the workspace.")
	}
	selected, err := h.pathSelections.Resolve(token)
	if err != nil {
		return existingProjectReview{}, attachBadRequest("The chosen project folder is no longer available to Ori. Choose the project folder again.")
	}
	declaration := template.ProjectConnection.AttachExisting
	scan, err := projectconnection.ScanExistingProject(selected, declaration)
	switch {
	case errors.Is(err, projectconnection.ErrNoProjectEntry):
		return existingProjectReview{}, attachBadRequest("No project file ending in " + joinEntryExtensions(declaration.EntryExtensions) + " was found in the chosen folder.")
	case err != nil:
		return existingProjectReview{}, attachBadRequest("The chosen project folder cannot be read. Choose another project folder.")
	}
	entry, err := projectconnection.SelectProjectEntry(strings.TrimSpace(entryName), scan.Candidates)
	if err != nil {
		return existingProjectReview{}, attachBadRequest("The chosen project file is not in the chosen folder. Choose the project file again.")
	}
	owner, err := projectconnection.FindFolderOwner(owners, scan.Root, "")
	if err != nil {
		return existingProjectReview{}, errAttachUnavailable
	}
	return existingProjectReview{scan: scan, entry: entry, owner: owner}, nil
}

// attachFolderOwnerStore is every workspace (the session-backed store other
// create steps write through) plus where each one's folder is.
func (h *Handler) attachFolderOwnerStore() projectconnection.FolderOwnerStore {
	if h == nil || h.workspaceTaskStore == nil || h.workspaceStore == nil {
		return nil
	}
	return attachOwnerStore{Store: h.workspaceTaskStore, folders: h.workspaceStore}
}

type attachOwnerStore struct {
	agentworkspace.Store
	folders *agentworkspace.FileStore
}

func (s attachOwnerStore) GetFolderPath(id string) (string, error) {
	return s.folders.GetFolderPath(id)
}

// attachDuplicate refuses a folder that already belongs to a workspace. There
// is deliberately no way to attach it anyway.
func attachDuplicate(owner *projectconnection.FolderOwner) *createWorkspaceAttachError {
	name := strings.TrimSpace(owner.Name)
	if name == "" {
		name = "Another workspace"
	} else {
		name = "“" + name + "”"
	}
	return &createWorkspaceAttachError{
		status:    http.StatusConflict,
		message:   name + " already uses this folder. Open that workspace, or choose a different project folder.",
		duplicate: owner,
	}
}

// reviewProjectConnectionRequest is the body of
// POST /api/workspaces/project-connection/review.
type reviewProjectConnectionRequest struct {
	TemplateID     string `json:"template_id"`
	SelectionToken string `json:"selection_token"`
	EntryName      string `json:"entry_name,omitempty"`
}

// ReviewProjectConnection serves POST /api/workspaces/project-connection/review:
// an inert read of the folder behind a picker token for a blueprint. It lists
// the folder's project files, selects the only one (or the requested one), and
// reports a workspace that already owns the folder. It never creates anything.
func (h *Handler) ReviewProjectConnection(w http.ResponseWriter, r *http.Request) {
	var request reviewProjectConnectionRequest
	if !orihttp.ParseJSONBody(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.TemplateID) == "" {
		respondCreateWorkspaceAttachError(w, attachBadRequest("Choose a blueprint that supports existing projects before reviewing a folder."))
		return
	}
	template, err := h.resolveProjectTemplate(request.TemplateID, "")
	if err != nil {
		respondCreateWorkspaceAttachError(w, attachBadRequest("The selected blueprint is unavailable, so an existing project cannot be reviewed."))
		return
	}
	review, reviewErr := h.reviewExistingProject(template, request.SelectionToken, request.EntryName)
	if reviewErr != nil {
		respondCreateWorkspaceAttachError(w, reviewErr)
		return
	}
	response := map[string]any{
		"selected_folder":  review.scan.Root,
		"entry_name":       review.entry,
		"entry_candidates": review.scan.Candidates,
		"entry_extensions": template.ProjectConnection.AttachExisting.EntryExtensions,
		"duplicate":        nil,
	}
	if review.owner != nil {
		response["duplicate"] = review.owner
		response["duplicate_message"] = attachDuplicate(review.owner).message
	}
	_ = orihttp.RespondSuccess(w, response)
}

// starterTasksForAttachedProject is the blueprint's starter tasks allowed for
// an existing project, using the same connection_modes filter the guided
// journey applies. A declaration that cannot be read seeds nothing rather than
// tasks written for a new project.
func starterTasksForAttachedProject(template projecttemplates.Template) []projecttemplates.StarterTask {
	starters, err := projecttemplates.StarterTasksForConnection(template, projecttemplates.ProjectConnectionExistingProject)
	if err != nil {
		logger.Warn("Existing-project starter tasks are unavailable", logger.Fields{"template": template.ID, "error": err})
		return nil
	}
	return starters
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
	owners := h.attachFolderOwnerStore()
	if h.workspaceStore == nil || owners == nil || ws == nil || folderWS == nil {
		return errAttachWorkspaceFolderUnavailable
	}
	// From the final ownership check to the session write, no other attach (in
	// another tab or the guided journey) can claim the same folder.
	unlock := projectconnection.LockAttachCommit()
	defer unlock()
	scan, err := projectconnection.ScanExistingProject(plan.root, plan.declaration)
	if err != nil || scan.Root != plan.root || scan.Digest != plan.digest || !slices.Contains(scan.Candidates, plan.entry) {
		return errAttachFolderChanged
	}
	owner, err := projectconnection.FindFolderOwner(owners, scan.Root, ws.ID)
	switch {
	case err != nil:
		return errAttachUnavailable
	case owner != nil:
		return attachDuplicate(owner)
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
	body := map[string]any{
		"error":    attachErr.message,
		"conflict": map[string]any{"type": "project_connection"},
	}
	if attachErr.duplicate != nil {
		body["duplicate"] = attachErr.duplicate
	}
	_ = orihttp.RespondJSON(w, attachErr.status, body)
}
