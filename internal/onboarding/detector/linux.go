//go:build linux

package detector

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LinuxDetector detects applications on Linux using recently-used.xbel.
type LinuxDetector struct {
	config Config
}

// NewLinuxDetector creates a new Linux app detector.
func NewLinuxDetector(cfg Config) *LinuxDetector {
	return &LinuxDetector{config: cfg}
}

// Platform returns "linux".
func (d *LinuxDetector) Platform() string {
	return "linux"
}

// DetectApps scans the recently-used.xbel file and .desktop files.
func (d *LinuxDetector) DetectApps(ctx context.Context) ([]DetectedApp, error) {
	apps := make(map[string]DetectedApp)

	// Parse recently-used.xbel for usage data
	recentApps, err := d.parseRecentlyUsed(ctx)
	if err == nil {
		for _, app := range recentApps {
			if IsRecentlyUsed(app.LastUsed, d.config.RecencyDays) {
				apps[app.Name] = app
			}
		}
	}

	// Scan .desktop files for additional app info
	desktopApps, err := d.scanDesktopFiles(ctx)
	if err == nil {
		for _, app := range desktopApps {
			if _, exists := apps[app.Name]; !exists {
				// Check if app was recently accessed
				if IsRecentlyUsed(app.LastUsed, d.config.RecencyDays) {
					apps[app.Name] = app
				}
			}
		}
	}

	result := make([]DetectedApp, 0, len(apps))
	for _, app := range apps {
		result = append(result, app)
	}

	return result, nil
}

// xbelBookmark represents a bookmark in the recently-used.xbel file.
type xbelBookmark struct {
	Href     string `xml:"href,attr"`
	Modified string `xml:"modified,attr"`
	Visited  string `xml:"visited,attr"`
	Info     struct {
		Metadata struct {
			Applications struct {
				Application []struct {
					Name  string `xml:"name,attr"`
					Exec  string `xml:"exec,attr"`
					Count int    `xml:"count,attr"`
				} `xml:"application"`
			} `xml:"applications"`
		} `xml:"metadata"`
	} `xml:"info"`
}

// xbelFile represents the recently-used.xbel file structure.
type xbelFile struct {
	XMLName   xml.Name       `xml:"xbel"`
	Bookmarks []xbelBookmark `xml:"bookmark"`
}

// parseRecentlyUsed parses the ~/.local/share/recently-used.xbel file.
func (d *LinuxDetector) parseRecentlyUsed(ctx context.Context) ([]DetectedApp, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	xbelPath := filepath.Join(homeDir, ".local", "share", "recently-used.xbel")
	data, err := os.ReadFile(xbelPath)
	if err != nil {
		return nil, err
	}

	var xbel xbelFile
	if err := xml.Unmarshal(data, &xbel); err != nil {
		return nil, err
	}

	appUsage := make(map[string]time.Time)

	for _, bookmark := range xbel.Bookmarks {
		// Parse the modified/visited time
		var lastUsed time.Time
		if bookmark.Modified != "" {
			lastUsed, _ = time.Parse(time.RFC3339, bookmark.Modified)
		}
		if lastUsed.IsZero() && bookmark.Visited != "" {
			lastUsed, _ = time.Parse(time.RFC3339, bookmark.Visited)
		}

		// Get apps that opened this file
		for _, app := range bookmark.Info.Metadata.Applications.Application {
			appName := app.Name
			if appName == "" {
				continue
			}

			// Track the most recent usage per app
			if existing, ok := appUsage[appName]; !ok || lastUsed.After(existing) {
				appUsage[appName] = lastUsed
			}
		}
	}

	var apps []DetectedApp
	for name, lastUsed := range appUsage {
		apps = append(apps, DetectedApp{
			Name:     formatLinuxAppName(name),
			LastUsed: lastUsed,
		})
	}

	return apps, nil
}

// scanDesktopFiles scans .desktop files for installed applications.
func (d *LinuxDetector) scanDesktopFiles(ctx context.Context) ([]DetectedApp, error) {
	desktopDirs := []string{
		"/usr/share/applications",
		"/usr/local/share/applications",
	}

	homeDir, err := os.UserHomeDir()
	if err == nil {
		desktopDirs = append(desktopDirs,
			filepath.Join(homeDir, ".local", "share", "applications"),
		)
	}

	var apps []DetectedApp

	for _, dir := range desktopDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".desktop") {
				continue
			}

			desktopPath := filepath.Join(dir, entry.Name())
			app, err := parseDesktopFile(desktopPath)
			if err != nil || app.Name == "" {
				continue
			}

			// Skip hidden/system apps
			if !d.config.IncludeSystemApps && app.IconPath == "" {
				continue
			}

			apps = append(apps, app)
		}
	}

	return apps, nil
}

// parseDesktopFile parses a .desktop file to extract app info.
func parseDesktopFile(path string) (DetectedApp, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DetectedApp{}, err
	}

	var app DetectedApp
	lines := strings.Split(string(data), "\n")
	inDesktopEntry := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "[Desktop Entry]" {
			inDesktopEntry = true
			continue
		}
		if strings.HasPrefix(line, "[") {
			inDesktopEntry = false
			continue
		}

		if !inDesktopEntry {
			continue
		}

		if strings.HasPrefix(line, "Name=") {
			app.Name = strings.TrimPrefix(line, "Name=")
		} else if strings.HasPrefix(line, "Exec=") {
			exec := strings.TrimPrefix(line, "Exec=")
			// Extract the command (first word)
			parts := strings.Fields(exec)
			if len(parts) > 0 {
				app.Path = parts[0]
			}
		} else if strings.HasPrefix(line, "Icon=") {
			app.IconPath = strings.TrimPrefix(line, "Icon=")
		}
	}

	// Get last access time of the executable
	if app.Path != "" {
		if info, err := os.Stat(app.Path); err == nil {
			app.LastUsed = info.ModTime()
		}
	}

	return app, nil
}

// formatLinuxAppName formats a Linux app name for display.
func formatLinuxAppName(name string) string {
	// Map common executable names to friendly names
	knownApps := map[string]string{
		"code":               "Visual Studio Code",
		"code-oss":           "VS Code OSS",
		"gnome-terminal":     "Terminal",
		"konsole":            "Konsole",
		"firefox":            "Firefox",
		"google-chrome":      "Google Chrome",
		"chromium":           "Chromium",
		"slack":              "Slack",
		"discord":            "Discord",
		"postman":            "Postman",
		"docker":             "Docker",
		"gimp":               "GIMP",
		"inkscape":           "Inkscape",
		"libreoffice":        "LibreOffice",
		"nautilus":           "Files",
		"gedit":              "Text Editor",
		"vim":                "Vim",
		"nvim":               "Neovim",
		"emacs":              "Emacs",
		"sublime_text":       "Sublime Text",
		"jetbrains-idea":     "IntelliJ IDEA",
		"jetbrains-pycharm":  "PyCharm",
		"jetbrains-goland":   "GoLand",
		"jetbrains-webstorm": "WebStorm",
		"obsidian":           "Obsidian",
		"notion":             "Notion",
		"figma-linux":        "Figma",
	}

	lower := strings.ToLower(name)
	if friendly, ok := knownApps[lower]; ok {
		return friendly
	}

	return name
}
