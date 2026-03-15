package web

import (
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
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
		orihttp.BadRequest(w, "Missing component name")
		return
	}

	// Create default data - in a real application, this would come from the request context
	data := ComponentData{
		Title:        "Ori Agent",
		Theme:        "light",
		CurrentAgent: "No Agent Selected",
		Model:        "gpt-4",
	}

	// Render the component
	content, err := ch.renderer.RenderComponent(componentName, data)
	if err != nil {
		orihttp.InternalError(w, "Failed to render component: "+err.Error())
		return
	}
	orihttp.WriteHTML(w, content)
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
	orihttp.WriteJSON(w, map[string][]string{"components": components})
}
