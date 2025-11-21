package pluginhttp

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// WebPageHandler serves custom web pages from plugins
type WebPageHandler struct {
	State store.Store
}

// NewWebPageHandler creates a new web page handler
func NewWebPageHandler(state store.Store) *WebPageHandler {
	return &WebPageHandler{
		State: state,
	}
}

// ServeHTTP handles plugin web page requests
// URL format: /api/plugins/{plugin-name}/pages/{page-path}
func (h *WebPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse URL path: /api/plugins/{plugin-name}/pages/{page-path}
	path := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	parts := strings.SplitN(path, "/pages/", 2)

	if len(parts) != 2 {
		http.Error(w, "Invalid URL format. Expected: /api/plugins/{plugin-name}/pages/{page-path}", http.StatusBadRequest)
		return
	}

	pluginName := parts[0]
	pagePath := parts[1]

	// Get current agent
	_, current := h.State.ListAgents()
	ag, ok := h.State.GetAgent(current)
	if !ok {
		http.Error(w, "Current agent not found", http.StatusInternalServerError)
		return
	}

	// Find the plugin
	loadedPlugin, exists := ag.Plugins[pluginName]
	if !exists {
		http.Error(w, fmt.Sprintf("Plugin '%s' not found or not loaded", pluginName), http.StatusNotFound)
		return
	}

	// Check if plugin implements WebPageProvider
	webProvider, ok := loadedPlugin.Tool.(pluginapi.WebPageProvider)
	if !ok {
		http.Error(w, fmt.Sprintf("Plugin '%s' does not support web pages", pluginName), http.StatusNotImplemented)
		return
	}

	// Parse query parameters
	queryParams := make(map[string]string)
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			queryParams[key] = values[0] // Take first value for simplicity
		}
	}

	// Serve the page
	content, contentType, err := webProvider.ServeWebPage(pagePath, queryParams)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error serving page: %v", err), http.StatusInternalServerError)
		return
	}

	// Set content type
	if contentType == "" {
		contentType = "text/html; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)

	// Write content
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(content)); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

// ListPages returns available pages for a plugin
// URL format: /api/plugins/{plugin-name}/pages
func (h *WebPageHandler) ListPages(w http.ResponseWriter, r *http.Request) {
	// Parse URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	pluginName := strings.TrimSuffix(path, "/pages")

	// Get current agent
	_, current := h.State.ListAgents()
	ag, ok := h.State.GetAgent(current)
	if !ok {
		http.Error(w, "Current agent not found", http.StatusInternalServerError)
		return
	}

	// Find the plugin
	loadedPlugin, exists := ag.Plugins[pluginName]
	if !exists {
		http.Error(w, fmt.Sprintf("Plugin '%s' not found or not loaded", pluginName), http.StatusNotFound)
		return
	}

	// Check if plugin implements WebPageProvider
	webProvider, ok := loadedPlugin.Tool.(pluginapi.WebPageProvider)
	if !ok {
		// Plugin doesn't provide web pages, return empty list
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"pages":[]}`)); err != nil {
			log.Printf("Failed to write response: %v", err)
		}
		return
	}

	// Get available pages
	pages := webProvider.GetWebPages()

	// Return as JSON
	w.Header().Set("Content-Type", "application/json")
	response := fmt.Sprintf(`{"pages":[%s]}`, strings.Join(quoteStrings(pages), ","))
	if _, err := w.Write([]byte(response)); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

// Helper function to quote strings for JSON
func quoteStrings(strs []string) []string {
	quoted := make([]string, len(strs))
	for i, s := range strs {
		quoted[i] = fmt.Sprintf(`"%s"`, s)
	}
	return quoted
}
