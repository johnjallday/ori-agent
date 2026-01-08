package pluginapi

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"sync"
)

// TemplateRenderer provides template rendering capabilities for plugins.
// It handles template parsing, caching, and rendering with automatic XSS protection.
type TemplateRenderer struct {
	cache map[string]*template.Template
	mu    sync.RWMutex
}

// NewTemplateRenderer creates a new template renderer instance.
func NewTemplateRenderer() *TemplateRenderer {
	return &TemplateRenderer{
		cache: make(map[string]*template.Template),
	}
}

// RenderTemplate renders a template from an embedded filesystem with the given data.
// Templates are automatically cached for performance (parsed once, rendered many times).
// HTML escaping is automatic to prevent XSS attacks.
//
// Parameters:
//   - templateFS: The embedded filesystem containing templates (use go:embed)
//   - templateName: Name/path of the template file (e.g., "marketplace.html")
//   - data: Data to pass to the template
//
// Example usage in a plugin:
//
//	//go:embed templates/*.html
//	var assetsFS embed.FS
//
//	func (p *myPlugin) ServeWebPage(path string, query map[string]string) (string, string, error) {
//	    renderer := pluginapi.NewTemplateRenderer()
//	    html, err := renderer.RenderTemplate(assetsFS, "marketplace.html", map[string]interface{}{
//	        "Title": "Plugin Marketplace",
//	        "Items": items,
//	    })
//	    return html, "text/html", err
//	}
func (r *TemplateRenderer) RenderTemplate(templateFS fs.FS, templateName string, data interface{}) (string, error) {
	// Try to get from cache first
	tmpl, err := r.getOrParseTemplate(templateFS, templateName)
	if err != nil {
		return "", fmt.Errorf("failed to parse template %q: %w", templateName, err)
	}

	// Render template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template %q: %w", templateName, err)
	}

	return buf.String(), nil
}

// RenderTemplateWithLayout renders a template with a layout (base template).
// Useful for pages that share a common structure (header, footer, etc.).
//
// Example:
//
//	html, err := renderer.RenderTemplateWithLayout(
//	    templateFS,
//	    "layout.html",        // Base template with {{template "content" .}}
//	    "marketplace.html",   // Content template
//	    data,
//	)
func (r *TemplateRenderer) RenderTemplateWithLayout(templateFS fs.FS, layoutName, templateName string, data interface{}) (string, error) {
	cacheKey := layoutName + ":" + templateName

	// Try to get from cache
	tmpl, err := r.getOrParseTemplateWithLayout(templateFS, layoutName, templateName, cacheKey)
	if err != nil {
		return "", fmt.Errorf("failed to parse templates: %w", err)
	}

	// Render template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// ClearCache clears the template cache.
// Useful during development or when templates are updated.
func (r *TemplateRenderer) ClearCache() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache = make(map[string]*template.Template)
}

// getOrParseTemplate retrieves a template from cache or parses it if not cached.
func (r *TemplateRenderer) getOrParseTemplate(templateFS fs.FS, templateName string) (*template.Template, error) {
	// Check cache first (with read lock)
	r.mu.RLock()
	if tmpl, exists := r.cache[templateName]; exists {
		r.mu.RUnlock()
		return tmpl, nil
	}
	r.mu.RUnlock()

	// Parse template (with write lock)
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check in case another goroutine parsed it while we were waiting
	if tmpl, exists := r.cache[templateName]; exists {
		return tmpl, nil
	}

	// Parse template
	content, err := fs.ReadFile(templateFS, templateName)
	if err != nil {
		return nil, fmt.Errorf("failed to read template file: %w", err)
	}

	tmpl, err := template.New(templateName).Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	// Cache the parsed template
	r.cache[templateName] = tmpl
	return tmpl, nil
}

// getOrParseTemplateWithLayout retrieves or parses a template with layout.
func (r *TemplateRenderer) getOrParseTemplateWithLayout(templateFS fs.FS, layoutName, templateName, cacheKey string) (*template.Template, error) {
	// Check cache first (with read lock)
	r.mu.RLock()
	if tmpl, exists := r.cache[cacheKey]; exists {
		r.mu.RUnlock()
		return tmpl, nil
	}
	r.mu.RUnlock()

	// Parse templates (with write lock)
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check cache
	if tmpl, exists := r.cache[cacheKey]; exists {
		return tmpl, nil
	}

	// Read layout file
	layoutContent, err := fs.ReadFile(templateFS, layoutName)
	if err != nil {
		return nil, fmt.Errorf("failed to read layout file: %w", err)
	}

	// Read template file
	templateContent, err := fs.ReadFile(templateFS, templateName)
	if err != nil {
		return nil, fmt.Errorf("failed to read template file: %w", err)
	}

	// Parse both templates
	tmpl, err := template.New(layoutName).Parse(string(layoutContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse layout: %w", err)
	}

	tmpl, err = tmpl.New(templateName).Parse(string(templateContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	// Cache the parsed template
	r.cache[cacheKey] = tmpl
	return tmpl, nil
}

// DefaultRenderer is a global template renderer instance that can be used by plugins.
var DefaultRenderer = NewTemplateRenderer()

// RenderTemplate is a convenience function that uses the default global renderer.
func RenderTemplate(templateFS fs.FS, templateName string, data interface{}) (string, error) {
	return DefaultRenderer.RenderTemplate(templateFS, templateName, data)
}

// RenderTemplateWithLayout is a convenience function that uses the default global renderer.
func RenderTemplateWithLayout(templateFS fs.FS, layoutName, templateName string, data interface{}) (string, error) {
	return DefaultRenderer.RenderTemplateWithLayout(templateFS, layoutName, templateName, data)
}
