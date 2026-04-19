package updatemanager

import (
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/johnjallday/ori-agent/internal/logger"
)

// Manager handles software update checking and management
type Manager struct {
	CurrentVersion string
	RepoOwner      string
	RepoName       string
	httpClient     *http.Client
	githubToken    string
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
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		token = os.Getenv("ORI_GITHUB_TOKEN")
	}

	return &Manager{
		CurrentVersion: currentVersion,
		RepoOwner:      repoOwner,
		RepoName:       repoName,
		httpClient:     &http.Client{Timeout: 15 * time.Second},
		githubToken:    token,
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

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", fmt.Sprintf("ori-agent/%s (+https://github.com/%s/%s)", m.CurrentVersion, m.RepoOwner, m.RepoName))

	if m.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+m.githubToken)
	}

	client := m.httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			detail = "GitHub API returned an empty error response"
		}

		hint := "Set GITHUB_TOKEN or GH_TOKEN to increase GitHub API limits."
		return nil, fmt.Errorf("GitHub API returned status %d: %s (%s)", resp.StatusCode, detail, hint)
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
	// Normalize versions by removing 'v' prefix for comparison
	latestNormalized := strings.TrimPrefix(latest, "v")
	currentNormalized := strings.TrimPrefix(current, "v")

	// Simple version comparison - you might want to use a proper semver library
	// This handles basic cases like v1.2.3 vs v1.2.4
	return latestNormalized != currentNormalized && latestNormalized > currentNormalized
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
	// Get current working directory (where the app is running)
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}

	// Create temp file path with original name
	tempFilePath := filepath.Join(currentDir, filename+".tmp")

	// Download file using configured client (with timeout)
	client := m.httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to download update: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Read file content into memory for checksum verification.
	// Limit to 500 MB to prevent memory exhaustion from malicious releases.
	const maxDownloadSize = 500 << 20
	fileContent, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadSize))
	if err != nil {
		return "", fmt.Errorf("failed to read download: %w", err)
	}

	// SECURITY: Attempt to fetch and verify checksum
	// Try common checksum file naming conventions
	checksumVerified := false
	checksumURLs := []string{
		url + ".sha256",
		url + ".sha256sum",
		strings.TrimSuffix(url, filepath.Ext(url)) + ".sha256",
	}

	actualChecksum := sha256.Sum256(fileContent)
	actualChecksumHex := hex.EncodeToString(actualChecksum[:])

	for _, checksumURL := range checksumURLs {
		expectedChecksum, err := m.fetchChecksum(checksumURL)
		if err != nil {
			continue // Try next URL
		}

		// Checksum files may contain "checksum  filename" format, extract just the checksum
		checksumParts := strings.Fields(expectedChecksum)
		if len(checksumParts) > 0 {
			expectedChecksum = checksumParts[0]
		}
		expectedChecksum = strings.TrimSpace(strings.ToLower(expectedChecksum))

		if actualChecksumHex == expectedChecksum {
			checksumVerified = true
			logger.Debug("Checksum verified successfully", logger.Fields{
				"url":      checksumURL,
				"checksum": actualChecksumHex,
			})
			break
		} else {
			logger.Warn("Checksum mismatch", logger.Fields{
				"expected": expectedChecksum,
				"actual":   actualChecksumHex,
			})
		}
	}

	// Log warning if checksum couldn't be verified (but don't block - some releases may not have checksums)
	if !checksumVerified {
		logger.Warn("Could not verify checksum for downloaded file - no valid checksum file found", logger.Fields{
			"url":      url,
			"checksum": actualChecksumHex,
		})
	}

	// Write file to disk
	file, err := os.Create(tempFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to create download file: %w", err)
	}

	_, err = file.Write(fileContent)
	if err != nil {
		if closeErr := file.Close(); closeErr != nil {
			logger.Verbosef("Warning: failed to close file after write error: %v", closeErr)
		}
		if removeErr := os.Remove(tempFilePath); removeErr != nil {
			logger.Verbosef("Warning: failed to remove temp file after write error: %v", removeErr)
		}
		return "", fmt.Errorf("failed to save download: %w", err)
	}
	if err := file.Close(); err != nil {
		// Close error after successful write could mean data wasn't flushed
		if removeErr := os.Remove(tempFilePath); removeErr != nil {
			logger.Verbosef("Warning: failed to remove temp file after close error: %v", removeErr)
		}
		return "", fmt.Errorf("failed to close download file: %w", err)
	}

	// Determine the final filename - use "ori-agent" or "ori-agent.exe"
	var finalName string
	if runtime.GOOS == "windows" {
		finalName = "ori-agent.exe"
	} else {
		finalName = "ori-agent"
	}
	finalPath := filepath.Join(currentDir, finalName)

	// Backup current binary if it exists
	if _, err := os.Stat(finalPath); err == nil {
		backupPath := finalPath + ".old"
		if err := os.Rename(finalPath, backupPath); err != nil {
			_ = os.Remove(tempFilePath)
			return "", fmt.Errorf("failed to backup current binary: %w", err)
		}
	}

	// Rename downloaded file to final name
	if err := os.Rename(tempFilePath, finalPath); err != nil {
		_ = os.Remove(tempFilePath)
		return "", fmt.Errorf("failed to rename downloaded file: %w", err)
	}

	// Make the downloaded file executable (for unix-like systems)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(finalPath, 0755); err != nil {
			return "", fmt.Errorf("failed to make file executable: %w", err)
		}
	}

	return finalPath, nil
}

// fetchChecksum fetches a checksum file from the given URL
func (m *Manager) fetchChecksum(url string) (string, error) {
	client := m.httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	if m.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+m.githubToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum file not found: %d", resp.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, 1024)) // Limit to 1KB for safety
	if err != nil {
		return "", err
	}

	return string(content), nil
}
