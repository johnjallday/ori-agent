package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// SetProjectTemplateDeps enables the project-template tools: a resolver for
// the templates library directory and the event bus for project.created.
func (p *WorkspaceToolProvider) SetProjectTemplateDeps(templatesRootResolver func() string, eventBus *workspace.EventBus) {
	p.templatesRootResolver = templatesRootResolver
	p.projectEventBus = eventBus
}

// --- workspace_project_templates (read) ---

func (p *WorkspaceToolProvider) projectTemplatesTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "workspace_project_templates",
			Description: "List the project templates available for scaffolding a project folder inside this workspace (e.g. a REAPER song or a writing project). Use workspace_create_project to instantiate one.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			templates, err := projecttemplates.ListLibrary(p.templatesRootResolver())
			if err != nil {
				return "", fmt.Errorf("failed to read templates library: %w", err)
			}
			items := make([]map[string]any, 0, len(templates))
			for _, tpl := range templates {
				items = append(items, map[string]any{
					"id":          tpl.ID,
					"name":        tpl.Name,
					"description": tpl.Description,
					"tags":        tpl.Tags,
				})
			}
			message := fmt.Sprintf("%d project template(s) available.", len(items))
			if len(items) == 0 {
				message = "No project templates are installed. The user can add one by dropping a template folder into the templates directory."
			}
			return marshalToolResponse(map[string]any{
				"templates": items,
				"message":   message,
			})
		},
	}
}

// --- workspace_create_project (write) ---

func (p *WorkspaceToolProvider) createProjectTool() toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name:        "workspace_create_project",
			Description: "Create this workspace's project folder from a template (see workspace_project_templates). The project is scaffolded inside the workspace folder and recorded as the workspace's project_path. Fails if the workspace already has a project.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"template_id": map[string]any{
						"type":        "string",
						"description": "ID of a template from workspace_project_templates.",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Optional project name; defaults to the workspace name. Used for the folder name and {{name}} substitution.",
					},
				},
				"required": []string{"template_id"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			var req struct {
				TemplateID string `json:"template_id"`
				Name       string `json:"name"`
			}
			if err := json.Unmarshal([]byte(args), &req); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}

			ws, err := p.sessionStore.GetWorkspace(ctx, p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("workspace not found: %w", err)
			}
			if p.fileStore == nil {
				return "", fmt.Errorf("workspace folder storage is unavailable, so a project cannot be created")
			}
			folderWS, err := p.fileStore.Get(p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("workspace folder is unavailable: %w", err)
			}
			// workspace.json is the canonical store for project_path; the
			// session row may lag behind it (no SQLite column).
			existingProject := ws.ProjectPath
			if strings.TrimSpace(existingProject) == "" {
				existingProject = folderWS.ProjectPath
			}
			if err := projecttemplates.ValidateTarget(ws.IsGroup(), existingProject); err != nil {
				return "", err
			}
			folderPath, err := p.fileStore.GetFolderPath(p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("workspace folder is unavailable: %w", err)
			}

			tpl, err := projecttemplates.FindLibraryTemplate(p.templatesRootResolver(), req.TemplateID)
			if err != nil {
				available, listErr := projecttemplates.ListLibrary(p.templatesRootResolver())
				if listErr == nil && len(available) > 0 {
					ids := make([]string, 0, len(available))
					for _, t := range available {
						ids = append(ids, t.ID)
					}
					return "", fmt.Errorf("unknown template %q; available templates: %s", req.TemplateID, strings.Join(ids, ", "))
				}
				return "", err
			}

			projectName := strings.TrimSpace(req.Name)
			if projectName == "" {
				projectName = ws.Name
			}

			relPath, err := projecttemplates.Instantiate(tpl.Path, folderPath, projectName)
			if err != nil {
				return "", err
			}

			projectDirID, err := projecttemplates.EnsureProjectDirectoryReference(folderWS, projectName, folderPath, relPath)
			if err != nil {
				_ = os.RemoveAll(filepath.Join(folderPath, relPath))
				return "", fmt.Errorf("failed to register project folder: %w", err)
			}

			// workspace.json is the canonical store for project_path, so its
			// write must succeed — otherwise roll the project folder back.
			now := time.Now()
			folderWS.ProjectPath = relPath
			folderWS.Tags = workspace.MergeWorkspaceTags(folderWS.Tags, tpl.Tags)
			if folderWS.SharedData == nil {
				folderWS.SharedData = make(map[string]any)
			}
			projecttemplates.SetPrimaryDirectoryID(folderWS.SharedData, projectDirID)
			folderWS.UpdatedAt = now
			if err := p.fileStore.Save(folderWS); err != nil {
				_ = os.RemoveAll(filepath.Join(folderPath, relPath))
				return "", fmt.Errorf("failed to persist project path: %w", err)
			}

			// Best-effort metadata sync; reads hydrate project_path from disk.
			ws.ProjectPath = relPath
			ws.Tags = workspace.MergeWorkspaceTags(ws.Tags, tpl.Tags)
			if ws.SharedData == nil {
				ws.SharedData = make(map[string]any)
			}
			projecttemplates.SetPrimaryDirectoryID(ws.SharedData, projectDirID)
			if refsJSON, err := json.Marshal(folderWS.DirectoryReferences); err == nil {
				ws.DirectoryReferencesJSON = refsJSON
			}
			ws.UpdatedAt = now
			if err := p.sessionStore.UpdateWorkspace(ctx, ws); err != nil {
				logger.Warn("Failed to sync workspace metadata after project creation", logger.Fields{"id": p.workspaceID, "error": err})
			}

			if p.projectEventBus != nil {
				p.projectEventBus.Publish(workspace.Event{
					Type:        workspace.EventProjectCreated,
					WorkspaceID: p.workspaceID,
					Source:      "workspace_create_project",
					Data: map[string]any{
						"project_path": relPath,
						"template_id":  tpl.ID,
					},
				})
			}

			logger.Info("Workspace tool created project from template", logger.Fields{
				"workspace_id": p.workspaceID,
				"template":     tpl.ID,
				"project_path": relPath,
			})
			return marshalToolResponse(map[string]any{
				"project_path": relPath,
				"template_id":  tpl.ID,
				"tags":         ws.Tags,
				"message":      fmt.Sprintf("Project created at %q inside the workspace folder (template %q). It is recorded as the workspace's project_path and is readable through the workspace filesystem tools.", relPath, tpl.ID),
			})
		},
	}
}
