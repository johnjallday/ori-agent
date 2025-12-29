// Package detector provides cross-platform application detection
// to infer user profiles based on recently used applications.
package detector

import (
	"context"
	"runtime"
	"time"
)

// DetectedApp represents an application found on the user's system.
type DetectedApp struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	LastUsed time.Time `json:"last_used"`
	IconPath string    `json:"icon_path,omitempty"`
}

// Detector defines the interface for platform-specific app detection.
type Detector interface {
	// DetectApps scans for recently used applications.
	// Returns only apps used within the recency window.
	DetectApps(ctx context.Context) ([]DetectedApp, error)

	// Platform returns the platform name (darwin, windows, linux).
	Platform() string
}

// Config holds configuration for app detection.
type Config struct {
	// RecencyDays is the number of days to look back for app usage.
	// Apps not used within this window are ignored.
	RecencyDays int

	// IncludeSystemApps determines whether to include system applications.
	IncludeSystemApps bool

	// CustomAppPaths allows specifying additional paths to scan.
	CustomAppPaths []string
}

// DefaultConfig returns the default detection configuration.
func DefaultConfig() Config {
	return Config{
		RecencyDays:       7,
		IncludeSystemApps: false,
		CustomAppPaths:    nil,
	}
}

// New creates a new Detector for the current platform.
func New(cfg Config) (Detector, error) {
	switch runtime.GOOS {
	case "darwin":
		return NewDarwinDetector(cfg), nil
	case "windows":
		return NewWindowsDetector(cfg), nil
	case "linux":
		return NewLinuxDetector(cfg), nil
	default:
		return NewFallbackDetector(cfg), nil
	}
}

// IsRecentlyUsed checks if an app was used within the recency window.
func IsRecentlyUsed(lastUsed time.Time, recencyDays int) bool {
	if lastUsed.IsZero() {
		return false
	}
	cutoff := time.Now().AddDate(0, 0, -recencyDays)
	return lastUsed.After(cutoff)
}

// FilterRecentApps filters a list of apps to only include recently used ones.
func FilterRecentApps(apps []DetectedApp, recencyDays int) []DetectedApp {
	var recent []DetectedApp
	for _, app := range apps {
		if IsRecentlyUsed(app.LastUsed, recencyDays) {
			recent = append(recent, app)
		}
	}
	return recent
}

// KnownAppCategories maps application names to categories for profile inference.
var KnownAppCategories = map[string]string{
	// Developer Tools
	"Visual Studio Code": "developer",
	"Code":               "developer",
	"Xcode":              "developer",
	"Android Studio":     "developer",
	"IntelliJ IDEA":      "developer",
	"PyCharm":            "developer",
	"GoLand":             "developer",
	"WebStorm":           "developer",
	"Sublime Text":       "developer",
	"Atom":               "developer",
	"Vim":                "developer",
	"Neovim":             "developer",
	"Cursor":             "developer",
	"Zed":                "developer",

	// Terminal & Shell
	"Terminal":         "developer",
	"iTerm":            "developer",
	"iTerm2":           "developer",
	"Hyper":            "developer",
	"Warp":             "developer",
	"Alacritty":        "developer",
	"kitty":            "developer",
	"Windows Terminal": "developer",

	// DevOps & Infrastructure
	"Docker":         "devops",
	"Docker Desktop": "devops",
	"Lens":           "devops",
	"Kubernetes":     "devops",
	"Postman":        "devops",
	"Insomnia":       "devops",

	// Design Tools
	"Figma":             "designer",
	"Sketch":            "designer",
	"Adobe Photoshop":   "designer",
	"Adobe Illustrator": "designer",
	"Adobe XD":          "designer",
	"Affinity Designer": "designer",
	"Affinity Photo":    "designer",
	"Canva":             "designer",
	"Blender":           "designer",

	// Data & Analytics
	"Jupyter Notebook": "data",
	"JupyterLab":       "data",
	"R Studio":         "data",
	"RStudio":          "data",
	"Tableau":          "data",
	"DataGrip":         "data",
	"DBeaver":          "data",
	"Excel":            "data",
	"Microsoft Excel":  "data",
	"Numbers":          "data",

	// Writing & Productivity
	"Notion":         "writer",
	"Obsidian":       "writer",
	"Bear":           "writer",
	"Ulysses":        "writer",
	"iA Writer":      "writer",
	"Microsoft Word": "writer",
	"Word":           "writer",
	"Google Docs":    "writer",
	"Pages":          "writer",
	"Typora":         "writer",

	// Communication & Collaboration
	"Slack":           "collaboration",
	"Discord":         "collaboration",
	"Microsoft Teams": "collaboration",
	"Zoom":            "collaboration",
	"Google Meet":     "collaboration",

	// Project Management
	"Linear":   "project_manager",
	"Jira":     "project_manager",
	"Asana":    "project_manager",
	"Trello":   "project_manager",
	"Monday":   "project_manager",
	"ClickUp":  "project_manager",
	"Basecamp": "project_manager",

	// Version Control
	"GitHub Desktop": "developer",
	"GitKraken":      "developer",
	"Sourcetree":     "developer",
	"Tower":          "developer",
	"Fork":           "developer",

	// Browsers (generic, less useful for profiling)
	"Safari":         "general",
	"Google Chrome":  "general",
	"Firefox":        "general",
	"Arc":            "general",
	"Microsoft Edge": "general",
	"Brave Browser":  "general",
}

// GetAppCategory returns the category for a known app, or empty string if unknown.
func GetAppCategory(appName string) string {
	if cat, ok := KnownAppCategories[appName]; ok {
		return cat
	}
	return ""
}
