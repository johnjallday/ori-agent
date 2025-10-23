package device

import (
	"os"
	"runtime"
	"strings"

	"github.com/johnjallday/dolphin-agent/internal/types"
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

	// Check for battery (indicates laptop)
	if hasBattery() {
		return "laptop"
	}

	// Default to desktop for systems with display
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
		// On macOS, check for IOKit battery information
		// For simplicity, we'll assume laptops for now
		// A more robust check would use IOKit APIs
		return true // Most macOS devices are laptops

	case "windows":
		// On Windows, could check SYSTEM_POWER_STATUS
		// For now, return false
		return false
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
