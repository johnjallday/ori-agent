package updatemanager

import (
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
)

// Manager handles software update checking and management
type Manager struct {
	CurrentVersion string
	RepoOwner      string
	RepoName       string
}

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

// UpdateInfo represents update check results
type UpdateInfo struct {
	CurrentVersion  string    `json:"currentVersion"`
	LatestVersion   string    `json:"latestVersion"`
	UpdateAvailable bool      `json:"updateAvailable"`
	ReleaseDate     time.Time `json:"releaseDate"`
	ReleaseNotes    string    `json:"releaseNotes"`
	Assets          []Asset   `json:"assets"`
	Repository      string    `json:"repository"`
}

// Asset represents a release asset
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// ReleaseInfo represents release information
type ReleaseInfo struct {
	Version     string    `json:"version"`
	Name        string    `json:"name"`
	Date        time.Time `json:"date"`
	Prerelease  bool      `json:"prerelease"`
	AssetCount  int       `json:"assetCount"`
	Description string    `json:"description"`
}

// NewManager creates a new update manager
func NewManager(currentVersion, repoOwner, repoName string) *Manager {
	return &Manager{
		CurrentVersion: currentVersion,
		RepoOwner:      repoOwner,
		RepoName:       repoName,
	}
}

// CheckUpdates checks for available updates
func (m *Manager) CheckUpdates(includePrerelease bool) (*UpdateInfo, error) {
	releases, err := m.fetchReleases()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch releases: %w", err)
	}

	latestRelease := m.findLatestRelease(releases, includePrerelease)
	if latestRelease == nil {
		return nil, fmt.Errorf("no releases found")
	}

	var assets []Asset
	for _, asset := range latestRelease.Assets {
		assets = append(assets, Asset{
			Name: asset.Name,
			URL:  asset.BrowserDownloadURL,
			Size: asset.Size,
		})
	}

	updateAvailable := m.isNewerVersion(latestRelease.TagName, m.CurrentVersion)

	return &UpdateInfo{
		CurrentVersion:  m.CurrentVersion,
		LatestVersion:   latestRelease.TagName,
		UpdateAvailable: updateAvailable,
		ReleaseDate:     latestRelease.PublishedAt,
		ReleaseNotes:    latestRelease.Body,
		Assets:          assets,
		Repository:      fmt.Sprintf("%s/%s", m.RepoOwner, m.RepoName),
	}, nil
}

// ListReleases lists all available releases
func (m *Manager) ListReleases(includePrerelease bool, limit int) ([]ReleaseInfo, error) {
	releases, err := m.fetchReleases()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch releases: %w", err)
	}

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

	// Apply limit if specified
	if limit > 0 && len(filteredReleases) > limit {
		filteredReleases = filteredReleases[:limit]
	}

	var releaseInfos []ReleaseInfo
	for _, release := range filteredReleases {
		// Truncate description if too long
		description := release.Body
		if len(description) > 200 {
			description = description[:200] + "..."
		}

		releaseInfos = append(releaseInfos, ReleaseInfo{
			Version:     release.TagName,
			Name:        release.Name,
			Date:        release.PublishedAt,
			Prerelease:  release.Prerelease,
			AssetCount:  len(release.Assets),
			Description: description,
		})
	}

	return releaseInfos, nil
}

// DownloadUpdate downloads a specific version
func (m *Manager) DownloadUpdate(version string) (string, error) {
	releases, err := m.fetchReleases()
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
	asset := m.findAssetForPlatform(targetRelease.Assets)
	if asset == nil {
		return "", fmt.Errorf("no compatible asset found for platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// Download file
	return m.downloadFile(asset.BrowserDownloadURL, asset.Name, version)
}

// GetCurrentVersion returns current version info
func (m *Manager) GetCurrentVersion() map[string]string {
	return map[string]string{
		"version":    m.CurrentVersion,
		"repository": fmt.Sprintf("%s/%s", m.RepoOwner, m.RepoName),
	}
}

func (m *Manager) fetchReleases() ([]GitHubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", m.RepoOwner, m.RepoName)

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

func (m *Manager) findLatestRelease(releases []GitHubRelease, includePrerelease bool) *GitHubRelease {
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

func (m *Manager) isNewerVersion(latest, current string) bool {
	// Simple version comparison - you might want to use a proper semver library
	// This handles basic cases like v1.2.3 vs v1.2.4
	return latest != current && latest > current
}

func (m *Manager) findAssetForPlatform(assets []struct {
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

func (m *Manager) downloadFile(url, filename, version string) (string, error) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "dolphin-update-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Download file
	resp, err := http.Get(url)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to download update: %w", err)
	}
	defer resp.Body.Close()

	filePath := filepath.Join(tempDir, filename)
	file, err := os.Create(filePath)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to create download file: %w", err)
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to save download: %w", err)
	}

	return filePath, nil
}