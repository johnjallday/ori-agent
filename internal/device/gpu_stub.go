//go:build !darwin && !linux && !windows

package device

import (
	"github.com/johnjallday/ori-agent/internal/types"
)

// detectGPUPlatform is a stub for unsupported platforms
func detectGPUPlatform() *types.GPUInfo {
	return nil
}

// detectTotalRAMPlatform is a stub for unsupported platforms
func detectTotalRAMPlatform() int64 {
	return 0
}

// detectHardwareInfoPlatform is a stub for unsupported platforms
func detectHardwareInfoPlatform() *HardwareInfo {
	return nil
}
