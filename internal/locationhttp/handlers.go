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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write method not allowed response", logger.Fields{"error": err})
		}
		return
	}

	var zone location.Zone
	if err := json.NewDecoder(r.Body).Decode(&zone); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Validate zone
	if zone.Name == "" {
		if err := orihttp.RespondBadRequest(w, "zone name is required"); err != nil {
			logger.Error("Failed to write bad request response", logger.Fields{"error": err})
		}
		return
	}

	if len(zone.DetectionRules) == 0 {
		if err := orihttp.RespondBadRequest(w, "at least one detection rule is required"); err != nil {
			logger.Error("Failed to write bad request response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract zone ID from URL path
				// URL format: /api/location/zones/{id}
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/location/zones/")
	zoneID := strings.Split(path, "/")[0]

	if zoneID == "" {
		if err := orihttp.RespondBadRequest(w, "zone ID is required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var zone location.Zone
	if err := json.NewDecoder(r.Body).Decode(&zone); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Ensure zone ID matches URL
	zone.ID = zoneID

	// Validate zone
	if zone.Name == "" {
		if err := orihttp.RespondBadRequest(w, "zone name is required"); err != nil {
			logger.

				// Update zone
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.manager.UpdateZone(zone); err != nil {
		if err.Error() == "zone not found" {
			if encodeErr := orihttp.RespondNotFound(w, err.Error()); encodeErr != nil {
				logger.Error("Failed to write not found response", logger.Fields{"error": encodeErr})
			}
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.

				// Extract zone ID from URL path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/location/zones/")
	zoneID := strings.Split(path, "/")[0]

	if zoneID == "" {
		if err := orihttp.RespondBadRequest(w, "zone ID is required"); err != nil {
			logger.

				// Delete zone
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.manager.RemoveZone(zoneID); err != nil {
		if err.Error() == "zone not found" {
			if encodeErr := orihttp.RespondNotFound(w, err.Error()); encodeErr != nil {
				logger.Error("Failed to write not found response", logger.Fields{"error": encodeErr})
			}
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var request struct {
		Location string `json:"location"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if request.Location == "" {
		if err := orihttp.RespondBadRequest(w, "location is required"); err != nil {
			logger.

				// Set manual location
				Error("Failed to write response", logger.Fields{"error": err})
		}
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
