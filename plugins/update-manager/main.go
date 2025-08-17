package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/dolphin-agent/pluginapi"
	"github.com/openai/openai-go/v2"
)

// updateManagerTool implements pluginapi.Tool for managing software updates
type updateManagerTool struct {
	currentVersion string
	repoOwner      string
	repoName       string
}

// Ensure compile-time conformance
var _ pluginapi.Tool = updateManagerTool{}

// GitHubRelease represents a GitHub release
type GitHubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

func (t updateManagerTool) Definition() openai.FunctionDefinitionParam {
	return openai.FunctionDefinitionParam{
		Name:        "update_manager",
		Description: openai.String("Check for software updates from GitHub releases and manage update installation"),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action to perform",
					"enum":        []string{"check_updates", "list_releases", "install_update", "get_current_version"},
				},
				"version": map[string]any{
					"type":        "string",
					"description": "Specific version to install (required for install_update action)",
				},
				"include_prerelease": map[string]any{
					"type":        "boolean",
					"description": "Include pre-release versions in results (default: false)",
					"default":     false,
				},
			},
			"required": []string{"action"},
		},
	}
}

func (t updateManagerTool) Call(ctx context.Context, args string) (string, error) {
	var params struct {
		Action           string `json:"action"`
		Version          string `json:"version"`
		IncludePrerelease bool   `json:"include_prerelease"`
	}
	
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("failed to parse parameters: %w", err)
	}

	switch params.Action {
	case "check_updates":
		return t.checkUpdates(params.IncludePrerelease)
	case "list_releases":
		return t.listReleases(params.IncludePrerelease)
	case "install_update":
		if params.Version == "" {
			return "", fmt.Errorf("version parameter is required for install_update action")
		}
		return t.installUpdate(params.Version)
	case "get_current_version":
		return t.getCurrentVersion()
	default:
		return "", fmt.Errorf("unknown action: %s", params.Action)
	}
}

