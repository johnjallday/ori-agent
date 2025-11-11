package locationhttp

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/location"
)

// Handler handles location-related HTTP requests
type Handler struct {
	manager *location.Manager
}

// NewHandler creates a new location HTTP handler
func NewHandler(manager *location.Manager) *Handler {
	return &Handler{
		manager: manager,
	}
}

// GetCurrentLocation handles GET /api/location/current
func (h *Handler) GetCurrentLocation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	currentLocation := h.manager.GetCurrentLocation()

	response := struct {
		Location string `json:"location"`
	}{
		Location: currentLocation,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetZones handles GET /api/location/zones
func (h *Handler) GetZones(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	zones := h.manager.GetZones()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(zones)
}

// CreateZone handles POST /api/location/zones
func (h *Handler) CreateZone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var zone location.Zone
	if err := json.NewDecoder(r.Body).Decode(&zone); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate zone
	if zone.Name == "" {
		http.Error(w, "zone name is required", http.StatusBadRequest)
		return
	}

	if len(zone.DetectionRules) == 0 {
		http.Error(w, "at least one detection rule is required", http.StatusBadRequest)
		return
	}

	// Add zone
	if err := h.manager.AddZone(zone); err != nil {
		http.Error(w, "failed to add zone: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(zone)
}

// UpdateZone handles PUT /api/location/zones/:id
func (h *Handler) UpdateZone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract zone ID from URL path
	// URL format: /api/location/zones/{id}
	path := strings.TrimPrefix(r.URL.Path, "/api/location/zones/")
	zoneID := strings.Split(path, "/")[0]

	if zoneID == "" {
		http.Error(w, "zone ID is required", http.StatusBadRequest)
		return
	}

	var zone location.Zone
	if err := json.NewDecoder(r.Body).Decode(&zone); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Ensure zone ID matches URL
	zone.ID = zoneID

	// Validate zone
	if zone.Name == "" {
		http.Error(w, "zone name is required", http.StatusBadRequest)
		return
	}

	// Update zone
	if err := h.manager.UpdateZone(zone); err != nil {
		if err.Error() == "zone not found" {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, "failed to update zone: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(zone)
}

// DeleteZone handles DELETE /api/location/zones/:id
func (h *Handler) DeleteZone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract zone ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/location/zones/")
	zoneID := strings.Split(path, "/")[0]

	if zoneID == "" {
		http.Error(w, "zone ID is required", http.StatusBadRequest)
		return
	}

	// Delete zone
	if err := h.manager.RemoveZone(zoneID); err != nil {
		if err.Error() == "zone not found" {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, "failed to delete zone: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// SetManualLocation handles POST /api/location/override
func (h *Handler) SetManualLocation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		Location string `json:"location"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if request.Location == "" {
		http.Error(w, "location is required", http.StatusBadRequest)
		return
	}

	// Set manual location
	h.manager.SetManualLocation(request.Location)

	response := struct {
		Location string `json:"location"`
		Message  string `json:"message"`
	}{
		Location: request.Location,
		Message:  "Manual location set successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
