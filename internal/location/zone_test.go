package location

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWiFiRuleExactMatching tests exact SSID matching
func TestWiFiRuleExactMatching(t *testing.T) {
	rule := WiFiRule{SSID: "HomeNetwork"}

	if !rule.Matches("HomeNetwork") {
		t.Error("Expected exact match for 'HomeNetwork'")
	}

	if rule.Matches("OfficeNetwork") {
		t.Error("Expected no match for 'OfficeNetwork'")
	}

	if rule.Matches("HomeNetwork2") {
		t.Error("Expected no match for 'HomeNetwork2'")
	}
}

// TestWiFiRuleWildcardMatching tests wildcard SSID matching
func TestWiFiRuleWildcardMatching(t *testing.T) {
	rule := WiFiRule{SSID: "HomeNetwork*"}

	if !rule.Matches("HomeNetwork") {
		t.Error("Expected match for 'HomeNetwork'")
	}

	if !rule.Matches("HomeNetwork-2.4GHz") {
		t.Error("Expected match for 'HomeNetwork-2.4GHz'")
	}

	if !rule.Matches("HomeNetwork5G") {
		t.Error("Expected match for 'HomeNetwork5G'")
	}

	if rule.Matches("OfficeNetwork") {
		t.Error("Expected no match for 'OfficeNetwork'")
	}
}

// TestZoneCRUDOperations tests zone CRUD operations
func TestZoneCRUDOperations(t *testing.T) {
	manager := NewManager([]Detector{}, []Zone{})

	// Test AddZone
	zone1 := Zone{
		ID:          "home",
		Name:        "Home",
		Description: "Home network",
		DetectionRules: []DetectionRule{
			WiFiRule{SSID: "HomeNetwork"},
		},
	}

	err := manager.AddZone(zone1)
	if err != nil {
		t.Errorf("Failed to add zone: %v", err)
	}

	// Test GetZoneByID
	retrieved, err := manager.GetZoneByID("home")
	if err != nil {
		t.Errorf("Failed to get zone: %v", err)
	}

	if retrieved.Name != "Home" {
		t.Errorf("Expected zone name 'Home', got '%s'", retrieved.Name)
	}

	// Test GetZones
	zones := manager.GetZones()
	if len(zones) != 1 {
		t.Errorf("Expected 1 zone, got %d", len(zones))
	}

	// Test UpdateZone
	zone1.Description = "Updated description"
	err = manager.UpdateZone(zone1)
	if err != nil {
		t.Errorf("Failed to update zone: %v", err)
	}

	retrieved, _ = manager.GetZoneByID("home")
	if retrieved.Description != "Updated description" {
		t.Errorf("Expected updated description, got '%s'", retrieved.Description)
	}

	// Test RemoveZone
	err = manager.RemoveZone("home")
	if err != nil {
		t.Errorf("Failed to remove zone: %v", err)
	}

	zones = manager.GetZones()
	if len(zones) != 0 {
		t.Errorf("Expected 0 zones after removal, got %d", len(zones))
	}
}

// TestZonePersistence tests saving and loading zones
func TestZonePersistence(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	zonePath := filepath.Join(tmpDir, "zones.json")

	// Create manager with zones
	zones := []Zone{
		{
			ID:          "home",
			Name:        "Home",
			Description: "Home network",
			DetectionRules: []DetectionRule{
				WiFiRule{SSID: "HomeNetwork"},
			},
		},
		{
			ID:          "office",
			Name:        "Office",
			Description: "Office network",
			DetectionRules: []DetectionRule{
				WiFiRule{SSID: "OfficeNetwork*"},
			},
		},
	}

	manager := NewManager([]Detector{}, zones)

	// Save zones
	err := manager.SaveZones(zonePath)
	if err != nil {
		t.Errorf("Failed to save zones: %v", err)
	}

	// Check file permissions
	info, err := os.Stat(zonePath)
	if err != nil {
		t.Errorf("Failed to stat zone file: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("Expected file permissions 0600, got %o", perm)
	}

	// Load zones
	loadedZones, err := LoadZones(zonePath)
	if err != nil {
		t.Errorf("Failed to load zones: %v", err)
	}

	if len(loadedZones) != 2 {
		t.Errorf("Expected 2 zones, got %d", len(loadedZones))
	}

	// Verify zone data
	var homeZone Zone
	for _, z := range loadedZones {
		if z.ID == "home" {
			homeZone = z
			break
		}
	}

	if homeZone.Name != "Home" {
		t.Errorf("Expected zone name 'Home', got '%s'", homeZone.Name)
	}

	if len(homeZone.DetectionRules) != 1 {
		t.Errorf("Expected 1 detection rule, got %d", len(homeZone.DetectionRules))
	}
}

// TestLoadZonesMissingFile tests loading zones from a non-existent file
func TestLoadZonesMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	zonePath := filepath.Join(tmpDir, "nonexistent.json")

	zones, err := LoadZones(zonePath)
	if err != nil {
		t.Errorf("Expected no error for missing file, got: %v", err)
	}

	if len(zones) != 0 {
		t.Errorf("Expected 0 zones for missing file, got %d", len(zones))
	}
}

// TestLoadZonesCorruptedFile tests handling of corrupted JSON
func TestLoadZonesCorruptedFile(t *testing.T) {
	tmpDir := t.TempDir()
	zonePath := filepath.Join(tmpDir, "corrupted.json")

	// Write corrupted JSON
	err := os.WriteFile(zonePath, []byte("invalid json{{{"), 0600)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	_, err = LoadZones(zonePath)
	if err == nil {
		t.Error("Expected error for corrupted JSON file")
	}
}

// TestAddZoneValidation tests zone validation
func TestAddZoneValidation(t *testing.T) {
	manager := NewManager([]Detector{}, []Zone{})

	// Test missing name
	err := manager.AddZone(Zone{
		ID: "test",
		DetectionRules: []DetectionRule{
			WiFiRule{SSID: "Test"},
		},
	})

	if err == nil {
		t.Error("Expected error for missing zone name")
	}

	// Test auto-generated ID
	zone := Zone{
		Name:        "Test",
		Description: "Test zone",
		DetectionRules: []DetectionRule{
			WiFiRule{SSID: "TestNetwork"},
		},
	}

	err = manager.AddZone(zone)
	if err != nil {
		t.Errorf("Failed to add zone: %v", err)
	}

	zones := manager.GetZones()
	if len(zones) != 1 {
		t.Errorf("Expected 1 zone, got %d", len(zones))
	}

	if zones[0].ID == "" {
		t.Error("Expected auto-generated ID")
	}
}
