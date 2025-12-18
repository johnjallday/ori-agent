package marketplacehttp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	marketplaces := h.marketplaceStore.List()
	resp := ListMarketplacesResponse{Marketplaces: marketplaces}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// AddMarketplace adds a new marketplace
// POST /api/marketplaces
func (h *Handler) AddMarketplace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AddMarketplaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Detect source type
	sourceType := "url"
	source := strings.TrimSpace(req.Source)
	if !strings.HasPrefix(source, "http://") && !strings.HasPrefix(source, "https://") {
		parts := strings.Split(source, "/")
		if len(parts) == 2 && len(parts[0]) > 0 && len(parts[1]) > 0 {
			sourceType = "github"
		}
	}

	mp := types.Marketplace{
		Name:       req.Name,
		Source:     req.Source,
		SourceType: sourceType,
		Enabled:    true,
	}

	if err := h.marketplaceStore.Add(mp); err != nil {
		http.Error(w, fmt.Sprintf("Failed to add marketplace: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// UpdateMarketplace updates marketplace settings
// PUT /api/marketplaces/{id}
func (h *Handler) UpdateMarketplace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from URL path
	id := extractMarketplaceID(r.URL.Path)
	if id == "" {
		http.Error(w, "Marketplace ID required", http.StatusBadRequest)
		return
	}

	// Get existing marketplace
	existing, err := h.marketplaceStore.Get(id)
	if err != nil {
		http.Error(w, fmt.Sprintf("Marketplace not found: %s", id), http.StatusNotFound)
		return
	}

	// Parse update request
	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Apply updates
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
		http.Error(w, fmt.Sprintf("Failed to update marketplace: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// DeleteMarketplace removes a marketplace (except official)
// DELETE /api/marketplaces/{id}
func (h *Handler) DeleteMarketplace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from URL path
	id := extractMarketplaceID(r.URL.Path)
	if id == "" {
		http.Error(w, "Marketplace ID required", http.StatusBadRequest)
		return
	}

	if err := h.marketplaceStore.Remove(id); err != nil {
		http.Error(w, fmt.Sprintf("Failed to remove marketplace: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// ReorderMarketplaces updates marketplace priority order
// POST /api/marketplaces/reorder
func (h *Handler) ReorderMarketplaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ReorderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.marketplaceStore.Reorder(req.IDs); err != nil {
		http.Error(w, fmt.Sprintf("Failed to reorder marketplaces: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// TestMarketplace validates a marketplace URL and returns preview data
// POST /api/marketplaces/test
func (h *Handler) TestMarketplace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req TestMarketplaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	resp := TestMarketplaceResponse{}

	// Detect source type
	source := strings.TrimSpace(req.Source)
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		resp.SourceType = "url"
		resp.ResolvedURL = source
	} else {
		parts := strings.Split(source, "/")
		if len(parts) == 2 && len(parts[0]) > 0 && len(parts[1]) > 0 {
			resp.SourceType = "github"
			resp.ResolvedURL = fmt.Sprintf("https://raw.githubusercontent.com/%s/main/plugin_registry.json", source)
		} else {
			resp.Valid = false
			resp.Error = "Invalid source format. Use a URL or GitHub repo (user/repo)"
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
	}

	// Try to fetch and parse the registry
	httpResp, err := http.Get(resp.ResolvedURL)
	if err != nil {
		resp.Valid = false
		resp.Error = fmt.Sprintf("Failed to fetch: %v", err)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	defer func() { _ = httpResp.Body.Close() }()

	if httpResp.StatusCode != http.StatusOK {
		resp.Valid = false
		resp.Error = fmt.Sprintf("HTTP error: %d", httpResp.StatusCode)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		resp.Valid = false
		resp.Error = fmt.Sprintf("Failed to read response: %v", err)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
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
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		resp.PluginCount = len(metaReg.Plugins)
	} else {
		resp.PluginCount = len(reg.Plugins)
	}

	resp.Valid = true
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// RefreshMarketplace forces a refresh of a specific marketplace
// POST /api/marketplaces/{id}/refresh
func (h *Handler) RefreshMarketplace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from URL path (remove /refresh suffix)
	path := strings.TrimSuffix(r.URL.Path, "/refresh")
	id := extractMarketplaceID(path)
	if id == "" {
		http.Error(w, "Marketplace ID required", http.StatusBadRequest)
		return
	}

	mp, err := h.marketplaceStore.Get(id)
	if err != nil {
		http.Error(w, fmt.Sprintf("Marketplace not found: %s", id), http.StatusNotFound)
		return
	}

	// Fetch from this marketplace
	reg, err := h.registryManager.FetchFromMarketplace(*mp)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to refresh marketplace: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "ok",
		"plugin_count": len(reg.Plugins),
	}); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
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
