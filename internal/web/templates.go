package web

import (
	"errors"
	"github.com/johnjallday/ori-agent/internal/version"
	"html/template"

	"github.com/johnjallday/ori-agent/internal/logger"
	"path/filepath"
	"strings"
)

// TemplateData represents data passed to templates
type TemplateData struct {
	Title        string
	Theme        string
	CurrentAgent string
	Model        string
	Version      string
	CurrentPage  string
	Extra        map[string]any // Additional custom data

	// Navbar configuration fields
	NavbarClass            string
	NavbarFixed            bool
	NavbarPadding          string
	ShowBackButton         bool
	BackButtonURL          string
	BackButtonTitle        string
	ShowSidebarToggle      bool
	SidebarToggleId        string
	SidebarToggleHideOnLg  bool
	SidebarToggleTarget    string
	BrandNoDecoration      bool
	BrandURL               string
	BrandColor             string
	BrandIcon              template.HTML
	UseLegacyIcon          bool
	BrandText              string
	ShowNavLinks           bool
	ShowCurrentAgent       bool
	CurrentAgentClickable  bool
	ShowLocationIndicator  bool
	ShowUpdateNotification bool
	UpdateButtonText       string
	ShowRefreshButton      bool
	DarkModeIconFill       string
}

// TemplateRenderer handles template rendering
type TemplateRenderer struct {
	templates map[string]*template.Template
}

// NewTemplateRenderer creates a new template renderer
func NewTemplateRenderer() *TemplateRenderer {
	return &TemplateRenderer{
		templates: make(map[string]*template.Template),
	}
}

// LoadTemplates loads all templates from the embedded filesystem
func (tr *TemplateRenderer) LoadTemplates() error {
	logger.Debug("Loading templates from embedded filesystem", logger.Fields{})

	// Create a new template with custom functions if needed
	tmpl := template.New("base")

	// Load all template files from embedded filesystem
	templatePaths := []string{
		"templates/layout/base.tmpl",
		"templates/layout/head.tmpl",
		"templates/layout/scripts-dx-utils.tmpl",
		"templates/layout/theme-init.tmpl",
		"templates/components/sidebar.tmpl",
		"templates/components/workspace-hub.tmpl",
		"templates/components/support-chat.tmpl",
		"templates/components/dashboard.tmpl",
		"templates/components/session-modals.tmpl",
		"templates/components/search-palette.tmpl",
		"templates/components/chat-area.tmpl",
		"templates/components/task-modal.tmpl",
		"templates/components/modals.tmpl",
		"templates/components/navbar.tmpl",
		"templates/components/vault-modal.tmpl",
		"templates/components/vault-settings-section.tmpl",
		"templates/components/user-profile-form.tmpl",
		"templates/components/file_dialog.tmpl",
		"templates/components/workspaces/manage-agents-modal.tmpl",
		"templates/components/workspaces/create-workspace-modal.tmpl",
		"templates/components/project-templates-manage.tmpl",
		"templates/components/workspaces/sync-modal.tmpl",
		"templates/components/workspaces/workspace-details-modal.tmpl",
		"templates/pages/index.tmpl",
		"templates/pages/agents.tmpl",
		"templates/pages/agents-roster.tmpl",
		"templates/pages/agents-detail.tmpl",
		"templates/pages/agents-claude-detail.tmpl",
		"templates/pages/agents-codex-detail.tmpl",
		"templates/pages/agents-create.tmpl",
		"templates/pages/settings.tmpl",
		"templates/pages/profile.tmpl",
		"templates/pages/vault.tmpl",
		"templates/pages/workflows.tmpl",
		"templates/pages/workspace-canvas.tmpl",
		"templates/pages/workspace-detail.tmpl",
		"templates/pages/workspace-agent-detail.tmpl",
		"templates/pages/workspace-diagnostics.tmpl",
		"templates/pages/workspace-task.tmpl",
		"templates/pages/workspace-run.tmpl",
		"templates/pages/usage.tmpl",
		"templates/pages/mcp.tmpl",
		"templates/pages/plugins.tmpl",
		"templates/pages/models.tmpl",
		"templates/pages/review.tmpl",
		"templates/pages/skills.tmpl",
		"templates/pages/templates.tmpl",
		"templates/pages/workspaces.tmpl",
		"templates/pages/personalize.tmpl",
		"templates/pages/note.tmpl",
		"templates/pages/action-center.tmpl",
	}

	for _, path := range templatePaths {
		content, err := Templates.ReadFile(path)
		if err != nil {
			logger.Warn("Could not read template", logger.Fields{"path": path, "err": err})
			continue
		}

		// Extract the template name from the path
		name := filepath.Base(path)
		_, err = tmpl.New(name).Parse(string(content))
		if err != nil {
			logger.Error("Error parsing template", logger.Fields{"template": name, "error": err})
			return err
		}
		logger.Debug("Loaded template", logger.Fields{"name": name})
	}

	tr.templates["index"] = tmpl
	tr.templates["agents"] = tmpl
	tr.templates["agents-roster"] = tmpl
	tr.templates["agents-detail"] = tmpl
	tr.templates["agents-claude-detail"] = tmpl
	tr.templates["agents-codex-detail"] = tmpl
	tr.templates["agents-create"] = tmpl
	tr.templates["settings"] = tmpl
	tr.templates["profile"] = tmpl
	tr.templates["vault"] = tmpl
	tr.templates["workflows"] = tmpl
	tr.templates["workspace-canvas"] = tmpl
	tr.templates["workspace-detail"] = tmpl
	tr.templates["workspace-agent-detail"] = tmpl
	tr.templates["workspace-diagnostics"] = tmpl
	tr.templates["workspace-task"] = tmpl
	tr.templates["workspace-run"] = tmpl
	tr.templates["usage"] = tmpl
	tr.templates["mcp"] = tmpl
	tr.templates["plugins"] = tmpl
	tr.templates["models"] = tmpl
	tr.templates["review"] = tmpl
	tr.templates["skills"] = tmpl
	tr.templates["templates"] = tmpl
	tr.templates["workspaces"] = tmpl
	tr.templates["personalize"] = tmpl
	tr.templates["note-page"] = tmpl
	tr.templates["action-center"] = tmpl
	logger.Info("Successfully loaded templates from embedded filesystem", logger.Fields{})

	return nil
}

