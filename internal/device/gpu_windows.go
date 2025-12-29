//go:build windows

package device

import (
	"os/exec"
	"strconv"
	"strings"
	"unsafe"

	"github.com/johnjallday/ori-agent/internal/types"
	"golang.org/x/sys/windows"
)

// detectGPUPlatform implements GPU detection for Windows
func detectGPUPlatform() *types.GPUInfo {
	// Use wmic to get GPU information
	cmd := exec.Command("wmic", "path", "win32_VideoController", "get", "Name,AdapterRAM", "/format:csv")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 2 {
		return nil
	}

	// Skip header line, parse first GPU
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// CSV format: Node,AdapterRAM,Name
		parts := strings.Split(line, ",")
		if len(parts) < 3 {
			continue
		}

		vramBytes, _ := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		name := strings.TrimSpace(parts[2])

		if name == "" {
			continue
		}

		info := &types.GPUInfo{
			Name:       name,
			VRAM:       vramBytes,
			IsDiscrete: vramBytes > 0,
			Vendor:     DetectVendorFromName(name),
		}

		// Intel GPUs are typically integrated
		if info.Vendor == "Intel" {
			info.IsDiscrete = false
		}

		return info
	}

	return nil
}

// detectTotalRAMPlatform detects total system RAM on Windows
func detectTotalRAMPlatform() int64 {
	var memStatus memoryStatusEx
	memStatus.Length = uint32(unsafe.Sizeof(memStatus))

	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	globalMemoryStatusEx := kernel32.NewProc("GlobalMemoryStatusEx")

	ret, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&memStatus)))
	if ret == 0 {
		return 0
	}

	return int64(memStatus.TotalPhys)
}

// memoryStatusEx matches Windows MEMORYSTATUSEX structure
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}
