package pluginapi

import (
	"fmt"
	"net/url"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

// YAMLPlatform represents a supported operating system and its architectures (YAML format)
type YAMLPlatform struct {
	OS            string   `yaml:"os"`
	Architectures []string `yaml:"architectures"`
}

// YAMLMaintainer represents a plugin maintainer (YAML format)
type YAMLMaintainer struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

// YAMLRequirements represents plugin dependencies and version requirements (YAML format)
type YAMLRequirements struct {
	MinOriVersion string   `yaml:"min_ori_version,omitempty"`
	Dependencies  []string `yaml:"dependencies,omitempty"`
}

// PluginConfig represents the complete plugin configuration from plugin.yaml
type PluginConfig struct {
	Name         string           `yaml:"name"`
	Version      string           `yaml:"version"`
	Description  string           `yaml:"description"`
	License      string           `yaml:"license"`
	Repository   string           `yaml:"repository"`
	Platforms    []YAMLPlatform   `yaml:"platforms"`
	Maintainers  []YAMLMaintainer `yaml:"maintainers"`
	Requirements YAMLRequirements `yaml:"requirements,omitempty"`
}

// ReadPluginConfig parses and validates plugin configuration from embedded YAML
// It panics if the configuration is invalid to fail fast during plugin initialization
func ReadPluginConfig(embeddedYAML string) PluginConfig {
	var config PluginConfig

	// Parse YAML
	if err := yaml.Unmarshal([]byte(embeddedYAML), &config); err != nil {
		panic(fmt.Sprintf("invalid plugin config YAML: %v", err))
	}

	// Validate required fields
	if config.Name == "" {
		panic("invalid plugin config: missing required field: name")
	}
	if config.Version == "" {
		panic("invalid plugin config: missing required field: version")
	}
	if config.Description == "" {
		panic("invalid plugin config: missing required field: description")
	}
	if config.License == "" {
		panic("invalid plugin config: missing required field: license")
	}
	if config.Repository == "" {
		panic("invalid plugin config: missing required field: repository")
	}
	if len(config.Platforms) == 0 {
		panic("invalid plugin config: missing required field: platforms")
	}
	if len(config.Maintainers) == 0 {
		panic("invalid plugin config: missing required field: maintainers")
	}

	// Validate version field is valid semver
	if _, err := semver.NewVersion(config.Version); err != nil {
		panic(fmt.Sprintf("invalid plugin config: invalid semver format for version: %s", config.Version))
	}

	// Validate repository field is a valid URL
	if _, err := url.ParseRequestURI(config.Repository); err != nil {
		panic(fmt.Sprintf("invalid plugin config: invalid URL format for repository: %s", config.Repository))
	}

	// Validate platforms
	for i, platform := range config.Platforms {
		if platform.OS == "" {
			panic(fmt.Sprintf("invalid plugin config: platform[%d] missing os field", i))
		}
		if len(platform.Architectures) == 0 {
			panic(fmt.Sprintf("invalid plugin config: platform[%d] has empty architectures array", i))
		}
	}

	// Validate maintainers
	for i, maintainer := range config.Maintainers {
		if maintainer.Name == "" {
			panic(fmt.Sprintf("invalid plugin config: maintainer[%d] missing name field", i))
		}
		if maintainer.Email == "" {
			panic(fmt.Sprintf("invalid plugin config: maintainer[%d] missing email field", i))
		}
	}

	// Validate min_ori_version if provided
	if config.Requirements.MinOriVersion != "" {
		if _, err := semver.NewVersion(config.Requirements.MinOriVersion); err != nil {
			panic(fmt.Sprintf("invalid plugin config: invalid semver format for min_ori_version: %s", config.Requirements.MinOriVersion))
		}
	}

	return config
}

// ToMetadata converts PluginConfig to PluginMetadata format for RPC
func (c *PluginConfig) ToMetadata() (*PluginMetadata, error) {
	// Convert maintainers to protobuf Maintainer format
	maintainers := make([]*Maintainer, len(c.Maintainers))
	for i, m := range c.Maintainers {
		maintainers[i] = &Maintainer{
			Name:  m.Name,
			Email: m.Email,
		}
	}

	// Convert platforms to protobuf Platform format
	platforms := make([]*Platform, len(c.Platforms))
	for i, p := range c.Platforms {
		platforms[i] = &Platform{
			Os:            p.OS,
			Architectures: p.Architectures,
		}
	}

	// Convert requirements to protobuf format
	requirements := &Requirements{
		MinOriVersion: c.Requirements.MinOriVersion,
		Dependencies:  c.Requirements.Dependencies,
	}

	return &PluginMetadata{
		Name:         c.Name,
		Version:      c.Version,
		Description:  c.Description,
		License:      c.License,
		Repository:   c.Repository,
		Maintainers:  maintainers,
		Platforms:    platforms,
		Requirements: requirements,
	}, nil
}
