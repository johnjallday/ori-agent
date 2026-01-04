package pluginhttp

import (
	"fmt"

	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	web "github.com/johnjallday/ori-agent/internal/web"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// WebPageHandler serves custom web pages from plugins
type WebPageHandler struct {
	State            store.Store
	TemplateRenderer *web.TemplateRenderer
}

// NewWebPageHandler creates a new web page handler
func NewWebPageHandler(state store.Store, renderer *web.TemplateRenderer) *WebPageHandler {
	return &WebPageHandler{
		State:            state,
		TemplateRenderer: renderer,
	}
}

// ServeHTTP handles plugin web page requests
// URL format: /api/plugins/{plugin-name}/{page-path}
func (h *WebPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse URL path: /api/plugins/{plugin-name}/{page-path}
	path := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	parts := strings.SplitN(path, "/", 2)

	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		orihttp.BadRequest(w, "Invalid URL format. Expected: /api/plugins/{plugin-name}/{page-path}")
		return
	}

	pluginName := parts[0]
	pagePath := parts[1]

	_, current := h.State.ListAgents()
	ag, ok := h.State.GetAgent(current)
	if !ok {
		orihttp.InternalError(w, "Current agent not found")
		// Find the plugin
		return
	}

	loadedPlugin, exists := ag.Plugins[pluginName]
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("Plugin '%s' not found or not loaded", pluginName))
		// Check if plugin implements WebPageProvider
		return
	}

	webProvider, ok := loadedPlugin.Tool.(pluginapi.WebPageProvider)
	if !ok {
		orihttp.NotImplemented(w, fmt.Sprintf("Plugin '%s' does not support web pages", pluginName))
		return
	}

	if h.shouldWrapPage(r, pagePath, webProvider) {
		wrapped, err := h.renderPluginWrapper(r, pluginName, pagePath)
		if err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			if _, writeErr := w.Write([]byte(wrapped)); writeErr != nil {
				logger.Error("Failed to write response", logger.Fields{"error": writeErr})
			}
			return
		}
		logger.Warn("Failed to render plugin page wrapper", logger.Fields{"error": err, "plugin": pluginName, "page": pagePath})
	}

	// Parse query parameters
	queryParams := make(map[string]string)
	for key, values := range r.URL.Query() {
		if key == "ori_raw" {
			continue
		}
		if len(values) > 0 {
			queryParams[key] = values[0] // Take first value for simplicity
		}
	}

	// Serve the page
	content, contentType, err := webProvider.ServeWebPage(pagePath, queryParams)
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Error serving page: %v", err))
		// Set content type
		return
	}

	if contentType == "" {
		contentType = "text/html; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)

	// Write content
	w.WriteHeader(http.StatusOK)
	if _, writeErr := w.Write([]byte(content)); writeErr != nil {
		logger.Error("Failed to write response", logger.Fields{"error": writeErr})
	}
}

func (h *WebPageHandler) shouldWrapPage(r *http.Request, pagePath string, webProvider pluginapi.WebPageProvider) bool {
	if h.TemplateRenderer == nil {
		return false
	}
	if r.URL.Query().Get("ori_raw") == "1" {
		return false
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		return false
	}
	return containsString(webProvider.GetWebPages(), pagePath)
}

func (h *WebPageHandler) renderPluginWrapper(r *http.Request, pluginName, pagePath string) (string, error) {
	rawURL := *r.URL
	query := rawURL.Query()
	query.Set("ori_raw", "1")
	rawURL.RawQuery = query.Encode()

	data := web.GetDefaultData()
	data.Title = fmt.Sprintf("%s/%s - Ori Agent", pluginName, pagePath)
	data.BrandText = "Ori Agent"
	data.ShowSidebarToggle = false
	data.ShowNavLinks = true
	if data.Extra == nil {
		data.Extra = make(map[string]interface{})
	}
	data.Extra["PluginPageURL"] = rawURL.String()
	data.Extra["PluginPageTitle"] = fmt.Sprintf("%s/%s", pluginName, pagePath)

	return h.TemplateRenderer.RenderTemplate("plugin-page", data)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
		orihttp.InternalError(w, "Current agent not found")
		// Find the plugin
		return
	}

	loadedPlugin, exists := ag.Plugins[pluginName]
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("Plugin '%s' not found or not loaded", pluginName))
		// Check if plugin implements WebPageProvider
		return
	}

	webProvider, ok := loadedPlugin.Tool.(pluginapi.WebPageProvider)
	if !ok {
		// Plugin doesn't provide web pages, return empty list
		w.Header().Set("Content-Type", "application/json")
		if _, writeErr := w.Write([]byte(`{"pages":[]}`)); writeErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": writeErr})
		}
		return
	}

	// Get available pages
	pages := webProvider.GetWebPages()

	// Return as JSON
	w.Header().Set("Content-Type", "application/json")
	response := fmt.Sprintf(`{"pages":[%s]}`, strings.Join(quoteStrings(pages), ","))
	if _, writeErr := w.Write([]byte(response)); writeErr != nil {
		logger.Error("Failed to write response", logger.Fields{"error": writeErr})
	}
}

// ListAllPages returns all available web pages from all loaded plugins
// URL format: /api/plugins/all-pages
func (h *WebPageHandler) ListAllPages(w http.ResponseWriter, r *http.Request) {
	// Get current agent
	_, current := h.State.ListAgents()
	ag, ok := h.State.GetAgent(current)
	if !ok {
		orihttp.InternalError(w, "Current agent not found")
		return
	}

	type PluginPage struct {
		Plugin string `json:"plugin"`
		Page   string `json:"page"`
		URL    string `json:"url"`
	}

	var allPages []PluginPage

	// Iterate through all loaded plugins
	for pluginName, loadedPlugin := range ag.Plugins {
		webProvider, ok := loadedPlugin.Tool.(pluginapi.WebPageProvider)
		if !ok {
			continue
		}

		// Get available pages for this plugin
		pages := webProvider.GetWebPages()
		for _, page := range pages {
			allPages = append(allPages, PluginPage{
				Plugin: pluginName,
				Page:   page,
				URL:    fmt.Sprintf("/api/plugins/%s/%s", pluginName, page),
			})
		}
	}

	// Return as JSON
	w.Header().Set("Content-Type", "application/json")

	// Build JSON manually to avoid import
	var pagesJSON []string
	for _, p := range allPages {
		pagesJSON = append(pagesJSON, fmt.Sprintf(`{"plugin":"%s","page":"%s","url":"%s"}`, p.Plugin, p.Page, p.URL))
	}
	response := fmt.Sprintf(`{"pages":[%s]}`, strings.Join(pagesJSON, ","))

	if _, writeErr := w.Write([]byte(response)); writeErr != nil {
		logger.Error("Failed to write response", logger.Fields{"error": writeErr})
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
