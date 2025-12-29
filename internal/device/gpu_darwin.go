//go:build darwin

package device

import (
	"encoding/json"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/johnjallday/ori-agent/internal/types"
)

// sysctl constants for macOS
const (
	ctlHW     = 6  // sysctl CTL_HW
	hwMemsize = 24 // sysctl HW_MEMSIZE (macOS)
)

// systemProfilerDisplayData represents the JSON structure from system_profiler SPDisplaysDataType
type systemProfilerDisplayData struct {
	SPDisplaysDataType []displayController `json:"SPDisplaysDataType"`
}

type displayController struct {
	SPPCIDevice      string    `json:"sppci_device,omitempty"`
	SPDisplaysVendor string    `json:"sppci_vendor,omitempty"`
	ChipsetModel     string    `json:"sppci_model,omitempty"`
	DeviceType       string    `json:"spdisplays_device-type,omitempty"`
	VRAM             string    `json:"spdisplays_vram,omitempty"`
	VRAMShared       string    `json:"spdisplays_vram_shared,omitempty"`
	VendorID         string    `json:"spdisplays_vendor,omitempty"`
	DeviceID         string    `json:"sppci_device_type,omitempty"`
	BusType          string    `json:"sppci_bus,omitempty"`
	Displays         []display `json:"spdisplays_ndrvs,omitempty"`
	Name             string    `json:"_name,omitempty"`
	GPUCoreCount     int       `json:"gpu_core_count,omitempty"`
	MetalFamily      string    `json:"sppci_metal_family,omitempty"`
}

type display struct {
	Name       string `json:"_name,omitempty"`
	Resolution string `json:"_spdisplays_resolution,omitempty"`
}

// detectGPUPlatform implements GPU detection for macOS
func detectGPUPlatform() *types.GPUInfo {
	isAppleSilicon := runtime.GOARCH == "arm64"

	// Run system_profiler to get GPU information
	cmd := exec.Command("system_profiler", "SPDisplaysDataType", "-json")
	output, err := cmd.Output()
	if err != nil {
		if isAppleSilicon {
			return NewAppleSiliconGPU()
		}
		return nil
	}

	var data systemProfilerDisplayData
	if err := json.Unmarshal(output, &data); err != nil {
		if isAppleSilicon {
			return NewAppleSiliconGPU()
		}
		return nil
	}

	if len(data.SPDisplaysDataType) == 0 {
		if isAppleSilicon {
			return NewAppleSiliconGPU()
		}
		return nil
	}

	// Get the primary GPU (first one in list)
	gpu := data.SPDisplaysDataType[0]

	info := &types.GPUInfo{
		Name:           gpu.Name,
		IsAppleSilicon: isAppleSilicon,
	}

	// Determine vendor - try name first, then vendor field, then fallback
	info.Vendor = detectVendorFromGPU(gpu)

	// Parse VRAM
	if gpu.VRAM != "" {
		info.VRAM = ParseVRAM(gpu.VRAM)
		info.IsDiscrete = true
	} else if gpu.VRAMShared != "" {
		info.VRAM = ParseVRAM(gpu.VRAMShared)
		info.IsDiscrete = false
	}

	// Apple Silicon uses unified memory, not discrete
	if isAppleSilicon {
		info.IsDiscrete = false
	}

	return info
}

// detectVendorFromGPU determines vendor from macOS display controller info
func detectVendorFromGPU(gpu displayController) string {
	// Try name-based detection first (uses shared function)
	if vendor := DetectVendorFromName(gpu.Name); vendor != "Unknown" {
		return vendor
	}

	// Check vendor field as fallback
	vendorField := strings.ToLower(gpu.SPDisplaysVendor)
	switch {
	case strings.Contains(vendorField, "apple"):
		return "Apple"
	case strings.Contains(vendorField, "nvidia"):
		return "NVIDIA"
	case strings.Contains(vendorField, "amd") || strings.Contains(vendorField, "ati"):
		return "AMD"
	case strings.Contains(vendorField, "intel"):
		return "Intel"
	}

	// Final fallback: check if it's arm64 (Apple Silicon)
	if runtime.GOARCH == "arm64" {
		return "Apple"
	}

	return "Unknown"
}

// ParseVRAM parses VRAM strings like "8 GB", "8192 MB" to bytes.
// Exported for testing.
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

// detectTotalRAMPlatform detects total system RAM on macOS
func detectTotalRAMPlatform() int64 {
	// Use sysctl to get hw.memsize
	cmd := exec.Command("sysctl", "-n", "hw.memsize")
	output, err := cmd.Output()
	if err != nil {
		// Fallback: try syscall approach
		return detectRAMSysctl()
	}

	memStr := strings.TrimSpace(string(output))
	memsize, err := strconv.ParseUint(memStr, 10, 64)
	if err != nil {
		return detectRAMSysctl()
	}

	return int64(memsize)
}

// detectRAMSysctl uses the syscall package to get memory size
func detectRAMSysctl() int64 {
	var memsize uint64
	size := uintptr(8)

	mib := []int32{ctlHW, hwMemsize}
	if _, _, errno := syscall.Syscall6(
		syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		uintptr(len(mib)),
		uintptr(unsafe.Pointer(&memsize)),
		uintptr(unsafe.Pointer(&size)),
		0,
		0,
	); errno != 0 {
		return 0
	}

	return int64(memsize)
}
