package location

import (
	"strings"
	"time"
)

// DetectionMethod represents the method used to detect location
type DetectionMethod string

const (
	DetectionMethodWiFi   DetectionMethod = "wifi"
	DetectionMethodIP     DetectionMethod = "ip"
	DetectionMethodGPS    DetectionMethod = "gps"
	DetectionMethodManual DetectionMethod = "manual"
)

// Zone represents a named location with detection rules
type Zone struct {
	// ID is the unique identifier for this zone
	ID string `json:"id"`
	// Name is the human-readable name (e.g., "Home", "Office")
	Name string `json:"name"`
	// Description provides additional context about this zone
	Description string `json:"description"`
	// DetectionRules are the rules used to match this zone
	DetectionRules []DetectionRule `json:"detection_rules"`
}

// DetectionRule defines the interface for zone matching logic
type DetectionRule interface {
	// Method returns the detection method this rule applies to
	Method() DetectionMethod
	// Matches returns true if the detected value matches this rule
	Matches(detectedValue string) bool
}

// WiFiRule implements DetectionRule for WiFi SSID matching
type WiFiRule struct {
	// SSID is the WiFi network name to match
	// Supports exact match or wildcard patterns (e.g., "MyNetwork*")
	SSID string `json:"ssid"`
}

// Method returns the detection method for WiFi rules
func (r WiFiRule) Method() DetectionMethod {
	return DetectionMethodWiFi
}

// Matches checks if the detected SSID matches this rule
// Supports exact match and wildcard patterns (e.g., "HomeNetwork*")
func (r WiFiRule) Matches(detectedValue string) bool {
	// Exact match
	if r.SSID == detectedValue {
		return true
	}

	// Wildcard match
	if strings.HasSuffix(r.SSID, "*") {
		prefix := strings.TrimSuffix(r.SSID, "*")
		return strings.HasPrefix(detectedValue, prefix)
	}

	return false
}

// LocationChangeEvent represents a location change event for the event bus
type LocationChangeEvent struct {
	// PreviousLocation is the previous location zone name (empty if first detection)
	PreviousLocation string
	// CurrentLocation is the new location zone name
	CurrentLocation string
	// Timestamp is when the location change was detected
	Timestamp time.Time
	// DetectionMethod is the method used to detect the new location
	DetectionMethod DetectionMethod
}
