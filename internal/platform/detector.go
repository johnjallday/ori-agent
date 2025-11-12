package platform

import (
	"fmt"
	"runtime"
)

// DetectPlatform returns the current platform as a string in the format "OS-ARCH"
// (e.g., "darwin-arm64", "linux-amd64", "windows-amd64")
func DetectPlatform() string {
	return fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
}

// DetectOS returns the current operating system
// (e.g., "darwin", "linux", "windows", "freebsd")
func DetectOS() string {
	return runtime.GOOS
}

// DetectArch returns the current architecture
// (e.g., "amd64", "arm64", "386", "arm")
func DetectArch() string {
	return runtime.GOARCH
}

// GetPlatformDisplayName returns a user-friendly display name for a given platform string
func GetPlatformDisplayName(platform string) string {
	displayNames := map[string]string{
		// macOS
		"darwin-amd64": "macOS (Intel)",
		"darwin-arm64": "macOS (Apple Silicon)",

		// Linux
		"linux-amd64": "Linux (64-bit)",
		"linux-arm64": "Linux (ARM64)",
		"linux-386":   "Linux (32-bit)",
		"linux-arm":   "Linux (ARM)",

		// Windows
		"windows-amd64": "Windows (64-bit)",
		"windows-arm64": "Windows (ARM64)",
		"windows-386":   "Windows (32-bit)",

		// FreeBSD
		"freebsd-amd64": "FreeBSD (64-bit)",
		"freebsd-386":   "FreeBSD (32-bit)",
		"freebsd-arm":   "FreeBSD (ARM)",
	}

	if displayName, ok := displayNames[platform]; ok {
		return displayName
	}

	// Fallback: return the platform string as-is
	return platform
}

// GetOSDisplayName returns a user-friendly display name for an operating system
func GetOSDisplayName(os string) string {
	displayNames := map[string]string{
		"darwin":  "macOS",
		"linux":   "Linux",
		"windows": "Windows",
		"freebsd": "FreeBSD",
	}

	if displayName, ok := displayNames[os]; ok {
		return displayName
	}

	return os
}

// GetArchDisplayName returns a user-friendly display name for an architecture
func GetArchDisplayName(arch string) string {
	displayNames := map[string]string{
		"amd64": "64-bit (Intel/AMD)",
		"arm64": "ARM64 (Apple Silicon, ARM servers)",
		"386":   "32-bit (x86)",
		"arm":   "ARM (32-bit)",
	}

	if displayName, ok := displayNames[arch]; ok {
		return displayName
	}

	return arch
}
