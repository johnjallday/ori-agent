package web

import (
	"errors"
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

// LoadTemplates loads all templates from the templates directory
func (tr *TemplateRenderer) LoadTemplates() error {
	templateDir := "internal/web/templates"

	// Load base layout templates
	layoutPattern := filepath.Join(templateDir, "layout", "*.tmpl")
	componentPattern := filepath.Join(templateDir, "components", "*.tmpl")
	pagePattern := filepath.Join(templateDir, "pages", "*.tmpl")

	log.Printf("Loading templates from patterns: %s, %s, %s", layoutPattern, componentPattern, pagePattern)

	// Parse all templates together so they can reference each other
	tmpl, err := template.ParseGlob(layoutPattern)
	if err != nil {
		log.Printf("Error loading layout templates: %v", err)
		return err
	}

	tmpl, err = tmpl.ParseGlob(componentPattern)
	if err != nil {
		log.Printf("Error loading component templates: %v", err)
		return err
	}

	tmpl, err = tmpl.ParseGlob(pagePattern)
	if err != nil {
		log.Printf("Error loading page templates: %v", err)
		return err
	}

	tr.templates["index"] = tmpl
	log.Printf("Successfully loaded templates")

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
	// For index, we want to execute the base template
	templateName := "base.tmpl"
	if name != "index" {
		templateName = name + ".tmpl"
	}

	log.Printf("Executing template: %s with data: %+v", templateName, data)
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
		Title:        "Dolphin Agent Chatbot",
		Theme:        "light",
		CurrentAgent: "Default Agent",
		Model:        "gpt-4",
		Version:      "0.0.1",
	}
}

