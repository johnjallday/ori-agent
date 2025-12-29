//go:build windows

package detector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// WindowsDetector detects applications on Windows using Prefetch files.
type WindowsDetector struct {
	config Config
}

// NewWindowsDetector creates a new Windows app detector.
func NewWindowsDetector(cfg Config) *WindowsDetector {
	return &WindowsDetector{config: cfg}
}

// Platform returns "windows".
func (d *WindowsDetector) Platform() string {
	return "windows"
}

// DetectApps scans Prefetch files to find recently used applications.
func (d *WindowsDetector) DetectApps(ctx context.Context) ([]DetectedApp, error) {
	prefetchDir := `C:\Windows\Prefetch`

	entries, err := os.ReadDir(prefetchDir)
	if err != nil {
		// Prefetch may not be accessible without admin rights
		// Fall back to scanning common app directories
		return d.scanCommonPaths(ctx)
	}

	var apps []DetectedApp
	seen := make(map[string]bool)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".pf") {
			continue
		}

		// Extract app name from prefetch filename
		// Format: APPNAME-HASH.pf
		appName := extractPrefetchAppName(name)
		if appName == "" || seen[appName] {
			continue
		}

		// Skip system executables
		if !d.config.IncludeSystemApps && isWindowsSystemApp(appName) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		lastUsed := info.ModTime()
		if !IsRecentlyUsed(lastUsed, d.config.RecencyDays) {
			continue
		}

		seen[appName] = true
		apps = append(apps, DetectedApp{
			Name:     formatWindowsAppName(appName),
			Path:     filepath.Join(prefetchDir, name),
			LastUsed: lastUsed,
		})
	}

	return apps, nil
}

// scanCommonPaths scans common installation directories as a fallback.
func (d *WindowsDetector) scanCommonPaths(ctx context.Context) ([]DetectedApp, error) {
	commonPaths := []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("LOCALAPPDATA"),
		filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local", "Programs"),
	}

	var apps []DetectedApp
	seen := make(map[string]bool)

	for _, basePath := range commonPaths {
		if basePath == "" {
			continue
		}

		entries, err := os.ReadDir(basePath)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			appName := entry.Name()
			if seen[appName] {
				continue
			}

			// Look for executable
			appPath := filepath.Join(basePath, appName)
			exePath := findWindowsExecutable(appPath)
			if exePath == "" {
				continue
			}

			info, err := os.Stat(exePath)
			if err != nil {
				continue
			}

			lastUsed := info.ModTime()
			if !IsRecentlyUsed(lastUsed, d.config.RecencyDays) {
				continue
			}

			seen[appName] = true
			apps = append(apps, DetectedApp{
				Name:     appName,
				Path:     exePath,
				LastUsed: lastUsed,
			})
		}
	}

	return apps, nil
}

// extractPrefetchAppName extracts the application name from a prefetch filename.
func extractPrefetchAppName(filename string) string {
	// Remove .pf extension
	name := strings.TrimSuffix(filename, ".pf")
	name = strings.TrimSuffix(name, ".PF")

	// Remove hash suffix (last segment after -)
	if idx := strings.LastIndex(name, "-"); idx > 0 {
		name = name[:idx]
	}

	// Remove .EXE if present
	name = strings.TrimSuffix(name, ".EXE")
	name = strings.TrimSuffix(name, ".exe")

	return name
}

// formatWindowsAppName formats a Windows executable name to a display name.
func formatWindowsAppName(name string) string {
	// Map known executables to friendly names
	knownApps := map[string]string{
		"CODE":            "Visual Studio Code",
		"DEVENV":          "Visual Studio",
		"DOCKER DESKTOP":  "Docker Desktop",
		"SLACK":           "Slack",
		"DISCORD":         "Discord",
		"CHROME":          "Google Chrome",
		"FIREFOX":         "Firefox",
		"MSEDGE":          "Microsoft Edge",
		"WINWORD":         "Microsoft Word",
		"EXCEL":           "Microsoft Excel",
		"POWERPNT":        "Microsoft PowerPoint",
		"OUTLOOK":         "Microsoft Outlook",
		"TEAMS":           "Microsoft Teams",
		"WINDOWSTERMINAL": "Windows Terminal",
		"POSTMAN":         "Postman",
		"FIGMA":           "Figma",
		"NOTION":          "Notion",
		"OBSIDIAN":        "Obsidian",
	}

	upper := strings.ToUpper(name)
	if friendly, ok := knownApps[upper]; ok {
		return friendly
	}

	// Title case the name
	return strings.Title(strings.ToLower(name))
}

// isWindowsSystemApp checks if an app is a Windows system application.
func isWindowsSystemApp(name string) bool {
	systemApps := []string{
		"SVCHOST", "CSRSS", "LSASS", "SERVICES", "WINLOGON",
		"DWDM", "SMSS", "WININIT", "SPOOLSV", "SEARCHINDEXER",
		"DLLHOST", "TASKHOST", "CONHOST", "RUNDLL32", "MSIEXEC",
		"WUAUCLT", "TRUSTEDINSTALLER", "AUDIODG", "FONTDRVHOST",
	}

	upper := strings.ToUpper(name)
	for _, sys := range systemApps {
		if upper == sys {
			return true
		}
	}
	return false
}

// findWindowsExecutable looks for a main executable in an app directory.
func findWindowsExecutable(appDir string) string {
	entries, err := os.ReadDir(appDir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(strings.ToLower(name), ".exe") {
			return filepath.Join(appDir, name)
		}
	}

	return ""
}
