package web

import (
	"embed"
	"html/template"
	"io/fs"
	"strings"
)

// Embed the entire static directory.
//
//go:embed static/*
var Static embed.FS

// ComponentRenderer handles template-based component rendering
type ComponentRenderer struct {
	templates map[string]*template.Template
}

// NewComponentRenderer creates a new component renderer
func NewComponentRenderer() *ComponentRenderer {
	return &ComponentRenderer{
		templates: make(map[string]*template.Template),
	}
}

// LoadComponent loads a component template from the embedded filesystem
func (cr *ComponentRenderer) LoadComponent(name string) error {
	path := "static/components/" + name + ".html"
	content, err := Static.ReadFile(path)
	if err != nil {
		return err
	}
	
	tmpl, err := template.New(name).Parse(string(content))
	if err != nil {
		return err
	}
	
	cr.templates[name] = tmpl
	return nil
}

// LoadAllComponents loads all component templates from the components directory
func (cr *ComponentRenderer) LoadAllComponents() error {
	componentsDir := "static/components"
	
	return fs.WalkDir(Static, componentsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		
		if !d.IsDir() && strings.HasSuffix(path, ".html") {
			// Extract component name from path
			name := strings.TrimSuffix(strings.TrimPrefix(path, componentsDir+"/"), ".html")
			return cr.LoadComponent(name)
		}
		
		return nil
	})
}

// RenderComponent renders a component with the given data
func (cr *ComponentRenderer) RenderComponent(name string, data interface{}) (string, error) {
	tmpl, exists := cr.templates[name]
	if !exists {
		// Try to load the component if it doesn't exist
		if err := cr.LoadComponent(name); err != nil {
			return "", err
		}
		tmpl = cr.templates[name]
	}
	
	var buf strings.Builder
	err := tmpl.Execute(&buf, data)
	if err != nil {
		return "", err
	}
	
	return buf.String(), nil
}

// GetComponentList returns a list of available components
func (cr *ComponentRenderer) GetComponentList() []string {
	var components []string
	for name := range cr.templates {
		components = append(components, name)
	}
	return components
}
