package sessionhttp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// resolveProjectTemplate picks the requested template: a library ID when
// templateID is set, otherwise an arbitrary folder path (the "Choose folder…"
// escape hatch).
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

	relPath, err := projecttemplates.Instantiate(tpl.Path, folderPath, projectName)
	if err != nil {
		return err
	}

	now := time.Now()
	ws.ProjectPath = relPath
	ws.UpdatedAt = now
	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		_ = os.RemoveAll(filepath.Join(folderPath, relPath))
		ws.ProjectPath = ""
		return fmt.Errorf("failed to persist project path: %w", err)
	}

	folderWS.ProjectPath = relPath
	folderWS.UpdatedAt = now
	if err := h.workspaceStore.Save(folderWS); err != nil {
		// workspace.json is rebuilt from the session store on later syncs, so
		// this is a warning rather than a rollback.
		logger.Warn("Failed to sync project path to workspace.json", logger.Fields{"id": ws.ID, "error": err})
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
