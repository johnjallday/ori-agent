package types

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// Marketplace represents a plugin marketplace configuration
type Marketplace struct {
	ID          string     `json:"id"`                     // Unique identifier (UUID or slug)
	Name        string     `json:"name"`                   // Display name
	Source      string     `json:"source"`                 // URL or GitHub user/repo format
	SourceType  string     `json:"source_type"`            // "url", "github", "gitlab", "bitbucket", or "file"
	Enabled     bool       `json:"enabled"`                // Whether marketplace is active
	Order       int        `json:"order"`                  // Priority order (lower = higher priority)
	IsOfficial  bool       `json:"is_official"`            // True for the default official marketplace
	LastFetched *time.Time `json:"last_fetched,omitempty"` // Last successful fetch
	LastError   string     `json:"last_error,omitempty"`   // Last error message if any
}

// MarketplaceConfig holds the list of configured marketplaces
type MarketplaceConfig struct {
	Marketplaces []Marketplace `json:"marketplaces"`
}

// IsGitHub returns true if the source is in GitHub user/repo format
func (m *Marketplace) IsGitHub() bool {
	return m.SourceType == "github" || m.detectSourceType() == "github"
}

// detectSourceType determines whether the source is a URL or GitHub repo
func (m *Marketplace) detectSourceType() string {
	source := strings.TrimSpace(m.Source)
	if strings.HasPrefix(source, "file://") || filepath.IsAbs(source) {
		return "file"
	}
	// If it starts with http:// or https://, it's a URL
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return "url"
	}
	// If it matches user/repo pattern (no slashes except one), it's GitHub
	parts := strings.Split(source, "/")
	if len(parts) == 2 && len(parts[0]) > 0 && len(parts[1]) > 0 {
		return "github"
	}
	// Default to URL
	return "url"
}

// DetectMarketplaceSourceType detects the marketplace source type for a raw source string.
func DetectMarketplaceSourceType(source string) string {
	return (&Marketplace{Source: source}).detectSourceType()
}

// ResolveLocalMarketplacePath resolves a local marketplace source to an absolute filesystem path.
// Accepts absolute paths or file:// URLs and rejects relative paths.
func ResolveLocalMarketplacePath(source string) (string, error) {
	trimmed := strings.TrimSpace(source)
	if strings.HasPrefix(trimmed, "file://") {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return "", fmt.Errorf("invalid file URL: %w", err)
		}
		path := parsed.Path
		if parsed.Host != "" && parsed.Host != "localhost" {
			path = filepath.Join(string(filepath.Separator)+parsed.Host, path)
		}
		unescaped, err := url.PathUnescape(path)
		if err != nil {
			return "", fmt.Errorf("invalid file URL path: %w", err)
		}
		trimmed = unescaped
	}

	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("local marketplace path must be absolute")
	}

	return filepath.Clean(trimmed), nil
}

// ResolveURL converts the source to a fetchable URL
// For GitHub repos, converts user/repo to raw.githubusercontent.com URL
// For GitLab/Bitbucket project URLs, converts to raw file URLs
// For other URLs, returns the source as-is
func (m *Marketplace) ResolveURL() string {
	sourceType := m.SourceType
	if sourceType == "" {
		sourceType = m.detectSourceType()
	}

	source := strings.TrimSpace(m.Source)
	source = strings.TrimSuffix(source, "/") // Remove trailing slash

	if sourceType == "file" {
		if path, err := ResolveLocalMarketplacePath(source); err == nil {
			return path
		}
	}

	if sourceType == "github" {
		// Convert user/repo to raw GitHub URL
		return fmt.Sprintf("https://raw.githubusercontent.com/%s/main/plugin_registry.json", source)
	}

	// Handle GitLab project URLs
	// e.g., https://gitlab.com/user/repo -> https://gitlab.com/user/repo/-/raw/main/plugin_registry.json
	if strings.Contains(source, "gitlab.com") || strings.Contains(source, "gitlab.") {
		// Check if it's already a raw URL
		if strings.Contains(source, "/-/raw/") {
			return source
		}
		// Check if it already points to a file (has file extension)
		if strings.HasSuffix(source, ".json") {
			return source
		}
		// Convert project URL to raw file URL
		return fmt.Sprintf("%s/-/raw/main/plugin_registry.json", source)
	}

	// Handle Bitbucket project URLs
	// e.g., https://bitbucket.org/user/repo -> https://bitbucket.org/user/repo/raw/main/plugin_registry.json
	if strings.Contains(source, "bitbucket.org") || strings.Contains(source, "bitbucket.") {
		// Check if it's already a raw URL
		if strings.Contains(source, "/raw/") {
			return source
		}
		// Check if it already points to a file
		if strings.HasSuffix(source, ".json") {
			return source
		}
		// Convert project URL to raw file URL
		return fmt.Sprintf("%s/raw/main/plugin_registry.json", source)
	}

	// Handle GitHub web URLs (not shorthand)
	// e.g., https://github.com/user/repo -> https://raw.githubusercontent.com/user/repo/main/plugin_registry.json
	if strings.Contains(source, "github.com") {
		// Check if it's already a raw URL
		if strings.Contains(source, "raw.githubusercontent.com") {
			return source
		}
		// Check if it already points to a file
		if strings.HasSuffix(source, ".json") {
			return source
		}
		// Extract user/repo from GitHub URL
		// Format: https://github.com/user/repo or https://github.com/user/repo/...
		parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(source, "https://"), "http://"), "/")
		if len(parts) >= 3 && parts[0] == "github.com" {
			userRepo := parts[1] + "/" + parts[2]
			return fmt.Sprintf("https://raw.githubusercontent.com/%s/main/plugin_registry.json", userRepo)
		}
	}

	return source
}

// Validate checks if the marketplace configuration is valid
func (m *Marketplace) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("marketplace name is required")
	}
	if strings.TrimSpace(m.Source) == "" {
		return fmt.Errorf("marketplace source is required")
	}
	if m.detectSourceType() == "file" {
		if _, err := ResolveLocalMarketplacePath(m.Source); err != nil {
			return err
		}
	}
	return nil
}

// OfficialMarketplaceID is the ID for the default official marketplace
const OfficialMarketplaceID = "official"

// DefaultOfficialMarketplace returns the default official marketplace configuration
func DefaultOfficialMarketplace() Marketplace {
	return Marketplace{
		ID:         OfficialMarketplaceID,
		Name:       "Ori Official",
		Source:     "johnjallday/ori-plugin-registry",
		SourceType: "github",
		Enabled:    true,
		Order:      0,
		IsOfficial: true,
	}
}
