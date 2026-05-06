//go:build darwin

package detector

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DarwinDetector detects applications on macOS using Spotlight metadata.
type DarwinDetector struct {
	config Config
}

// NewDarwinDetector creates a new macOS app detector.
func NewDarwinDetector(cfg Config) *DarwinDetector {
	return &DarwinDetector{config: cfg}
}

// Platform returns "darwin".
func (d *DarwinDetector) Platform() string {
	return "darwin"
}

// DetectApps uses multiple methods to find recently used applications.
func (d *DarwinDetector) DetectApps(ctx context.Context) ([]DetectedApp, error) {
	appSet := make(map[string]DetectedApp)

	// Method 1: Get currently running apps (these are definitely "recent")
	runningApps := d.getRunningApps(ctx)
	for _, app := range runningApps {
		if !d.config.IncludeSystemApps && isSystemApp(app.Path) {
			continue
		}
		appSet[app.Path] = app
	}

	// Method 2: Get apps with kMDItemLastUsedDate within recency window
	recentApps := d.findRecentlyUsedApps(ctx)
	for _, app := range recentApps {
		if _, exists := appSet[app.Path]; !exists {
			if !d.config.IncludeSystemApps && isSystemApp(app.Path) {
				continue
			}
			appSet[app.Path] = app
		}
	}

	// Method 3: Get apps from /Applications that were modified recently
	modifiedApps := d.findRecentlyModifiedApps()
	for _, app := range modifiedApps {
		if _, exists := appSet[app.Path]; !exists {
			if !d.config.IncludeSystemApps && isSystemApp(app.Path) {
				continue
			}
			appSet[app.Path] = app
		}
	}

	// Convert map to slice
	var result []DetectedApp
	for _, app := range appSet {
		result = append(result, app)
	}

	return result, nil
}

// getRunningApps gets currently running applications using lsappinfo.
func (d *DarwinDetector) getRunningApps(ctx context.Context) []DetectedApp {
	cmd := exec.CommandContext(ctx, "lsappinfo", "list")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	var apps []DetectedApp
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		// Look for lines with "bundle path=" (note the space)
		if strings.Contains(line, "bundle path=") {
			// Extract the path between quotes
			start := strings.Index(line, `"`)
			if start == -1 {
				continue
			}
			end := strings.Index(line[start+1:], `"`)
			if end == -1 {
				continue
			}
			path := line[start+1 : start+1+end]

			if strings.HasSuffix(path, ".app") {
				name := extractAppName(path)
				apps = append(apps, DetectedApp{
					Name:     name,
					Path:     path,
					LastUsed: time.Now(), // Running now = recently used
				})
			}
		}
	}

	return apps
}

// findRecentlyUsedApps finds apps with kMDItemLastUsedDate set recently.
func (d *DarwinDetector) findRecentlyUsedApps(ctx context.Context) []DetectedApp {
	// Query Spotlight for apps used in the last N days
	cutoff := time.Now().AddDate(0, 0, -d.config.RecencyDays)
	dateStr := cutoff.Format("2006-01-02")

	query := `kMDItemContentType == "com.apple.application-bundle" && kMDItemLastUsedDate >= $time.iso(` + dateStr + `)`
	cmd := exec.CommandContext(ctx, "mdfind", query)
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	var apps []DetectedApp
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		path := strings.TrimSpace(scanner.Text())
		if path == "" {
			continue
		}

		name := extractAppName(path)
		lastUsed, iconPath := d.getAppMetadata(ctx, path)
		apps = append(apps, DetectedApp{
			Name:     name,
			Path:     path,
			LastUsed: lastUsed,
			IconPath: iconPath,
		})
	}

	return apps
}

// findRecentlyModifiedApps finds apps in /Applications modified recently.
func (d *DarwinDetector) findRecentlyModifiedApps() []DetectedApp {
	var apps []DetectedApp
	cutoff := time.Now().AddDate(0, 0, -d.config.RecencyDays)

	// Check common app locations
	locations := []string{
		"/Applications",
		filepath.Join(os.Getenv("HOME"), "Applications"),
	}

	for _, loc := range locations {
		entries, err := os.ReadDir(loc)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".app") {
				continue
			}

			path := filepath.Join(loc, entry.Name())
			info, err := entry.Info()
			if err != nil {
				continue
			}

			// Check if modified within recency window
			if info.ModTime().After(cutoff) {
				name := extractAppName(path)
				apps = append(apps, DetectedApp{
					Name:     name,
					Path:     path,
					LastUsed: info.ModTime(),
				})
			}
		}
	}

	return apps
}

// getAppMetadata retrieves last used date and icon path for an app.
func (d *DarwinDetector) getAppMetadata(ctx context.Context, appPath string) (time.Time, string) {
	// Use mdls to get metadata
	cmd := exec.CommandContext(ctx, "mdls",
		"-name", "kMDItemLastUsedDate",
		"-name", "kMDItemDisplayName",
		"-raw",
		appPath,
	)
	output, err := cmd.Output()
	if err != nil {
		return time.Time{}, ""
	}

	lastUsed := parseSpotlightDate(string(output))
	iconPath := filepath.Join(appPath, "Contents", "Resources", "AppIcon.icns")

	return lastUsed, iconPath
}

// parseSpotlightDate parses the date format from mdls output.
// Format: 2024-01-15 10:30:45 +0000
func parseSpotlightDate(output string) time.Time {
	// mdls -raw outputs values separated by null bytes
	parts := strings.Split(output, "\x00")
	if len(parts) == 0 {
		return time.Time{}
	}

	dateStr := strings.TrimSpace(parts[0])
	if dateStr == "(null)" || dateStr == "" {
		return time.Time{}
	}

	// Try parsing the Spotlight date format
	formats := []string{
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05 +0000",
		time.RFC3339,
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t
		}
	}

	return time.Time{}
}

// isSystemApp checks if an app path is a system application.
func isSystemApp(path string) bool {
	systemPaths := []string{
		"/System/",
		"/Library/Apple/",
		"/System/Library/",
	}
	for _, prefix := range systemPaths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// extractAppName extracts the application name from a .app bundle path.
func extractAppName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, ".app")
}
