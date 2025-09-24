package version

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Build-time variables (set via -ldflags)
var (
	Version   = "dev"     // Version from VERSION file or build flag
	GitCommit = "unknown" // Git commit hash
	BuildDate = "unknown" // Build timestamp
)

// GetVersion reads the version from the VERSION file or returns build-time version
func GetVersion() string {
	// If version was set at build time, use it
	if Version != "dev" && Version != "" {
		return Version
	}

	// Try to read from VERSION file
	versionFile := "VERSION"

	// If we can't find VERSION in current directory, try relative to executable
	if _, err := os.Stat(versionFile); os.IsNotExist(err) {
		// Try to find VERSION file relative to the executable
		if execPath, err := os.Executable(); err == nil {
			execDir := filepath.Dir(execPath)
			versionFile = filepath.Join(execDir, "VERSION")
		}
	}

	if data, err := os.ReadFile(versionFile); err == nil {
		version := strings.TrimSpace(string(data))
		if version != "" {
			return version
		}
	}

	//fmt.Println("GetVersion Failed")
	// Fallback to hardcoded version if file doesn't exist or is empty
	return "dev"
}

// GetBuildInfo returns comprehensive build information
func GetBuildInfo() map[string]string {
	return map[string]string{
		"version":    GetVersion(),
		"git_commit": GitCommit,
		"build_date": BuildDate,
	}
}

// GetVersionString returns a formatted version string with build info
func GetVersionString() string {
	info := GetBuildInfo()
	if info["git_commit"] != "unknown" && info["build_date"] != "unknown" {
		return fmt.Sprintf("%s (commit: %s, built: %s)",
			info["version"], info["git_commit"], info["build_date"])
	}
	return info["version"]
}
