//go:build linux

package location

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// LinuxWiFiDetector implements WiFiDetector for Linux
type LinuxWiFiDetector struct{}

// NewWiFiDetector creates a new WiFi detector for the current platform
func NewWiFiDetector() WiFiDetector {
	return &LinuxWiFiDetector{}
}

// Name returns the detector name
func (d *LinuxWiFiDetector) Name() string {
	return "wifi-linux"
}

// Detect returns the current WiFi SSID on Linux
func (d *LinuxWiFiDetector) Detect(ctx context.Context) (string, error) {
	return d.GetCurrentSSID()
}

// GetCurrentSSID returns the current WiFi SSID on Linux using iwgetid
func (d *LinuxWiFiDetector) GetCurrentSSID() (string, error) {
	// Try iwgetid first (most common)
	cmd := exec.Command("iwgetid", "-r")
	output, err := cmd.Output()
	if err == nil {
		ssid := strings.TrimSpace(string(output))
		if ssid != "" {
			return ssid, nil
		}
		return "", errors.New("not connected to WiFi network")
	}

	// Fallback: try nmcli (NetworkManager)
	cmd = exec.Command("nmcli", "-t", "-f", "active,ssid", "dev", "wifi")
	output, err = cmd.Output()
	if err != nil {
		return "", errors.New("failed to detect WiFi: iwgetid and nmcli not available")
	}

	// Parse nmcli output
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "yes:") {
			ssid := strings.TrimPrefix(line, "yes:")
			ssid = strings.TrimSpace(ssid)
			if ssid != "" {
				return ssid, nil
			}
		}
	}

	return "", errors.New("not connected to WiFi network")
}
