package location

import (
	"context"
	"sync"
	"testing"
	"time"
)

// MockDetector is a mock detector for testing
type MockDetector struct {
	name        string
	returnValue string
	returnError error
}

func (m *MockDetector) Name() string {
	return m.name
}

func (m *MockDetector) Detect(ctx context.Context) (string, error) {
	if m.returnError != nil {
		return "", m.returnError
	}
	return m.returnValue, nil
}

// TestLocationDetectionWithSingleDetector tests detection with one detector
func TestLocationDetectionWithSingleDetector(t *testing.T) {
	detector := &MockDetector{
		name:        "mock-wifi",
		returnValue: "TestNetwork",
		returnError: nil,
	}

	zone := Zone{
		ID:          "home",
		Name:        "Home",
		Description: "Home network",
		DetectionRules: []DetectionRule{
			WiFiRule{SSID: "TestNetwork"},
		},
	}

	manager := NewManager([]Detector{detector}, []Zone{zone})

	// Perform detection
	value, method := manager.detectLocation()

	if value != "TestNetwork" {
		t.Errorf("Expected detected value 'TestNetwork', got '%s'", value)
	}

	if method != DetectionMethodWiFi {
		t.Errorf("Expected detection method WiFi, got %s", method)
	}
}

// TestFallbackToSecondDetector tests fallback when first detector fails
func TestFallbackToSecondDetector(t *testing.T) {
	detector1 := &MockDetector{
		name:        "failing-detector",
		returnValue: "",
		returnError: context.DeadlineExceeded,
	}

	detector2 := &MockDetector{
		name:        "working-detector",
		returnValue: "BackupNetwork",
		returnError: nil,
	}

	zone := Zone{
		ID:          "office",
		Name:        "Office",
		Description: "Office network",
		DetectionRules: []DetectionRule{
			WiFiRule{SSID: "BackupNetwork"},
		},
	}

	manager := NewManager([]Detector{detector1, detector2}, []Zone{zone})

	// Perform detection
	value, _ := manager.detectLocation()

	if value != "BackupNetwork" {
		t.Errorf("Expected detected value 'BackupNetwork', got '%s'", value)
	}
}

// TestZoneMatching tests zone matching logic
func TestZoneMatching(t *testing.T) {
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
				WiFiRule{SSID: "OfficeNetwork"},
			},
		},
	}

	manager := NewManager([]Detector{}, zones)

	// Test matching home network
	zoneName := manager.matchZone("HomeNetwork")
	if zoneName != "Home" {
		t.Errorf("Expected zone name 'Home', got '%s'", zoneName)
	}

	// Test matching office network
	zoneName = manager.matchZone("OfficeNetwork")
	if zoneName != "Office" {
		t.Errorf("Expected zone name 'Office', got '%s'", zoneName)
	}

	// Test no match
	zoneName = manager.matchZone("UnknownNetwork")
	if zoneName != "Unknown" {
		t.Errorf("Expected zone name 'Unknown', got '%s'", zoneName)
	}
}

// TestLocationChangeEvent tests location change event emission
func TestLocationChangeEvent(t *testing.T) {
	detector := &MockDetector{
		name:        "mock-wifi",
		returnValue: "TestNetwork",
		returnError: nil,
	}

	zone := Zone{
		ID:          "home",
		Name:        "Home",
		Description: "Home network",
		DetectionRules: []DetectionRule{
			WiFiRule{SSID: "TestNetwork"},
		},
	}

	manager := NewManager([]Detector{detector}, []Zone{zone})

	// Register event callback
	var mu sync.Mutex
	eventReceived := false
	var receivedEvent LocationChangeEvent

	manager.OnLocationChange(func(event LocationChangeEvent) {
		mu.Lock()
		defer mu.Unlock()
		eventReceived = true
		receivedEvent = event
	})

	// Trigger detection
	manager.detectAndUpdate()

	// Wait for event to be processed
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if !eventReceived {
		t.Error("Expected location change event to be received")
	}

	if receivedEvent.CurrentLocation != "Home" {
		t.Errorf("Expected current location 'Home', got '%s'", receivedEvent.CurrentLocation)
	}

	if receivedEvent.PreviousLocation != "Unknown" {
		t.Errorf("Expected previous location 'Unknown', got '%s'", receivedEvent.PreviousLocation)
	}
}

// TestDuplicateEventPrevention tests that duplicate events are not emitted
func TestDuplicateEventPrevention(t *testing.T) {
	detector := &MockDetector{
		name:        "mock-wifi",
		returnValue: "TestNetwork",
		returnError: nil,
	}

	zone := Zone{
		ID:          "home",
		Name:        "Home",
		Description: "Home network",
		DetectionRules: []DetectionRule{
			WiFiRule{SSID: "TestNetwork"},
		},
	}

	manager := NewManager([]Detector{detector}, []Zone{zone})

	// Register event callback
	var mu sync.Mutex
	eventCount := 0

	manager.OnLocationChange(func(event LocationChangeEvent) {
		mu.Lock()
		defer mu.Unlock()
		eventCount++
	})

	// Trigger detection twice
	manager.detectAndUpdate()
	time.Sleep(100 * time.Millisecond)

	manager.detectAndUpdate()
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if eventCount != 1 {
		t.Errorf("Expected 1 event, got %d events", eventCount)
	}
}

// TestManualLocationOverride tests manual location override
func TestManualLocationOverride(t *testing.T) {
	detector := &MockDetector{
		name:        "mock-wifi",
		returnValue: "TestNetwork",
		returnError: nil,
	}

	manager := NewManager([]Detector{detector}, []Zone{})

	// Set manual location
	manager.SetManualLocation("Manual Location")

	location := manager.GetCurrentLocation()
	if location != "Manual Location" {
		t.Errorf("Expected current location 'Manual Location', got '%s'", location)
	}

	// Clear manual location
	manager.ClearManualLocation()

	// Should fall back to detector
	time.Sleep(100 * time.Millisecond)
	location = manager.GetCurrentLocation()
	// Will be "Unknown" since no zones match
	if location != "Unknown" {
		t.Errorf("Expected current location 'Unknown' after clearing manual, got '%s'", location)
	}
}
