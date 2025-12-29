//go:build linux

package device

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/johnjallday/ori-agent/internal/types"
)

// detectGPUPlatform implements GPU detection for Linux
func detectGPUPlatform() *types.GPUInfo {
	// Try NVIDIA GPU detection first (most common for ML workloads)
	if info := detectNVIDIAGPU(); info != nil {
		return info
	}

	// Try AMD GPU detection
	if info := detectAMDGPU(); info != nil {
		return info
	}

	// No discrete GPU found
	return nil
}

// detectNVIDIAGPU uses nvidia-smi to detect NVIDIA GPUs
func detectNVIDIAGPU() *types.GPUInfo {
	cmd := exec.Command("nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return nil
	}

	// Parse first GPU
	parts := strings.Split(lines[0], ", ")
	if len(parts) < 2 {
		return nil
	}

	name := strings.TrimSpace(parts[0])
	vramMB, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		vramMB = 0
	}

	return &types.GPUInfo{
		Name:       name,
		Vendor:     "NVIDIA",
		VRAM:       vramMB * 1024 * 1024, // Convert MB to bytes
		IsDiscrete: true,
	}
}

// detectAMDGPU uses rocm-smi to detect AMD GPUs
func detectAMDGPU() *types.GPUInfo {
	// Try rocm-smi first
	cmd := exec.Command("rocm-smi", "--showproductname", "--showmeminfo", "vram")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	outputStr := string(output)

	// Parse GPU name
	var name string
	if idx := strings.Index(outputStr, "Card series:"); idx != -1 {
		line := outputStr[idx:]
		if endIdx := strings.Index(line, "\n"); endIdx != -1 {
			name = strings.TrimSpace(strings.TrimPrefix(line[:endIdx], "Card series:"))
		}
	}

	if name == "" {
		name = "AMD GPU"
	}

	// Parse VRAM (look for "VRAM Total Memory" line)
	var vram int64
	if idx := strings.Index(outputStr, "VRAM Total Memory"); idx != -1 {
		line := outputStr[idx:]
		if endIdx := strings.Index(line, "\n"); endIdx != -1 {
			// Extract numeric value
			valuePart := line[:endIdx]
			for _, field := range strings.Fields(valuePart) {
				if v, err := strconv.ParseInt(field, 10, 64); err == nil && v > 0 {
					vram = v * 1024 * 1024 // Assume MB
					break
				}
			}
		}
	}

	return &types.GPUInfo{
		Name:       name,
		Vendor:     "AMD",
		VRAM:       vram,
		IsDiscrete: true,
	}
}

// detectTotalRAMPlatform detects total system RAM on Linux
func detectTotalRAMPlatform() int64 {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				// Value is in kB
				kb, err := strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					return 0
				}
				return kb * 1024 // Convert to bytes
			}
		}
	}

	return 0
}
