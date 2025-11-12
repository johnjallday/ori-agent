package platform

import (
	"runtime"
	"strings"
	"testing"
)

func TestDetectPlatform(t *testing.T) {
	platform := DetectPlatform()

	// Verify format is "os-arch"
	parts := strings.Split(platform, "-")
	if len(parts) != 2 {
		t.Errorf("Expected platform format 'os-arch', got: %s", platform)
	}

	// Verify it matches runtime values
	expectedOS := runtime.GOOS
	expectedArch := runtime.GOARCH
	expected := expectedOS + "-" + expectedArch

	if platform != expected {
		t.Errorf("Expected platform %s, got %s", expected, platform)
	}
}

func TestDetectOS(t *testing.T) {
	os := DetectOS()

	// Verify returns a valid OS
	validOS := []string{"darwin", "linux", "windows", "freebsd", "openbsd", "netbsd"}
	isValid := false
	for _, valid := range validOS {
		if os == valid {
			isValid = true
			break
		}
	}

	if !isValid {
		t.Logf("Detected OS: %s (not in common list, but may be valid)", os)
	}

	// Verify it matches runtime
	if os != runtime.GOOS {
		t.Errorf("Expected OS %s, got %s", runtime.GOOS, os)
	}
}

func TestDetectArch(t *testing.T) {
	arch := DetectArch()

	// Verify returns a valid architecture
	validArchs := []string{"amd64", "arm64", "386", "arm", "ppc64", "ppc64le", "mips", "mipsle"}
	isValid := false
	for _, valid := range validArchs {
		if arch == valid {
			isValid = true
			break
		}
	}

	if !isValid {
		t.Logf("Detected arch: %s (not in common list, but may be valid)", arch)
	}

	// Verify it matches runtime
	if arch != runtime.GOARCH {
		t.Errorf("Expected arch %s, got %s", runtime.GOARCH, arch)
	}
}

func TestGetPlatformDisplayName(t *testing.T) {
	tests := []struct {
		platform string
		expected string
	}{
		{"darwin-arm64", "macOS (Apple Silicon)"},
		{"darwin-amd64", "macOS (Intel)"},
		{"linux-amd64", "Linux (64-bit)"},
		{"linux-arm64", "Linux (ARM64)"},
		{"windows-amd64", "Windows (64-bit)"},
		{"windows-arm64", "Windows (ARM64)"},
		{"freebsd-amd64", "FreeBSD (64-bit)"},
		{"unknown-platform", "unknown-platform"}, // Fallback test
	}

	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			result := GetPlatformDisplayName(tt.platform)
			if result != tt.expected {
				t.Errorf("GetPlatformDisplayName(%s) = %s, expected %s", tt.platform, result, tt.expected)
			}
		})
	}
}

func TestGetOSDisplayName(t *testing.T) {
	tests := []struct {
		os       string
		expected string
	}{
		{"darwin", "macOS"},
		{"linux", "Linux"},
		{"windows", "Windows"},
		{"freebsd", "FreeBSD"},
		{"unknown", "unknown"}, // Fallback test
	}

	for _, tt := range tests {
		t.Run(tt.os, func(t *testing.T) {
			result := GetOSDisplayName(tt.os)
			if result != tt.expected {
				t.Errorf("GetOSDisplayName(%s) = %s, expected %s", tt.os, result, tt.expected)
			}
		})
	}
}

func TestGetArchDisplayName(t *testing.T) {
	tests := []struct {
		arch     string
		expected string
	}{
		{"amd64", "64-bit (Intel/AMD)"},
		{"arm64", "ARM64 (Apple Silicon, ARM servers)"},
		{"386", "32-bit (x86)"},
		{"arm", "ARM (32-bit)"},
		{"unknown", "unknown"}, // Fallback test
	}

	for _, tt := range tests {
		t.Run(tt.arch, func(t *testing.T) {
			result := GetArchDisplayName(tt.arch)
			if result != tt.expected {
				t.Errorf("GetArchDisplayName(%s) = %s, expected %s", tt.arch, result, tt.expected)
			}
		})
	}
}
