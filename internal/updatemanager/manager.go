package updatemanager

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
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

	return compareVersions(latestNormalized, currentNormalized) > 0
}

// compareVersions compares two dot-separated numeric versions segment by
// segment, returning -1/0/1. Plain string comparison is wrong here because
// it sorts "0.0.10" before "0.0.9". Non-numeric segments (pre-release tags
// etc.) fall back to string comparison; a missing segment counts as 0.
func compareVersions(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	for i := 0; i < len(aParts) || i < len(bParts); i++ {
		aSeg, bSeg := "0", "0"
		if i < len(aParts) {
			aSeg = aParts[i]
		}
		if i < len(bParts) {
			bSeg = bParts[i]
		}

		aNum, aErr := strconv.Atoi(aSeg)
		bNum, bErr := strconv.Atoi(bSeg)
		if aErr == nil && bErr == nil {
			if aNum != bNum {
				if aNum > bNum {
					return 1
				}
				return -1
			}
			continue
		}

		if aSeg != bSeg {
			if aSeg > bSeg {
				return 1
			}
			return -1
		}
	}

	return 0
}
