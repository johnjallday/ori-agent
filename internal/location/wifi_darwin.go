//go:build darwin

package location

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// DarwinWiFiDetector implements WiFiDetector for macOS
type DarwinWiFiDetector struct{}

// NewWiFiDetector creates a new WiFi detector for the current platform
func NewWiFiDetector() WiFiDetector {
	return &DarwinWiFiDetector{}
}

// Name returns the detector name
func (d *DarwinWiFiDetector) Name() string {
	return "wifi-darwin"
}

// Detect returns the current WiFi SSID on macOS
func (d *DarwinWiFiDetector) Detect(ctx context.Context) (string, error) {
	return d.GetCurrentSSID()
}

// GetCurrentSSID returns the current WiFi SSID on macOS using airport command
func (d *DarwinWiFiDetector) GetCurrentSSID() (string, error) {
	// Use airport command to get current WiFi info
	// Path: /System/Library/PrivateFrameworks/Apple80211.framework/Versions/Current/Resources/airport
	cmd := exec.Command("/System/Library/PrivateFrameworks/Apple80211.framework/Versions/Current/Resources/airport", "-I")
	output, err := cmd.Output()
	if err != nil {
		return "", errors.New("failed to execute airport command: WiFi may be disabled")
	}

	// Parse output to find SSID
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SSID:") {
			ssid := strings.TrimSpace(strings.TrimPrefix(line, "SSID:"))
			if ssid == "" {
				return "", errors.New("not connected to WiFi network")
			}
			return ssid, nil
		}
	}

	return "", errors.New("could not find SSID in airport output")
}
