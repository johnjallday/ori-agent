package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// workspaceSharedDataProjectDirectoryIDKey mirrors projecttemplates.ProjectDirectoryIDKey
// so this package and the workspace_create_project chat tool agree on the
// SharedData key used to record a workspace's project directory.
const workspaceSharedDataProjectDirectoryIDKey = projecttemplates.ProjectDirectoryIDKey

// resolveProjectTemplate picks the requested template: a library ID when
// templateID is set, otherwise an arbitrary folder path (the "Choose folder…"
// escape hatch).
//
// Note: templatePath is not restricted to the templates library — LoadFolder
// will os.Stat and later copy from any absolute path the caller supplies.
// That is acceptable for this admin-facing, local-first, single-user app
// (the caller already has filesystem access), but this endpoint and
// LoadFolder should not be exposed to untrusted callers without adding a
// path allowlist/containment check.
func (h *Handler) resolveProjectTemplate(templateID, templatePath string) (projecttemplates.Template, error) {
	switch {
	case strings.TrimSpace(templateID) != "":
		if h.templatesRootResolver == nil {
			return projecttemplates.Template{}, fmt.Errorf("templates library is not configured")
		}
		return projecttemplates.FindLibraryTemplate(h.templatesRootResolver(), templateID)
	case strings.TrimSpace(templatePath) != "":
		return projecttemplates.LoadFolder(templatePath)
	default:
		return projecttemplates.Template{}, fmt.Errorf("no template specified")
	}
}

// instantiateWorkspaceProject creates a project folder from a template inside
// the workspace's folder, persists ProjectPath in both the session store and
// the folder store, and publishes project.created. On any failure after the
// copy it removes the project folder again so the workspace never ends up
// with an orphaned project or a dangling ProjectPath.
func (h *Handler) instantiateWorkspaceProject(ctx context.Context, ws *session.Workspace, folderWS *agentworkspace.Workspace, templateID, templatePath, projectName string) error {
	if err := projecttemplates.ValidateTarget(ws.IsGroup(), ws.ProjectPath); err != nil {
		return err
	}
	if h.workspaceStore == nil {
		return fmt.Errorf("workspace folder storage is unavailable")
	}

	folderPath, err := h.workspaceStore.GetFolderPath(ws.ID)
	if err != nil {
		return fmt.Errorf("workspace folder is unavailable: %w", err)
	}

	tpl, err := h.resolveProjectTemplate(templateID, templatePath)
	if err != nil {
		return err
	}

	if strings.TrimSpace(projectName) == "" {
		projectName = ws.Name
	}
	displayProjectName := strings.TrimSpace(projectName)

	relPath, err := projecttemplates.Instantiate(tpl.Path, folderPath, projectName)
	if err != nil {
		return err
	}

	projectDirID, err := projecttemplates.EnsureProjectDirectoryReference(folderWS, displayProjectName, folderPath, relPath)
	if err != nil {
		_ = os.RemoveAll(filepath.Join(folderPath, relPath))
		return fmt.Errorf("failed to register project folder: %w", err)
	}

	// workspace.json is the canonical store for project_path (there is no
	// SQLite column; session reads hydrate it from disk), so its write is the
	// one that must succeed — otherwise roll the project folder back.
	now := time.Now()
	folderWS.ProjectPath = relPath
	folderWS.Tags = agentworkspace.MergeWorkspaceTags(folderWS.Tags, tpl.Tags)
	setFileStoreWorkspacePrimaryDirectoryID(folderWS, projectDirID)
	folderWS.UpdatedAt = now
	if err := h.workspaceStore.Save(folderWS); err != nil {
		_ = os.RemoveAll(filepath.Join(folderPath, relPath))
		folderWS.ProjectPath = ""
		return fmt.Errorf("failed to persist project path: %w", err)
	}

	ws.ProjectPath = relPath
	ws.Tags = agentworkspace.MergeWorkspaceTags(ws.Tags, tpl.Tags)
	if projectDirID != "" {
		setWorkspacePrimaryDirectoryID(ws, projectDirID)
		if ws.SharedData == nil {
			ws.SharedData = make(map[string]any)
		}
		ws.SharedData[workspaceSharedDataProjectDirectoryIDKey] = projectDirID
	}
	if refsJSON, err := json.Marshal(folderWS.DirectoryReferences); err == nil {
		ws.DirectoryReferencesJSON = refsJSON
	} else {
		logger.Warn("Failed to sync project directory reference to session workspace", logger.Fields{"id": ws.ID, "error": err})
	}
	ws.UpdatedAt = now
	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		// Best-effort: the session row only mirrors metadata; reads hydrate
		// project_path from workspace.json regardless.
		logger.Warn("Failed to sync workspace metadata after project creation", logger.Fields{"id": ws.ID, "error": err})
	}

	if h.eventBus != nil {
		h.eventBus.Publish(agentworkspace.Event{
			Type:        agentworkspace.EventProjectCreated,
			WorkspaceID: ws.ID,
			Source:      "sessionhttp",
			Data: map[string]any{
				"project_path": relPath,
				"template_id":  tpl.ID,
			},
		})
	}

	logger.Info("Project instantiated from template", logger.Fields{
		"workspace_id": ws.ID,
		"template":     tpl.ID,
		"project_path": relPath,
	})
	return nil
}

