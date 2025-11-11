package location

// WiFiDetector extends Detector with WiFi-specific methods
type WiFiDetector interface {
	Detector
	// GetCurrentSSID returns the current WiFi SSID without zone matching
	GetCurrentSSID() (string, error)
}
