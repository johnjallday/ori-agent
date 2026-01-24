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
	Extra        map[string]interface{} // Additional custom data

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
		"templates/components/sidebar.tmpl",
		"templates/components/session-sidebar.tmpl",
		"templates/components/workspace-hub.tmpl",
		"templates/components/chat-area.tmpl",
		"templates/components/task-modal.tmpl",
		"templates/components/modals.tmpl",
		"templates/components/navbar.tmpl",
		"templates/components/file_dialog.tmpl",
		"templates/components/studios/manage-agents-modal.tmpl",
		"templates/components/workspaces/create-workspace-modal.tmpl",
		"templates/components/studios/workspace-details-modal.tmpl",
		"templates/pages/index.tmpl",
		"templates/pages/agents.tmpl",
		"templates/pages/agents-detail.tmpl",
		"templates/pages/agents-edit.tmpl",
		"templates/pages/settings.tmpl",
		"templates/pages/marketplace.tmpl",
		"templates/pages/plugins.tmpl",
		"templates/pages/workflows.tmpl",
		"templates/pages/studios.tmpl",
		"templates/pages/plugin-page.tmpl",
		"templates/pages/workspace-canvas.tmpl",
		"templates/pages/usage.tmpl",
		"templates/pages/mcp.tmpl",
		"templates/pages/models.tmpl",
		"templates/pages/review.tmpl",
		"templates/pages/skills.tmpl",
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
			logger.Error("Error parsing template", logger.Fields{"error": name, "err": err})
			return err
		}
		logger.Debug("Loaded template", logger.Fields{"name": name})
	}

	tr.templates["index"] = tmpl
	tr.templates["agents"] = tmpl
	tr.templates["agents-detail"] = tmpl
	tr.templates["agents-edit"] = tmpl
	tr.templates["settings"] = tmpl
	tr.templates["marketplace"] = tmpl
	tr.templates["plugins"] = tmpl
	tr.templates["workflows"] = tmpl
	tr.templates["studios"] = tmpl
	tr.templates["plugin-page"] = tmpl
	tr.templates["workspace-canvas"] = tmpl
	tr.templates["usage"] = tmpl
	tr.templates["mcp"] = tmpl
	tr.templates["models"] = tmpl
	tr.templates["review"] = tmpl
	tr.templates["skills"] = tmpl
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
	case "marketplace", "settings", "plugins", "workflows", "studios", "plugin-page", "workspace-canvas", "usage", "mcp", "models", "review", "agents-detail", "agents-edit", "skills":
		// These templates use {{define "name"}}, so execute by defined name
		templateName = name
	case "agents":
		// agents.tmpl doesn't use {{define}}, so execute by file name
		templateName = name + ".tmpl"
	}

	err := tmpl.ExecuteTemplate(&buf, templateName, data)
	if err != nil {
		logger.Error("Error executing template", logger.Fields{"error": name, "err": err})
		return "", err
	}

	return buf.String(), nil
}

// GetDefaultData returns default template data
func GetDefaultData() TemplateData {
	return TemplateData{
		Title:        "Ori Agent",
		Theme:        "light",
		CurrentAgent: "Default Agent",
		Model:        "gpt-5-nano",
		Version:      version.GetVersion(),
		Extra:        make(map[string]interface{}), // Initialize Extra map

		// Navbar defaults - enable common features
		ShowSidebarToggle:      true,
		SidebarToggleTarget:    "#sidebar",
		ShowNavLinks:           true,
		ShowCurrentAgent:       true,
		ShowUpdateNotification: true,
	}
}
