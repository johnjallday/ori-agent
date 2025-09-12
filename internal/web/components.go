package web

import (
	"net/http"
)

// ComponentData represents data that can be passed to components
type ComponentData struct {
	Title         string
	Theme         string
	CurrentAgent  string
	Model         string
	Content       string
	Navbar        string
	Sidebar       string
	Modals        string
	CustomScripts string
}

// ComponentHandler handles component rendering requests
type ComponentHandler struct {
	renderer *ComponentRenderer
}

// NewComponentHandler creates a new component handler
func NewComponentHandler() *ComponentHandler {
	renderer := NewComponentRenderer()
	// Load all components at startup
	if err := renderer.LoadAllComponents(); err != nil {
		// Log error but don't fail - components will be loaded on-demand
	}

	return &ComponentHandler{
		renderer: renderer,
	}
}

// ServeComponent serves an individual component
func (ch *ComponentHandler) ServeComponent(w http.ResponseWriter, r *http.Request) {
	// Extract component name from URL path
	componentName := r.URL.Query().Get("name")
	if componentName == "" {
		http.Error(w, "Missing component name", http.StatusBadRequest)
		return
	}

	// Create default data - in a real application, this would come from the request context
	data := ComponentData{
		Title:        "Dolphin Agent",
		Theme:        "light",
		CurrentAgent: "default",
		Model:        "gpt-4",
	}

	// Render the component
	content, err := ch.renderer.RenderComponent(componentName, data)
	if err != nil {
		http.Error(w, "Failed to render component: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Set content type
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(content))
}

// RenderPage renders a complete page using components
func (ch *ComponentHandler) RenderPage(data ComponentData) (string, error) {
	// Render individual components
	navbar, err := ch.renderer.RenderComponent("navbar", data)
	if err != nil {
		return "", err
	}

	chatArea, err := ch.renderer.RenderComponent("chat-area", data)
	if err != nil {
		return "", err
	}

	// Update data with rendered components
	data.Navbar = navbar
	data.Content = chatArea

	// Render the complete layout
	return ch.renderer.RenderComponent("layout", data)
}

// ListComponents returns available component names
func (ch *ComponentHandler) ListComponents(w http.ResponseWriter, r *http.Request) {
	components := ch.renderer.GetComponentList()

	w.Header().Set("Content-Type", "application/json")
	// Simple JSON response
	response := `{"components":["` +
		joinStrings(components, `","`) +
		`"]}`

	w.Write([]byte(response))
}

// Helper function to join strings (avoiding external dependencies)
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	if len(strs) == 1 {
		return strs[0]
	}

	result := strs[0]
	for _, s := range strs[1:] {
		result += sep + s
	}
	return result
}

