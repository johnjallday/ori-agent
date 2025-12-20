package web

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
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
	// Load all components at startup - errors are non-fatal, components load on-demand
	_ = renderer.LoadAllComponents()

	return &ComponentHandler{
		renderer: renderer,
	}
}

// ServeComponent serves an individual component
func (ch *ComponentHandler) ServeComponent(w http.ResponseWriter, r *http.Request) {
	// Extract component name from URL path
	componentName := r.URL.Query().Get("name")
	if componentName == "" {
		if err := orihttp.RespondBadRequest(w, "Missing component name"); err != nil {
			logger.Error("Failed to write bad request response", logger.Fields{"error": err})
		}
		return
	}

	// Create default data - in a real application, this would come from the request context
	data := ComponentData{
		Title:        "Ori Agent",
		Theme:        "light",
		CurrentAgent: "default",
		Model:        "gpt-4",
	}

	// Render the component
	content, err := ch.renderer.RenderComponent(componentName, data)
	if err != nil {
		if encodeErr := orihttp.RespondInternalError(w, "Failed to render component: "+err.Error()); encodeErr != nil {
			logger.Error("Failed to write internal error response", logger.Fields{"error": encodeErr})
		}
		return
	}

	// Set content type
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(content)); err != nil {
		logger.Error("Failed to write response", logger.Fields{"response": err})
	}
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

	if _, err := w.Write([]byte(response)); err != nil {

		logger.Error("Failed to write response", logger.Fields{"response": err})

	}
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
