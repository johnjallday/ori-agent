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
		"templates/pages/settings.tmpl",
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
	tr.templates["settings"] = tmpl
	log.Printf("Successfully loaded templates from embedded filesystem")

	return nil
}

// RenderTemplate renders a template with the given data
func (tr *TemplateRenderer) RenderTemplate(name string, data TemplateData) (string, error) {
	log.Printf("Rendering template: %s", name)
	tmpl, exists := tr.templates[name]
	if !exists {
		log.Printf("Template not found: %s", name)
		return "", errors.New("template not found: " + name)
	}

	var buf strings.Builder
	// For index and settings, we want to execute their specific templates
	templateName := name + ".tmpl"
	if name == "index" {
		templateName = "base.tmpl"
	}

	err := tmpl.ExecuteTemplate(&buf, templateName, data)
	if err != nil {
		log.Printf("Error executing template: %v", err)
		return "", err
	}

	result := buf.String()
	log.Printf("Template rendered successfully, length: %d", len(result))
	return result, nil
}

// GetDefaultData returns default template data
func GetDefaultData() TemplateData {
	return TemplateData{
		Title:        "Ori Agent Chatbot",
		Theme:        "light",
		CurrentAgent: "Default Agent",
		Model:        "gpt-5-nano",
		Version:      version.GetVersion(),
		//Version: "test",
	}
}
