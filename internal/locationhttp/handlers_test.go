package locationhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/location"
)

// createTestManager creates a manager with test zones for testing
func createTestManager() *location.Manager {
	zones := []location.Zone{
		{
			ID:          "home",
			Name:        "Home",
			Description: "Home network",
			DetectionRules: []location.DetectionRule{
				location.WiFiRule{SSID: "HomeNetwork"},
			},
		},
	}

	manualDetector := location.NewManualDetector()
	wifiDetector := location.NewWiFiDetector()

	manager := location.NewManager([]location.Detector{manualDetector, wifiDetector}, zones)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager.Start(ctx, 60*time.Second)

	return manager
}

// TestGetCurrentLocation tests GET /api/location/current
func TestGetCurrentLocation(t *testing.T) {
	manager := createTestManager()
	handler := NewHandler(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/location/current", nil)
	w := httptest.NewRecorder()

	handler.GetCurrentLocation(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response struct {
		Location string `json:"location"`
	}

	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Errorf("Failed to decode response: %v", err)
	}

	if response.Location == "" {
		t.Error("Expected non-empty location")
	}
}

// TestGetZones tests GET /api/location/zones
func TestGetZones(t *testing.T) {
	manager := createTestManager()
	handler := NewHandler(manager)

	req := httptest.NewRequest(http.MethodGet, "/api/location/zones", nil)
	w := httptest.NewRecorder()

	handler.GetZones(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var zones []location.Zone
	if err := json.NewDecoder(w.Body).Decode(&zones); err != nil {
		t.Errorf("Failed to decode response: %v", err)
	}

	if len(zones) != 1 {
		t.Errorf("Expected 1 zone, got %d", len(zones))
	}

	if zones[0].Name != "Home" {
		t.Errorf("Expected zone name 'Home', got '%s'", zones[0].Name)
	}
}

// TestCreateZone tests POST /api/location/zones
func TestCreateZone(t *testing.T) {
	manager := createTestManager()
	handler := NewHandler(manager)

	newZone := location.Zone{
		Name:        "Office",
		Description: "Office network",
		DetectionRules: []location.DetectionRule{
			location.WiFiRule{SSID: "OfficeNetwork"},
		},
	}

	body, _ := json.Marshal(newZone)
	req := httptest.NewRequest(http.MethodPost, "/api/location/zones", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.CreateZone(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", w.Code)
	}

	// Verify zone was created
	zones := manager.GetZones()
	if len(zones) != 2 {
		t.Errorf("Expected 2 zones, got %d", len(zones))
	}
}

// TestCreateZoneInvalidRequest tests POST /api/location/zones with invalid data
func TestCreateZoneInvalidRequest(t *testing.T) {
	manager := createTestManager()
	handler := NewHandler(manager)

	// Test missing name
	invalidZone := location.Zone{
		Description: "Invalid zone",
		DetectionRules: []location.DetectionRule{
			location.WiFiRule{SSID: "TestNetwork"},
		},
	}

	body, _ := json.Marshal(invalidZone)
	req := httptest.NewRequest(http.MethodPost, "/api/location/zones", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.CreateZone(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	// Test missing detection rules
	invalidZone2 := location.Zone{
		Name:        "Invalid",
		Description: "Invalid zone",
	}

	body, _ = json.Marshal(invalidZone2)
	req = httptest.NewRequest(http.MethodPost, "/api/location/zones", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	handler.CreateZone(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for missing rules, got %d", w.Code)
	}
}

// TestUpdateZone tests PUT /api/location/zones/:id
func TestUpdateZone(t *testing.T) {
	manager := createTestManager()
	handler := NewHandler(manager)

	updatedZone := location.Zone{
		ID:          "home",
		Name:        "Home Updated",
		Description: "Updated home network",
		DetectionRules: []location.DetectionRule{
			location.WiFiRule{SSID: "HomeNetwork*"},
		},
	}

	body, _ := json.Marshal(updatedZone)
	req := httptest.NewRequest(http.MethodPut, "/api/location/zones/home", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.UpdateZone(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Verify zone was updated
	zone, err := manager.GetZoneByID("home")
	if err != nil {
		t.Errorf("Failed to get updated zone: %v", err)
	}

	if zone.Name != "Home Updated" {
		t.Errorf("Expected zone name 'Home Updated', got '%s'", zone.Name)
	}
}

// TestUpdateZoneNotFound tests PUT /api/location/zones/:id with non-existent zone
func TestUpdateZoneNotFound(t *testing.T) {
	manager := createTestManager()
	handler := NewHandler(manager)

	updatedZone := location.Zone{
		ID:          "nonexistent",
		Name:        "Nonexistent",
		Description: "This zone doesn't exist",
		DetectionRules: []location.DetectionRule{
			location.WiFiRule{SSID: "TestNetwork"},
		},
	}

	body, _ := json.Marshal(updatedZone)
	req := httptest.NewRequest(http.MethodPut, "/api/location/zones/nonexistent", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.UpdateZone(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

// TestDeleteZone tests DELETE /api/location/zones/:id
func TestDeleteZone(t *testing.T) {
	manager := createTestManager()
	handler := NewHandler(manager)

	req := httptest.NewRequest(http.MethodDelete, "/api/location/zones/home", nil)
	w := httptest.NewRecorder()

	handler.DeleteZone(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status 204, got %d", w.Code)
	}

	// Verify zone was deleted
	zones := manager.GetZones()
	if len(zones) != 0 {
		t.Errorf("Expected 0 zones after deletion, got %d", len(zones))
	}
}

// TestDeleteZoneNotFound tests DELETE /api/location/zones/:id with non-existent zone
func TestDeleteZoneNotFound(t *testing.T) {
	manager := createTestManager()
	handler := NewHandler(manager)

	req := httptest.NewRequest(http.MethodDelete, "/api/location/zones/nonexistent", nil)
	w := httptest.NewRecorder()

	handler.DeleteZone(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}
}

// TestSetManualLocation tests POST /api/location/override
func TestSetManualLocation(t *testing.T) {
	manager := createTestManager()
	handler := NewHandler(manager)

	request := struct {
		Location string `json:"location"`
	}{
		Location: "Manual Office",
	}

	body, _ := json.Marshal(request)
	req := httptest.NewRequest(http.MethodPost, "/api/location/override", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.SetManualLocation(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Wait for location to update
	time.Sleep(100 * time.Millisecond)

	// Verify manual location was set
	currentLocation := manager.GetCurrentLocation()
	if currentLocation != "Manual Office" {
		t.Errorf("Expected current location 'Manual Office', got '%s'", currentLocation)
	}
}

// TestSetManualLocationInvalidRequest tests POST /api/location/override with invalid data
func TestSetManualLocationInvalidRequest(t *testing.T) {
	manager := createTestManager()
	handler := NewHandler(manager)

	// Test empty location
	request := struct {
		Location string `json:"location"`
	}{
		Location: "",
	}

	body, _ := json.Marshal(request)
	req := httptest.NewRequest(http.MethodPost, "/api/location/override", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.SetManualLocation(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}
