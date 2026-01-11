// Package environ provides environment setup utilities for the application.
package environ

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandPath adds common development tool locations to the PATH environment variable.
// This ensures that tools like codex, go, node, etc. are available when the application
// is launched from a macOS app bundle (which has a minimal default PATH).
func ExpandPath() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = os.Getenv("HOME")
	}

	currentPath := os.Getenv("PATH")

	// Common locations for development tools (in priority order)
	additionalPaths := []string{
		"/opt/homebrew/bin",                  // Homebrew (Apple Silicon)
		"/opt/homebrew/sbin",                 // Homebrew sbin (Apple Silicon)
		"/usr/local/bin",                     // Homebrew (Intel) and other tools
		"/usr/local/sbin",                    // System tools
		filepath.Join(homeDir, ".local/bin"), // User local binaries
		filepath.Join(homeDir, "go/bin"),     // Go binaries
		filepath.Join(homeDir, ".cargo/bin"), // Rust/Cargo binaries
	}

	// Try to detect NVM Node.js installation
	nvmDir := filepath.Join(homeDir, ".nvm/versions/node")
	if entries, err := os.ReadDir(nvmDir); err == nil && len(entries) > 0 {
		// Use the latest Node.js version (entries are typically sorted)
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].IsDir() {
				nodeBinPath := filepath.Join(nvmDir, entries[i].Name(), "bin")
				if _, err := os.Stat(nodeBinPath); err == nil {
					additionalPaths = append(additionalPaths, nodeBinPath)
					break
				}
			}
		}
	}

	// Build new PATH by prepending additional paths that exist and aren't already in PATH
	var newPaths []string
	for _, p := range additionalPaths {
		// Check if path exists
		if _, err := os.Stat(p); err != nil {
			continue
		}
		// Check if already in PATH
		if strings.Contains(currentPath, p) {
			continue
		}
		newPaths = append(newPaths, p)
	}

	// Prepend new paths to existing PATH
	if len(newPaths) > 0 {
		expandedPath := strings.Join(newPaths, ":") + ":" + currentPath
		_ = os.Setenv("PATH", expandedPath)
	}
}
