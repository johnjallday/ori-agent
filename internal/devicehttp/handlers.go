package devicehttp

import (
	"net/http"
	"os/exec"
	"runtime"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/onboarding"
)

// Handler handles device-related HTTP requests
type Handler struct {
	onboardingManager *onboarding.Manager
}

// NewHandler creates a new device HTTP handler
func NewHandler(onboardingManager *onboarding.Manager) *Handler {
	return &Handler{
		onboardingManager: onboardingManager,
	}
}

// SetDeviceTypeRequest represents the request body for setting device type
type SetDeviceTypeRequest struct {
	DeviceType string `json:"device_type"`
}

// GetDeviceInfo returns device information
// GET /api/device/info
func (h *Handler) GetDeviceInfo(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}

	// Get device info from onboarding manager
	deviceInfo := h.onboardingManager.GetDeviceInfo()

	// If device hasn't been detected yet, detect it now
	if !deviceInfo.Detected {
		if err := h.onboardingManager.DetectAndStoreDevice(); err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to detect device", err)
			return
		}
		// Get the updated device info
		deviceInfo = h.onboardingManager.GetDeviceInfo()
	}

	orihttp.WriteJSON(w, deviceInfo)
}

// SetDeviceType allows user to manually set/override the device type
// POST /api/device/type
func (h *Handler) SetDeviceType(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}

	var req SetDeviceTypeRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	// Update device type via onboarding manager
	if err := h.onboardingManager.SetDeviceType(req.DeviceType); err != nil {
		if err == onboarding.ErrInvalidDeviceType {
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "Invalid device type", err)
			return
		}
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update device type", err)
		return
	}

	// Return updated device info
	deviceInfo := h.onboardingManager.GetDeviceInfo()
	orihttp.WriteJSON(w, deviceInfo)
}

// WiFiInfo represents the current WiFi connection information
type WiFiInfo struct {
	SSID string `json:"ssid"`
}

// GetCurrentWiFi returns the current WiFi SSID
// GET /api/device/wifi/current
func (h *Handler) GetCurrentWiFi(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}

	ssid := detectCurrentWiFiSSID()
	orihttp.WriteJSON(w, WiFiInfo{SSID: ssid})
}

// detectCurrentWiFiSSID detects the current WiFi SSID based on the operating system
func detectCurrentWiFiSSID() string {
	switch runtime.GOOS {
	case "darwin":
		return detectMacOSWiFiSSID()
	case "linux":
		// Future: implement Linux detection
		return ""
	case "windows":
		// Future: implement Windows detection
		return ""
	default:
		return ""
	}
}

// detectMacOSWiFiSSID detects WiFi SSID on macOS
func detectMacOSWiFiSSID() string {
	// Try primary method: networksetup
	interfaces := []string{"en0", "en1", "en2"}
	for _, iface := range interfaces {
		cmd := exec.Command("networksetup", "-getairportnetwork", iface)
		output, err := cmd.Output()
		if err == nil && len(output) > 0 {
			// Output format: "Current Wi-Fi Network: NetworkName"
			outputStr := string(output)
			if strings.Contains(outputStr, "Current Wi-Fi Network:") {
				parts := strings.SplitN(outputStr, ":", 2)
				if len(parts) == 2 {
					ssid := strings.TrimSpace(parts[1])
					if ssid != "" {
						return ssid
					}
				}
			}
		}
	}

	// Fallback method: airport command
	cmd := exec.Command("/System/Library/PrivateFrameworks/Apple80211.framework/Versions/Current/Resources/airport", "-I")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	// Parse output for SSID line
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, " SSID:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				ssid := strings.TrimSpace(parts[1])
				if ssid != "" {
					return ssid
				}
			}
		}
	}

	return ""
}
