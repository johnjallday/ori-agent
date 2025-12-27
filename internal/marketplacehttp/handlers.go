package marketplacehttp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/marketplace"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Handler provides HTTP handlers for marketplace management
type Handler struct {
	marketplaceStore *marketplace.Store
	registryManager  *registry.Manager
}

// NewHandler creates a new marketplace HTTP handler
func NewHandler(store *marketplace.Store, reg *registry.Manager) *Handler {
	return &Handler{
		marketplaceStore: store,
		registryManager:  reg,
	}
}

// AddMarketplaceRequest is the request body for adding a new marketplace
type AddMarketplaceRequest struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// ReorderRequest is the request body for reordering marketplaces
type ReorderRequest struct {
	IDs []string `json:"ids"`
}

// TestMarketplaceRequest is the request body for testing a marketplace
type TestMarketplaceRequest struct {
	Source string `json:"source"`
}

// TestMarketplaceResponse is the response for testing a marketplace
type TestMarketplaceResponse struct {
	Valid       bool   `json:"valid"`
	SourceType  string `json:"source_type"`
	ResolvedURL string `json:"resolved_url"`
	PluginCount int    `json:"plugin_count"`
	Error       string `json:"error,omitempty"`
}

// ListMarketplacesResponse wraps the marketplace list
type ListMarketplacesResponse struct {
	Marketplaces []types.Marketplace `json:"marketplaces"`
}

// ListMarketplaces returns all configured marketplaces
// GET /api/marketplaces
func (h *Handler) ListMarketplaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
		return
	}

	marketplaces := h.marketplaceStore.List()
	resp := ListMarketplacesResponse{Marketplaces: marketplaces}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": err})
		if encodeErr := orihttp.RespondInternalError(w, "Failed to encode response"); encodeErr != nil {
			logger.Error("Failed to write internal error response", logger.Fields{"error": encodeErr})
		}
	}
}

// AddMarketplace adds a new marketplace
// POST /api/marketplaces
func (h *Handler) AddMarketplace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
		return
	}

	var req AddMarketplaceRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Detect source type
	sourceType := types.DetectMarketplaceSourceType(strings.TrimSpace(req.Source))

	mp := types.Marketplace{
		Name:       req.Name,
		Source:     req.Source,
		SourceType: sourceType,
		Enabled:    true,
	}

	if err := h.marketplaceStore.Add(mp); err != nil {
		if encodeErr := orihttp.RespondBadRequest(w, fmt.Sprintf("Failed to add marketplace: %v", err)); encodeErr != nil {
			logger.Error("Failed to write bad request response", logger.Fields{"error": encodeErr})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": err})
		if encodeErr := orihttp.RespondInternalError(w, "Failed to encode response"); encodeErr != nil {
			logger.Error("Failed to write internal error response", logger.Fields{"error": encodeErr})
		}
	}
}

// UpdateMarketplace updates marketplace settings
// PUT /api/marketplaces/{id}
func (h *Handler) UpdateMarketplace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
		return
	}

	// Extract ID from URL path
	id := extractMarketplaceID(r.URL.Path)
	if id == "" {
		if err := orihttp.RespondBadRequest(w, "Marketplace ID required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	existing, err := h.marketplaceStore.Get(id)
	if err != nil {
		if encodeErr := orihttp.RespondNotFound(w, fmt.Sprintf("Marketplace not found: %s", id)); encodeErr != nil {
			logger.Error("Failed to write not found response", logger.Fields{"error": encodeErr})
		}
		return
	}

	// Parse update request
	var updates map[string]interface{}
	if !orihttp.ParseJSONBody(w, r, &updates) {
		return
	}

	if name, ok := updates["name"].(string); ok {
		existing.Name = name
	}
	if source, ok := updates["source"].(string); ok {
		existing.Source = source
	}
	if enabled, ok := updates["enabled"].(bool); ok {
		existing.Enabled = enabled
	}

	if err := h.marketplaceStore.Update(*existing); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Failed to update marketplace: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": err})
		if encodeErr := orihttp.RespondInternalError(w, "Failed to encode response"); encodeErr != nil {
			logger.Error("Failed to write internal error response", logger.Fields{"error": encodeErr})
		}
	}
}

// DeleteMarketplace removes a marketplace (except official)
// DELETE /api/marketplaces/{id}
func (h *Handler) DeleteMarketplace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
		return
	}

	// Extract ID from URL path
	id := extractMarketplaceID(r.URL.Path)
	if id == "" {
		if err := orihttp.RespondBadRequest(w, "Marketplace ID required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.marketplaceStore.Remove(id); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Failed to remove marketplace: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": err})
		if encodeErr := orihttp.RespondInternalError(w, "Failed to encode response"); encodeErr != nil {
			logger.Error("Failed to write internal error response", logger.Fields{"error": encodeErr})
		}
	}
}

// ReorderMarketplaces updates marketplace priority order
// POST /api/marketplaces/reorder
func (h *Handler) ReorderMarketplaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
		return
	}

	var req ReorderRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if err := h.marketplaceStore.Reorder(req.IDs); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Failed to reorder marketplaces: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": err})
		if encodeErr := orihttp.RespondInternalError(w, "Failed to encode response"); encodeErr != nil {
			logger.Error("Failed to write internal error response", logger.Fields{"error": encodeErr})
		}
	}
}

