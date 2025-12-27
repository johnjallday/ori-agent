package locationhttp

import (
	"encoding/json"

	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/location"
	"github.com/johnjallday/ori-agent/internal/logger"
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
		orihttp.MethodNotAllowed(w)
		return
	}

	currentLocation := h.manager.GetCurrentLocation()

	response := struct {
		Location string `json:"location"`
	}{
		Location: currentLocation,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// GetZones handles GET /api/location/zones
func (h *Handler) GetZones(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	zones := h.manager.GetZones()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(zones); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// CreateZone handles POST /api/location/zones
func (h *Handler) CreateZone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var zone location.Zone
	if !orihttp.ParseJSONBody(w, r, &zone) {
		return
	}

	// Validate zone
	if zone.Name == "" {
		orihttp.BadRequest(w, "zone name is required")
		return
	}

	if len(zone.DetectionRules) == 0 {
		orihttp.BadRequest(w, "at least one detection rule is required")
		return
	}

	// Add zone
	if err := h.manager.AddZone(zone); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "failed to add zone", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(zone); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// UpdateZone handles PUT /api/location/zones/:id
func (h *Handler) UpdateZone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract zone ID from URL path
	// URL format: /api/location/zones/{id}

	path := strings.TrimPrefix(r.URL.Path, "/api/location/zones/")
	zoneID := strings.Split(path, "/")[0]

	if zoneID == "" {
		orihttp.BadRequest(w, "zone ID is required")
		return
	}

	var zone location.Zone
	if !orihttp.ParseJSONBody(w, r, &zone) {
		return
	}

	// Ensure zone ID matches URL
	zone.ID = zoneID

	// Validate zone
	if zone.Name == "" {
		orihttp.BadRequest(w, "zone name is required")
		return
	}

	if err := h.manager.UpdateZone(zone); err != nil {
		if err.Error() == "zone not found" {
			orihttp.NotFound(w, err.Error())
		} else {
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "failed to update zone", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(zone); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// DeleteZone handles DELETE /api/location/zones/:id
func (h *Handler) DeleteZone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		orihttp.MethodNotAllowed(w)
		return
	}

	// Extract zone ID from URL path

	path := strings.TrimPrefix(r.URL.Path, "/api/location/zones/")
	zoneID := strings.Split(path, "/")[0]

	if zoneID == "" {
		orihttp.BadRequest(w, "zone ID is required")
		return
	}

	if err := h.manager.RemoveZone(zoneID); err != nil {
		if err.Error() == "zone not found" {
			orihttp.NotFound(w, err.Error())
		} else {
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "failed to delete zone", err)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// SetManualLocation handles POST /api/location/override
func (h *Handler) SetManualLocation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	var request struct {
		Location string `json:"location"`
	}

	if !orihttp.ParseJSONBody(w, r, &request) {
		return
	}

	if request.Location == "" {
		orihttp.BadRequest(w, "location is required")
		return
	}

	h.manager.SetManualLocation(request.Location)

	response := struct {
		Location string `json:"location"`
		Message  string `json:"message"`
	}{
		Location: request.Location,
		Message:  "Manual location set successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}
