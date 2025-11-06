package device

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/johnjallday/ori-agent/internal/types"
)

// Detect automatically detects device information
func Detect() types.DeviceInfo {
	info := types.DeviceInfo{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Detected: true,
		UserSet:  false,
	}

	// Detect device type based on environment signals
	info.Type = detectDeviceType()

	return info
}

// detectDeviceType attempts to determine if this is a desktop, laptop, or server
func detectDeviceType() string {
	// Check for battery first (most reliable indicator of laptop)
	// Even headless or SSH-connected laptops are still laptops
	if hasBattery() {
		return "laptop"
	}

	// Check for common server indicators
	if isRunningAsService() {
		return "server"
	}

	// Check for headless environment (no display)
	if isHeadless() {
		return "server"
	}

	// Check for SSH connection
	if isSSH() {
		return "server"
	}

	// Default to desktop for systems with display and no battery
	return "desktop"
}

// isRunningAsService checks if the app is running as a system service
func isRunningAsService() bool {
	// Check for systemd on Linux
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/run/systemd/system"); err == nil {
			// Check if we're running under systemd
			if ppid := os.Getppid(); ppid == 1 {
				return true
			}
		}
	}

	// Check for launchd on macOS
	if runtime.GOOS == "darwin" {
		if os.Getenv("__CFBundleIdentifier") != "" {
			return true
		}
	}

	return false
}

// isHeadless checks if there's no display available
func isHeadless() bool {
	// Check DISPLAY environment variable on Unix-like systems
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		display := os.Getenv("DISPLAY")
		waylandDisplay := os.Getenv("WAYLAND_DISPLAY")
		if display == "" && waylandDisplay == "" {
			return true
		}
	}

	return false
}

// isSSH checks if connected via SSH
func isSSH() bool {
	sshClient := os.Getenv("SSH_CLIENT")
	sshConnection := os.Getenv("SSH_CONNECTION")
	sshTTY := os.Getenv("SSH_TTY")

	return sshClient != "" || sshConnection != "" || sshTTY != ""
}

// hasBattery attempts to detect if the system has a battery
func hasBattery() bool {
	switch runtime.GOOS {
	case "linux":
		// Check /sys/class/power_supply for battery
		entries, err := os.ReadDir("/sys/class/power_supply")
		if err != nil {
			return false
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "BAT") {
				return true
			}
		}

	case "darwin":
		// On macOS, use pmset to check for battery presence
		return hasMacOSBattery()

	case "windows":
		// On Windows, could check SYSTEM_POWER_STATUS
		// For now, return false
		return false
	}

	return false
}

// hasMacOSBattery checks for battery on macOS using pmset command
func hasMacOSBattery() bool {
	// Execute pmset -g batt to check for battery presence
	// Desktop Macs (Mac mini, Mac Studio, iMac, Mac Pro) will show "Now drawing from 'AC Power'"
	// MacBooks will show battery information like "InternalBattery"

	cmd := exec.Command("pmset", "-g", "batt")
	output, err := cmd.Output()
	if err != nil {
		// If pmset fails, fallback to model detection
		return hasMacOSBatteryByModel()
	}

	outputStr := string(output)

	// Check for battery indicators
	if strings.Contains(outputStr, "InternalBattery") ||
		strings.Contains(outputStr, "Battery") && !strings.Contains(outputStr, "No batteries") {
		return true
	}

	return false
}

// hasMacOSBatteryByModel uses system_profiler to determine if this is a MacBook
func hasMacOSBatteryByModel() bool {
	// Use system_profiler to get hardware model
	cmd := exec.Command("system_profiler", "SPHardwareDataType")
	output, err := cmd.Output()
	if err != nil {
		// Conservative default - let user correct in UI
		return false
	}

	outputStr := strings.ToLower(string(output))

	// Check for MacBook in the model name
	if strings.Contains(outputStr, "macbook") {
		return true
	}

	return false
}

// ValidateDeviceType checks if a device type string is valid
func ValidateDeviceType(deviceType string) bool {
	switch deviceType {
	case "desktop", "laptop", "server", "unknown":
		return true
	default:
		return false
	}
}
