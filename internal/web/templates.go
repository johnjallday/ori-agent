package web

import (
	"errors"
	"github.com/johnjallday/ori-agent/internal/version"
	"html/template"
	"log"
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
	log.Printf("Loading templates from embedded filesystem")

	// Create a new template with custom functions if needed
	tmpl := template.New("base")

	// Load all template files from embedded filesystem
	templatePaths := []string{
		"templates/layout/base.tmpl",
		"templates/layout/head.tmpl",
		"templates/components/sidebar.tmpl",
		"templates/components/chat-area.tmpl",
		"templates/components/modals.tmpl",
		"templates/components/navbar.tmpl",
		"templates/pages/index.tmpl",
		"templates/pages/agents.tmpl",
		"templates/pages/settings.tmpl",
		"templates/pages/marketplace.tmpl",
		"templates/pages/workflows.tmpl",
		"templates/pages/studios.tmpl",
		"templates/pages/workspace-dashboard.tmpl",
		"templates/pages/usage.tmpl",
		"templates/pages/mcp.tmpl",
		"templates/pages/models.tmpl",
	}

	for _, path := range templatePaths {
		content, err := Templates.ReadFile(path)
		if err != nil {
			log.Printf("Warning: Could not read template %s: %v", path, err)
			continue
		}

		// Extract the template name from the path
		name := filepath.Base(path)
		_, err = tmpl.New(name).Parse(string(content))
		if err != nil {
			log.Printf("Error parsing template %s: %v", name, err)
			return err
		}
		log.Printf("Loaded template: %s", name)
	}

	tr.templates["index"] = tmpl
	tr.templates["agents"] = tmpl
	tr.templates["settings"] = tmpl
	tr.templates["marketplace"] = tmpl
	tr.templates["workflows"] = tmpl
	tr.templates["studios"] = tmpl
	tr.templates["workspace-dashboard"] = tmpl
	tr.templates["usage"] = tmpl
	tr.templates["mcp"] = tmpl
	tr.templates["models"] = tmpl
	log.Printf("Successfully loaded templates from embedded filesystem")

	return nil
}

// RenderTemplate renders a template with the given data
func (tr *TemplateRenderer) RenderTemplate(name string, data TemplateData) (string, error) {
	tmpl, exists := tr.templates[name]
	if !exists {
		log.Printf("Template not found: %s", name)
		return "", errors.New("template not found: " + name)
	}

	var buf strings.Builder
	// For index, we execute base.tmpl which includes all components
	// For standalone pages that use {{define}}, we execute them by their defined name (without .tmpl extension)
	// For agents (which doesn't use {{define}}), we execute the file name with .tmpl
	templateName := name + ".tmpl"
	if name == "index" {
		templateName = "base.tmpl"
	} else if name == "marketplace" || name == "settings" || name == "workflows" || name == "studios" || name == "workspace-dashboard" || name == "usage" || name == "mcp" || name == "models" {
		// These templates use {{define "name"}}, so execute by defined name
		templateName = name
	} else if name == "agents" {
		// agents.tmpl doesn't use {{define}}, so execute by file name
		templateName = name + ".tmpl"
	}

	err := tmpl.ExecuteTemplate(&buf, templateName, data)
	if err != nil {
		log.Printf("Error executing template %s: %v", name, err)
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