func (t updateManagerTool) checkUpdates(includePrerelease bool) (string, error) {
	releases, err := t.fetchReleases()
	if err != nil {
		return "", fmt.Errorf("failed to fetch releases: %w", err)
	}

	latestRelease := t.findLatestRelease(releases, includePrerelease)
	if latestRelease == nil {
		return "No releases found in the repository", nil
	}

	// Create structured data for update check
	type UpdateInfo struct {
		Type            string    `json:"type"`
		Title           string    `json:"title"`
		CurrentVersion  string    `json:"currentVersion"`
		LatestVersion   string    `json:"latestVersion"`
		UpdateAvailable bool      `json:"updateAvailable"`
		ReleaseDate     time.Time `json:"releaseDate"`
		ReleaseNotes    string    `json:"releaseNotes"`
		Assets          []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"assets"`
		Repository string `json:"repository"`
	}

	updateAvailable := t.isNewerVersion(latestRelease.TagName, t.currentVersion)

	var assets []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	for _, asset := range latestRelease.Assets {
		assets = append(assets, struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		}{
			Name: asset.Name,
			Size: asset.Size,
		})
	}

	result := UpdateInfo{
		Type:            "update_check",
		Title:           "🔄 Update Check Results",
		CurrentVersion:  t.currentVersion,
		LatestVersion:   latestRelease.TagName,
		UpdateAvailable: updateAvailable,
		ReleaseDate:     latestRelease.PublishedAt,
		ReleaseNotes:    latestRelease.Body,
		Assets:          assets,
		Repository:      fmt.Sprintf("%s/%s", t.repoOwner, t.repoName),
	}

	jsonData, err := json.Marshal(result)
	if err != nil {
		// Fallback to text format
		status := "up to date"
		if updateAvailable {
			status = "update available"
		}
		return fmt.Sprintf("Current version: %s\nLatest version: %s\nStatus: %s", 
			t.currentVersion, latestRelease.TagName, status), nil
	}

	return "STRUCTURED_DATA:" + string(jsonData), nil
}

func (t updateManagerTool) listReleases(includePrerelease bool) (string, error) {
	releases, err := t.fetchReleases()
	if err != nil {
		return "", fmt.Errorf("failed to fetch releases: %w", err)
	}

	if len(releases) == 0 {
		return "No releases found in the repository", nil
	}

	// Filter releases based on prerelease preference
	var filteredReleases []GitHubRelease
	for _, release := range releases {
		if release.Draft {
			continue // Skip draft releases
		}
		if !includePrerelease && release.Prerelease {
			continue // Skip prerelease versions if not requested
		}
		filteredReleases = append(filteredReleases, release)
	}

	// Create structured data for releases list
	type ReleaseItem struct {
		Version     string    `json:"version"`
		Name        string    `json:"name"`
		Date        time.Time `json:"date"`
		Prerelease  bool      `json:"prerelease"`
		AssetCount  int       `json:"assetCount"`
		Description string    `json:"description"`
	}

	type ReleasesList struct {
		Type       string        `json:"type"`
		Title      string        `json:"title"`
		Repository string        `json:"repository"`
		Count      int           `json:"count"`
		Releases   []ReleaseItem `json:"releases"`
	}

	var releaseItems []ReleaseItem
	for _, release := range filteredReleases {
		// Truncate description if too long
		description := release.Body
		if len(description) > 200 {
			description = description[:200] + "..."
		}
		
		releaseItems = append(releaseItems, ReleaseItem{
			Version:     release.TagName,
			Name:        release.Name,
			Date:        release.PublishedAt,
			Prerelease:  release.Prerelease,
			AssetCount:  len(release.Assets),
			Description: description,
		})
	}

	result := ReleasesList{
		Type:       "releases_list",
		Title:      "📦 Available Releases",
		Repository: fmt.Sprintf("%s/%s", t.repoOwner, t.repoName),
		Count:      len(releaseItems),
		Releases:   releaseItems,
	}

	jsonData, err := json.Marshal(result)
	if err != nil {
		// Fallback to markdown format
		return t.formatReleasesMarkdown(filteredReleases), nil
	}

	return "STRUCTURED_DATA:" + string(jsonData), nil
}

func (t updateManagerTool) installUpdate(version string) (string, error) {
	releases, err := t.fetchReleases()
	if err != nil {
		return "", fmt.Errorf("failed to fetch releases: %w", err)
	}

	// Find the specified version
	var targetRelease *GitHubRelease
	for _, release := range releases {
		if release.TagName == version {
			targetRelease = &release
			break
		}
	}

	if targetRelease == nil {
		return "", fmt.Errorf("version %s not found in releases", version)
	}

	// Find appropriate asset for current platform
	asset := t.findAssetForPlatform(targetRelease.Assets)
	if asset == nil {
		return "", fmt.Errorf("no compatible asset found for platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// Download and install
	return t.downloadAndInstall(asset.BrowserDownloadURL, asset.Name, version)
}

func (t updateManagerTool) getCurrentVersion() (string, error) {
	return fmt.Sprintf("Current version: %s\nRepository: %s/%s", 
		t.currentVersion, t.repoOwner, t.repoName), nil
}

func (t updateManagerTool) fetchReleases() ([]GitHubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", t.repoOwner, t.repoName)
	
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var releases []GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	return releases, nil
}

func (t updateManagerTool) findLatestRelease(releases []GitHubRelease, includePrerelease bool) *GitHubRelease {
	if len(releases) == 0 {
		return nil
	}

	// Sort releases by published date (newest first)
	sort.Slice(releases, func(i, j int) bool {
		return releases[i].PublishedAt.After(releases[j].PublishedAt)
	})

	for _, release := range releases {
		if release.Draft {
			continue // Skip draft releases
		}
		if !includePrerelease && release.Prerelease {
			continue // Skip prerelease versions if not requested
		}
		return &release
	}

	return nil
}

func (t updateManagerTool) isNewerVersion(latest, current string) bool {
	// Simple version comparison - you might want to use a proper semver library
	// This handles basic cases like v1.2.3 vs v1.2.4
	return latest != current && latest > current
}

func (t updateManagerTool) findAssetForPlatform(assets []struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}) *struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
} {
	// Look for platform-specific assets
	platformSuffixes := map[string][]string{
		"darwin":  {"darwin", "macos", "osx"},
		"linux":   {"linux"},
		"windows": {"windows", "win"},
	}

	archSuffixes := map[string][]string{
		"amd64": {"amd64", "x86_64", "x64"},
		"arm64": {"arm64", "aarch64"},
	}

	currentOS := runtime.GOOS
	currentArch := runtime.GOARCH

	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		
		// Check if asset matches current platform
		osMatch := false
		for _, suffix := range platformSuffixes[currentOS] {
			if strings.Contains(name, suffix) {
				osMatch = true
				break
			}
		}

		archMatch := false
		for _, suffix := range archSuffixes[currentArch] {
			if strings.Contains(name, suffix) {
				archMatch = true
				break
			}
		}

		if osMatch && archMatch {
			return &asset
		}
	}

	// If no exact match, return the first binary asset
	for _, asset := range assets {
		if strings.HasSuffix(asset.Name, ".tar.gz") || 
		   strings.HasSuffix(asset.Name, ".zip") ||
		   strings.HasSuffix(asset.Name, ".exe") {
			return &asset
		}
	}

	return nil
}

func (t updateManagerTool) downloadAndInstall(url, filename, version string) (string, error) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "dolphin-update-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Download file
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to download update: %w", err)
	}
	defer resp.Body.Close()

	filePath := filepath.Join(tempDir, filename)
	file, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to create download file: %w", err)
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to save download: %w", err)
	}

	// For safety, we'll just report success without actually installing
	// In a production system, you'd want proper installation logic here
	return fmt.Sprintf("✅ Downloaded update %s to %s\n⚠️  Manual installation required for security", 
		version, filePath), nil
}

func (t updateManagerTool) formatReleasesMarkdown(releases []GitHubRelease) string {
	result := fmt.Sprintf("## 📦 Available Releases (%d found)\n\n", len(releases))
	result += "| Version | Name | Date | Assets |\n"
	result += "|---------|------|------|--------|\n"

	for _, release := range releases {
		date := release.PublishedAt.Format("2006-01-02")
		preIcon := ""
		if release.Prerelease {
			preIcon = " 🚧"
		}
		result += fmt.Sprintf("| **%s**%s | %s | %s | %d |\n", 
			release.TagName, preIcon, release.Name, date, len(release.Assets))
	}

	result += fmt.Sprintf("\n📂 **Repository:** `%s/%s`\n", t.repoOwner, t.repoName)
	return result
}

// Tool is the exported symbol the host looks up via plugin.Open().Lookup("Tool")
var Tool = updateManagerTool{
	currentVersion: "v1.0.0", // This should be read from a version file or build info
	repoOwner:      "johnjallday",
	repoName:       "dolphin-agent",
}