// TestMarketplace validates a marketplace URL and returns preview data
// POST /api/marketplaces/test
func (h *Handler) TestMarketplace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var req TestMarketplaceRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	resp := TestMarketplaceResponse{}

	source := strings.TrimSpace(req.Source)
	sourceType := types.DetectMarketplaceSourceType(source)
	resp.SourceType = sourceType
	if sourceType == "url" && (strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://")) {
		if strings.Contains(source, "gitlab.com") || strings.Contains(source, "gitlab.") {
			resp.SourceType = "gitlab"
		} else if strings.Contains(source, "bitbucket.org") || strings.Contains(source, "bitbucket.") {
			resp.SourceType = "bitbucket"
		} else if strings.Contains(source, "github.com") {
			resp.SourceType = "github"
		}
	}

	if sourceType == "file" {
		path, err := types.ResolveLocalMarketplacePath(source)
		if err != nil {
			resp.Valid = false
			resp.Error = err.Error()
			w.Header().Set("Content-Type", "application/json")
			orihttp.WriteJSON(w, resp)
			return
		}
		resp.ResolvedURL = path

		data, err := os.ReadFile(path)
		if err != nil {
			resp.Valid = false
			resp.Error = fmt.Sprintf("Failed to read file: %v", err)
			w.Header().Set("Content-Type", "application/json")
			orihttp.WriteJSON(w, resp)
			return
		}

		var reg types.PluginRegistry
		if err := json.Unmarshal(data, &reg); err != nil {
			var metaReg struct {
				Plugins []types.PluginMetadata `json:"plugins"`
			}
			if err := json.Unmarshal(data, &metaReg); err != nil {
				resp.Valid = false
				resp.Error = "Invalid plugin registry format"
				w.Header().Set("Content-Type", "application/json")
				orihttp.WriteJSON(w, resp)
				return
			}
			resp.PluginCount = len(metaReg.Plugins)
		} else {
			resp.PluginCount = len(reg.Plugins)
		}

		resp.Valid = true
		w.Header().Set("Content-Type", "application/json")
		orihttp.WriteJSON(w, resp)
		return
	}

	if sourceType == "url" && !strings.HasPrefix(source, "http://") && !strings.HasPrefix(source, "https://") {
		resp.Valid = false
		resp.Error = "Invalid source format. Use a URL, GitHub repo (user/repo), or absolute file path"
		w.Header().Set("Content-Type", "application/json")
		orihttp.WriteJSON(w, resp)
		return
	}

	// Create a temporary marketplace to leverage ResolveURL logic
	tempMarketplace := types.Marketplace{
		Source: source,
	}
	resp.ResolvedURL = tempMarketplace.ResolveURL()

	// Try to fetch and parse the registry
	httpResp, err := http.Get(resp.ResolvedURL)
	if err != nil {
		resp.Valid = false
		resp.Error = fmt.Sprintf("Failed to fetch: %v", err)
		w.Header().Set("Content-Type", "application/json")
		orihttp.WriteJSON(w, resp)
		return
	}
	defer func() { _ = httpResp.Body.Close() }()

	if httpResp.StatusCode != http.StatusOK {
		resp.Valid = false
		resp.Error = fmt.Sprintf("HTTP error: %d", httpResp.StatusCode)
		w.Header().Set("Content-Type", "application/json")
		orihttp.WriteJSON(w, resp)
		return
	}

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		resp.Valid = false
		resp.Error = fmt.Sprintf("Failed to read response: %v", err)
		w.Header().Set("Content-Type", "application/json")
		orihttp.WriteJSON(w, resp)
		return
	}

	// Try to parse as plugin registry
	var reg types.PluginRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		// Try metadata format
		var metaReg struct {
			Plugins []types.PluginMetadata `json:"plugins"`
		}
		if err := json.Unmarshal(data, &metaReg); err != nil {
			resp.Valid = false
			resp.Error = "Invalid plugin registry format"
			w.Header().Set("Content-Type", "application/json")
			orihttp.WriteJSON(w, resp)
			return
		}
		resp.PluginCount = len(metaReg.Plugins)
	} else {
		resp.PluginCount = len(reg.Plugins)
	}

	resp.Valid = true
	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, resp)
}

// RefreshMarketplace forces a refresh of a specific marketplace
// POST /api/marketplaces/{id}/refresh
func (h *Handler) RefreshMarketplace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
		return
	}

	// Extract ID from URL path (remove /refresh suffix)
	path := strings.TrimSuffix(r.URL.Path, "/refresh")
	id := extractMarketplaceID(path)
	if id == "" {
		if err := orihttp.RespondBadRequest(w, "Marketplace ID required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	mp, err := h.marketplaceStore.Get(id)
	if err != nil {
		if encodeErr := orihttp.RespondNotFound(w, fmt.Sprintf("Marketplace not found: %s", id)); encodeErr != nil {
			logger.Error("Failed to write not found response", logger.Fields{"error": encodeErr})
		}
		return
	}

	// Fetch from this marketplace
	reg, err := h.registryManager.FetchFromMarketplace(*mp)
	if err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to refresh marketplace: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "ok",
		"plugin_count": len(reg.Plugins),
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": err})
		if encodeErr := orihttp.RespondInternalError(w, "Failed to encode response"); encodeErr != nil {
			logger.Error("Failed to write internal error response", logger.Fields{"error": encodeErr})
		}
	}
}

// extractMarketplaceID extracts the marketplace ID from URL path
// e.g., /api/marketplaces/abc123 -> abc123
func extractMarketplaceID(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "marketplaces" {
		return parts[2]
	}
	return ""
}
