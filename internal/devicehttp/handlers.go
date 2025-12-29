package devicehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/device"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/types"
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

// OllamaStatus represents the status of Ollama installation
type OllamaStatus struct {
	Installed     bool     `json:"installed"`      // Ollama CLI is installed
	Running       bool     `json:"running"`        // Ollama server is running
	Models        []string `json:"models"`         // Available models
	Version       string   `json:"version"`        // Ollama version (if available)
	ServerAddress string   `json:"server_address"` // Ollama server address
}

// GetOllamaStatus checks if Ollama is installed and running
// GET /api/device/ollama
func (h *Handler) GetOllamaStatus(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}

	status := OllamaStatus{
		ServerAddress: "http://localhost:11434",
	}

	// Check if Ollama CLI is installed
	ollamaPath, err := exec.LookPath("ollama")
	if err == nil && ollamaPath != "" {
		status.Installed = true

		// Try to get version
		cmd := exec.Command("ollama", "--version")
		if output, err := cmd.Output(); err == nil {
			version := strings.TrimSpace(string(output))
			// Clean up version string (e.g., "ollama version 0.1.17" -> "0.1.17")
			version = strings.TrimPrefix(version, "ollama version ")
			status.Version = version
		}
	}

	// Check if Ollama server is running by hitting the API
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", status.ServerAddress+"/api/tags", nil)
	if err == nil {
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Do(req)
		if err == nil {
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode == http.StatusOK {
				status.Running = true

				// Parse models
				var tagsResp struct {
					Models []struct {
						Name string `json:"name"`
					} `json:"models"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err == nil {
					status.Models = make([]string, 0, len(tagsResp.Models))
					for _, model := range tagsResp.Models {
						status.Models = append(status.Models, model.Name)
					}
				}
			}
		}
	}

	orihttp.WriteJSON(w, status)
}

// DeviceCapabilities represents the device hardware capabilities response
type DeviceCapabilities struct {
	GPU               *types.GPUInfo `json:"gpu,omitempty"`
	TotalRAMBytes     int64          `json:"total_ram_bytes"`
	TotalRAMFormatted string         `json:"total_ram_formatted"`
	MaxModelParams    string         `json:"max_model_params"`
	MemoryTier        string         `json:"memory_tier"`
	TierDescription   string         `json:"tier_description"`
	RecommendedModels []string       `json:"recommended_models"`
	OllamaLibraryURL  string         `json:"ollama_library_url"`
}

// GetCapabilities returns device hardware capabilities including GPU, RAM, and model recommendations
// GET /api/device/capabilities
func (h *Handler) GetCapabilities(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}

	// Get device info (triggers detection if needed)
	deviceInfo := h.onboardingManager.GetDeviceInfo()
	if !deviceInfo.Detected {
		if err := h.onboardingManager.DetectAndStoreDevice(); err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to detect device", err)
			return
		}
		deviceInfo = h.onboardingManager.GetDeviceInfo()
	}

	orihttp.WriteJSON(w, h.buildCapabilities(deviceInfo))
}

// buildCapabilities creates a DeviceCapabilities response from DeviceInfo
func (h *Handler) buildCapabilities(deviceInfo types.DeviceInfo) DeviceCapabilities {
	caps := DeviceCapabilities{
		GPU:               deviceInfo.GPU,
		TotalRAMBytes:     deviceInfo.TotalRAMBytes,
		TotalRAMFormatted: device.FormatBytes(deviceInfo.TotalRAMBytes),
		MaxModelParams:    deviceInfo.MaxModelParams,
		MemoryTier:        deviceInfo.MemoryTier,
		OllamaLibraryURL:  "https://ollama.com/library",
	}

	if deviceInfo.MemoryTier != "" {
		tier := device.MemoryTier(deviceInfo.MemoryTier)
		caps.TierDescription = tier.Description()
		caps.RecommendedModels = tier.RecommendedModels()
	}

	return caps
}

// DetectHardware forces a re-detection of hardware capabilities
// POST /api/device/detect-hardware
func (h *Handler) DetectHardware(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}

	// Force re-detection
	if err := h.onboardingManager.RedetectDevice(); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to re-detect device", err)
		return
	}

	// Return updated capabilities
	deviceInfo := h.onboardingManager.GetDeviceInfo()
	orihttp.WriteJSON(w, h.buildCapabilities(deviceInfo))
}
