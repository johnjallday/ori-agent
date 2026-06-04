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

// detectHardwareInfoPlatform detects machine and chip information on Linux.
// MachineName is read from the DMI product name exposed by the kernel and
// ChipType from the CPU model in /proc/cpuinfo. Returns nil when neither can
// be determined.
func detectHardwareInfoPlatform() *HardwareInfo {
	info := &HardwareInfo{
		MachineName: readDMIProductName(),
		ChipType:    readCPUModelName(),
	}
	if info.MachineName == "" && info.ChipType == "" {
		return nil
	}
	return info
}

// readDMIProductName reads the system product name from the kernel DMI
// subsystem (e.g. "XPS 15 9520"). Returns an empty string when the value is
// unavailable or is a common OEM placeholder.
func readDMIProductName() string {
	data, err := os.ReadFile("/sys/class/dmi/id/product_name")
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(data))
	switch strings.ToLower(name) {
	case "", "to be filled by o.e.m.", "system product name", "default string":
		return ""
	}
	return name
}

// readCPUModelName extracts the CPU model from /proc/cpuinfo. On x86 this is the
// "model name" field; on ARM it falls back to the "Hardware" or "Model" field.
func readCPUModelName() string {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()

	var fallback string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "model name":
			return value
		case "hardware", "model":
			if fallback == "" {
				fallback = value
			}
		}
	}
	return fallback
}