// RenderTemplate renders a template with the given data
func (tr *TemplateRenderer) RenderTemplate(name string, data TemplateData) (string, error) {
	tmpl, exists := tr.templates[name]
	if !exists {
		logger.Debug("Template not found", logger.Fields{"name": name})
		return "", errors.New("template not found: " + name)
	}

	var buf strings.Builder
	// For index, we execute base.tmpl which includes all components
	// For standalone pages that use {{define}}, we execute them by their defined name (without .tmpl extension)
	// For agents (which doesn't use {{define}}), we execute the file name with .tmpl
	templateName := name + ".tmpl"
	switch name {
	case "index":
		templateName = "base.tmpl"
	case "settings", "profile", "vault", "workflows", "workspace-canvas", "workspace-detail", "workspace-agent-detail", "workspace-diagnostics", "workspace-task", "workspace-run", "usage", "mcp", "plugins", "models", "review", "agents-roster", "agents-detail", "agents-claude-detail", "agents-codex-detail", "agents-create", "skills", "templates", "workspaces", "personalize", "note-page", "action-center":
		// These templates use {{define "name"}}, so execute by defined name
		templateName = name
	case "agents":
		// agents.tmpl doesn't use {{define}}, so execute by file name
		templateName = name + ".tmpl"
	}

	err := tmpl.ExecuteTemplate(&buf, templateName, data)
	if err != nil {
		logger.Error("Error executing template", logger.Fields{"template": name, "error": err})
		return "", err
	}

	return buf.String(), nil
}

// GetDefaultData returns default template data
func GetDefaultData() TemplateData {
	return TemplateData{
		Title:        "Ori Agent",
		Theme:        "light",
		CurrentAgent: "Assistant",
		Model:        "gpt-5-nano",
		Version:      version.GetVersion(),
		Extra:        make(map[string]any),

		// Navbar defaults - enable common features
		ShowSidebarToggle:      true,
		SidebarToggleTarget:    "#sidebar",
		ShowNavLinks:           true,
		ShowCurrentAgent:       true,
		ShowUpdateNotification: true,
	}
}
