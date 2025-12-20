package types

import (
	"fmt"
	"strings"
	"time"
)

// Marketplace represents a plugin marketplace configuration
type Marketplace struct {
	ID          string     `json:"id"`                     // Unique identifier (UUID or slug)
	Name        string     `json:"name"`                   // Display name
	Source      string     `json:"source"`                 // URL or GitHub user/repo format
	SourceType  string     `json:"source_type"`            // "url" or "github"
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

// ResolveURL converts the source to a fetchable URL
// For GitHub repos, converts user/repo to raw.githubusercontent.com URL
// For URLs, returns the source as-is
func (m *Marketplace) ResolveURL() string {
	sourceType := m.SourceType
	if sourceType == "" {
		sourceType = m.detectSourceType()
	}

	if sourceType == "github" {
		// Convert user/repo to raw GitHub URL
		return fmt.Sprintf("https://raw.githubusercontent.com/%s/main/plugin_registry.json", m.Source)
	}
	return m.Source
}

// Validate checks if the marketplace configuration is valid
func (m *Marketplace) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("marketplace name is required")
	}
	if strings.TrimSpace(m.Source) == "" {
		return fmt.Errorf("marketplace source is required")
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
