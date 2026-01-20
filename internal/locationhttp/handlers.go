package locationhttp

import (
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
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
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}

	currentLocation := h.manager.GetCurrentLocation()

	response := struct {
		Location string `json:"location"`
	}{
		Location: currentLocation,
	}

	orihttp.WriteJSON(w, response)
}

// GetZones handles GET /api/location/zones
func (h *Handler) GetZones(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}

	zones := h.manager.GetZones()

	orihttp.WriteJSON(w, zones)
}

// CreateZone handles POST /api/location/zones
func (h *Handler) CreateZone(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
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

	w.WriteHeader(http.StatusCreated)
	orihttp.WriteJSON(w, zone)
}

// UpdateZone handles PUT /api/location/zones/:id
func (h *Handler) UpdateZone(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPut) {
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

	orihttp.WriteJSON(w, zone)
}

// DeleteZone handles DELETE /api/location/zones/:id
func (h *Handler) DeleteZone(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodDelete) {
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
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
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

	orihttp.WriteJSON(w, response)
}
