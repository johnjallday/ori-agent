package location

import "context"

// Detector defines the interface for location detection implementations
// Detectors are responsible for determining the current location using
// a specific method (WiFi, IP, GPS, etc.)
type Detector interface {
	// Name returns a human-readable name for this detector
	Name() string

	// Detect attempts to detect the current location and returns a string value
	// that can be matched against zone detection rules.
	// For WiFi detectors, this returns the current SSID.
	// For IP detectors, this returns the IP address or geolocation city.
	// For GPS detectors, this returns coordinates as a string.
	// Returns an error if detection fails (e.g., WiFi disabled, no connection).
	Detect(ctx context.Context) (string, error)
}