func setFileStoreWorkspacePrimaryDirectoryID(ws *agentworkspace.Workspace, directoryID string) {
	if ws == nil {
		return
	}
	if ws.SharedData == nil {
		ws.SharedData = make(map[string]any)
	}
	projecttemplates.SetPrimaryDirectoryID(ws.SharedData, directoryID)
}

func (h *Handler) handleWorkspaceProject(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req struct {
		TemplateID   string `json:"template_id,omitempty"`
		TemplatePath string `json:"template_path,omitempty"`
		ProjectName  string `json:"project_name,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.TemplateID) != "" && strings.TrimSpace(req.TemplatePath) != "" {
		_ = orihttp.RespondBadRequest(w, "specify either template_id or template_path, not both")
		return
	}
	if strings.TrimSpace(req.TemplateID) == "" && strings.TrimSpace(req.TemplatePath) == "" {
		_ = orihttp.RespondBadRequest(w, "template_id or template_path is required")
		return
	}

	workspace, err := h.store.GetWorkspace(r.Context(), id)
	if err == session.ErrWorkspaceNotFound {
		_ = orihttp.RespondNotFound(w, "Workspace not found")
		return
	}
	if err != nil {
		logger.Error("Failed to get workspace for project creation", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "Failed to get workspace")
		return
	}
	h.hydrateWorkspaceMetadataInto(workspace)

	if h.workspaceStore == nil {
		_ = orihttp.RespondInternalError(w, "workspace folder storage is unavailable")
		return
	}
	folderWS, err := h.workspaceStore.Get(id)
	if err != nil {
		logger.Error("Failed to get workspace folder metadata for project creation", logger.Fields{"id": id, "error": err})
		_ = orihttp.RespondInternalError(w, "workspace folder is unavailable")
		return
	}

	if err := h.instantiateWorkspaceProject(r.Context(), workspace, folderWS, req.TemplateID, req.TemplatePath, req.ProjectName); err != nil {
		h.respondWorkspaceProjectError(w, err)
		return
	}

	hydrated := h.hydrateWorkspaceMetadataFromFileStore(workspace)
	_ = orihttp.RespondCreated(w, map[string]any{
		"success":      true,
		"project_path": hydrated.ProjectPath,
		"workspace":    h.buildWorkspaceDetailResponse(hydrated),
	})
}

func (h *Handler) respondWorkspaceProjectError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, projecttemplates.ErrGroupWorkspace):
		_ = orihttp.RespondBadRequest(w, err.Error())
	case errors.Is(err, projecttemplates.ErrReservedName):
		_ = orihttp.RespondBadRequest(w, err.Error())
	case errors.Is(err, projecttemplates.ErrTemplateNotFound):
		_ = orihttp.RespondBadRequest(w, err.Error())
	case errors.Is(err, projecttemplates.ErrProjectExists):
		_ = orihttp.RespondConflict(w, err.Error())
	default:
		logger.Error("Failed to create workspace project", logger.Fields{"error": err})
		_ = orihttp.RespondInternalError(w, "Failed to create project")
	}
}
