package device

import (
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/types"
)

// Byte size constants
const (
	KB = 1024
	MB = KB * 1024
	GB = MB * 1024
)

// DetectGPU detects GPU information for the current system.
// This function is implemented differently for each platform using build tags.
// Returns nil if GPU detection fails or is not supported on the platform.
func DetectGPU() *types.GPUInfo {
	return detectGPUPlatform()
}

// DetectTotalRAM returns the total system RAM in bytes.
// This function is implemented differently for each platform using build tags.
// Returns 0 if detection fails.
func DetectTotalRAM() int64 {
	return detectTotalRAMPlatform()
}

// DetectVendorFromName determines GPU vendor from the GPU name string.
// This is shared across all platforms to ensure consistent vendor detection.
func DetectVendorFromName(name string) string {
	nameLower := strings.ToLower(name)
	switch {
	case strings.Contains(nameLower, "apple"):
		return "Apple"
	case strings.Contains(nameLower, "nvidia") || strings.Contains(nameLower, "geforce") ||
		strings.Contains(nameLower, "quadro") || strings.Contains(nameLower, "rtx"):
		return "NVIDIA"
	case strings.Contains(nameLower, "amd") || strings.Contains(nameLower, "radeon") ||
		strings.Contains(nameLower, "ati"):
		return "AMD"
	case strings.Contains(nameLower, "intel"):
		return "Intel"
	default:
		return "Unknown"
	}
}

// NewAppleSiliconGPU creates a GPUInfo for Apple Silicon systems.
// Used as fallback when system_profiler fails.
func NewAppleSiliconGPU() *types.GPUInfo {
	return &types.GPUInfo{
		Name:           "Apple Silicon GPU",
		Vendor:         "Apple",
		IsAppleSilicon: true,
		IsDiscrete:     false,
	}
}

// ParseVRAM parses VRAM strings like "8 GB", "8192 MB" to bytes.
func ParseVRAM(vramStr string) int64 {
	vramStr = strings.TrimSpace(vramStr)
	parts := strings.Fields(vramStr)
	if len(parts) < 2 {
		return 0
	}

	value, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}

	unit := strings.ToUpper(parts[1])
	switch unit {
	case "GB":
		return int64(value * float64(GB))
	case "MB":
		return int64(value * float64(MB))
	case "KB":
		return int64(value * float64(KB))
	default:
		return int64(value)
	}
}
