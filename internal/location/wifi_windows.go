//go:build windows

package location

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// WindowsWiFiDetector implements WiFiDetector for Windows
type WindowsWiFiDetector struct{}

// NewWiFiDetector creates a new WiFi detector for the current platform
func NewWiFiDetector() WiFiDetector {
	return &WindowsWiFiDetector{}
}

// Name returns the detector name
func (d *WindowsWiFiDetector) Name() string {
	return "wifi-windows"
}

// Detect returns the current WiFi SSID on Windows
func (d *WindowsWiFiDetector) Detect(ctx context.Context) (string, error) {
	return d.GetCurrentSSID()
}

// GetCurrentSSID returns the current WiFi SSID on Windows using netsh
func (d *WindowsWiFiDetector) GetCurrentSSID() (string, error) {
	cmd := exec.Command("netsh", "wlan", "show", "interfaces")
	output, err := cmd.Output()
	if err != nil {
		return "", errors.New("failed to execute netsh command: WiFi may be disabled")
	}

	// Parse output to find SSID
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SSID") && strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				ssid := strings.TrimSpace(parts[1])
				if ssid != "" {
					return ssid, nil
				}
			}
		}
	}

	return "", errors.New("not connected to WiFi network")
}
