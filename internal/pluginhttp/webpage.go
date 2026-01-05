package pluginhttp

import (
	"fmt"
	"path/filepath"
	"sync"

	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	web "github.com/johnjallday/ori-agent/internal/web"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// WebPageHandler serves custom web pages from plugins
type WebPageHandler struct {
	State            store.Store
	TemplateRenderer *web.TemplateRenderer
	Loader           ToolLoader
	pluginLoadMu     sync.Mutex
}

// NewWebPageHandler creates a new web page handler
func NewWebPageHandler(state store.Store, renderer *web.TemplateRenderer) *WebPageHandler {
	return &WebPageHandler{
		State:            state,
		TemplateRenderer: renderer,
	}
}

// SetLoader sets the plugin loader for lazy loading plugins
func (h *WebPageHandler) SetLoader(loader ToolLoader) {
	h.Loader = loader
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
		return
	}

	// Lazy load the plugin if not yet loaded
	tool := loadedPlugin.Tool
	if tool == nil && h.Loader != nil {
		loadedTool, err := h.ensurePluginLoaded(current, pluginName, loadedPlugin)
		if err != nil {
			orihttp.InternalError(w, fmt.Sprintf("Failed to load plugin '%s': %v", pluginName, err))
			return
		}
		tool = loadedTool
	}

	if tool == nil {
		orihttp.InternalError(w, fmt.Sprintf("Plugin '%s' could not be loaded", pluginName))
		return
	}

	webProvider, ok := tool.(pluginapi.WebPageProvider)
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
		return
	}

	loadedPlugin, exists := ag.Plugins[pluginName]
	if !exists {
		orihttp.NotFound(w, fmt.Sprintf("Plugin '%s' not found or not loaded", pluginName))
		return
	}

	// Lazy load the plugin if not yet loaded
	tool := loadedPlugin.Tool
	if tool == nil && h.Loader != nil {
		loadedTool, err := h.ensurePluginLoaded(current, pluginName, loadedPlugin)
		if err != nil {
			// Plugin couldn't be loaded, return empty list
			w.Header().Set("Content-Type", "application/json")
			if _, writeErr := w.Write([]byte(`{"pages":[]}`)); writeErr != nil {
				logger.Error("Failed to write response", logger.Fields{"error": writeErr})
			}
			return
		}
		tool = loadedTool
	}

	if tool == nil {
		w.Header().Set("Content-Type", "application/json")
		if _, writeErr := w.Write([]byte(`{"pages":[]}`)); writeErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": writeErr})
		}
		return
	}

	webProvider, ok := tool.(pluginapi.WebPageProvider)
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
		// Lazy load the plugin if not yet loaded
		tool := loadedPlugin.Tool
		if tool == nil && h.Loader != nil {
			loadedTool, err := h.ensurePluginLoaded(current, pluginName, loadedPlugin)
			if err != nil {
				logger.Debug("Failed to lazy load plugin for web pages", logger.Fields{
					"plugin": pluginName,
					"error":  err.Error(),
				})
				continue
			}
			tool = loadedTool
		}

		if tool == nil {
			continue
		}

		webProvider, ok := tool.(pluginapi.WebPageProvider)
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

// ensurePluginLoaded lazy loads a plugin if not already loaded
func (h *WebPageHandler) ensurePluginLoaded(agentName, pluginName string, lp types.LoadedPlugin) (pluginapi.PluginTool, error) {
	h.pluginLoadMu.Lock()
	defer h.pluginLoadMu.Unlock()

	// Double-check if already loaded (another goroutine may have loaded it)
	ag, ok := h.State.GetAgent(agentName)
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", agentName)
	}
	if existing, exists := ag.Plugins[pluginName]; exists && existing.Tool != nil {
		return existing.Tool, nil
	}

	logger.Info("Lazy loading plugin for web pages", logger.Fields{
		"plugin": pluginName,
		"agent":  agentName,
	})

	// Load the plugin
	tool, err := h.Loader.Load(lp.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to load plugin %s: %w", pluginName, err)
	}

	// Set agent context
	agentSpecificStorePath := filepath.Join("agents", agentName, "config.json")
	if abs, err := filepath.Abs(agentSpecificStorePath); err == nil {
		agentSpecificStorePath = abs
	}
	pluginloader.SetAgentContext(tool, agentName, agentSpecificStorePath, "")

	// Update the plugin in the agent
	lp.Tool = tool
	lp.Definition = tool.Definition()

	// Get file support info
	supportsFiles, acceptedFileTypes := pluginloader.GetPluginFileSupport(tool)
	lp.SupportsFiles = supportsFiles
	lp.AcceptedFileTypes = acceptedFileTypes

	ag.Plugins[pluginName] = lp

	// Save the updated agent (ignore errors, non-critical)
	if err := h.State.SetAgent(agentName, ag); err != nil {
		logger.Warn("Failed to save agent after lazy loading plugin", logger.Fields{
			"agent":  agentName,
			"plugin": pluginName,
			"error":  err.Error(),
		})
	}

	return tool, nil
}

// Helper function to quote strings for JSON
func quoteStrings(strs []string) []string {
	quoted := make([]string, len(strs))
	for i, s := range strs {
		quoted[i] = fmt.Sprintf(`"%s"`, s)
	}
	return quoted
}
