package pluginapi

import (
	"fmt"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

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
	MaxOriVersion string   `yaml:"max_ori_version,omitempty"`
	ApiVersion    string   `yaml:"api_version,omitempty"`
	Dependencies  []string `yaml:"dependencies,omitempty"`
}

// YAMLConfigVariable represents a configuration variable in YAML format
type YAMLConfigVariable struct {
	Key              string                 `yaml:"key"`
	Name             string                 `yaml:"name"`
	Description      string                 `yaml:"description"`
	Type             string                 `yaml:"type"`
	Required         bool                   `yaml:"required"`
	DefaultValue     interface{}            `yaml:"default_value,omitempty"`
	Validation       string                 `yaml:"validation,omitempty"`
	Options          []string               `yaml:"options,omitempty"`
	Placeholder      string                 `yaml:"placeholder,omitempty"`
	PlatformDefaults map[string]interface{} `yaml:"platform_defaults,omitempty"`
}

// YAMLConfig represents the config section in plugin.yaml
type YAMLConfig struct {
	Variables []YAMLConfigVariable `yaml:"variables,omitempty"`
}

// YAMLToolParameter represents a parameter for a tool in YAML format
type YAMLToolParameter struct {
	Name        string      `yaml:"name"`
	Type        string      `yaml:"type"` // string, integer, number, boolean, enum, array, object
	Description string      `yaml:"description"`
	Required    bool        `yaml:"required,omitempty"`
	Default     interface{} `yaml:"default,omitempty"`
	Enum        []string    `yaml:"enum,omitempty"` // For enum type
	Items       *struct {
		Type string `yaml:"type"`
	} `yaml:"items,omitempty"` // For array type
	Properties map[string]YAMLToolParameter `yaml:"properties,omitempty"` // For object type
	Min        *float64                     `yaml:"min,omitempty"`        // For number/integer validation
	Max        *float64                     `yaml:"max,omitempty"`        // For number/integer validation
	MinLength  *int                         `yaml:"min_length,omitempty"` // For string validation
	MaxLength  *int                         `yaml:"max_length,omitempty"` // For string validation
	Pattern    string                       `yaml:"pattern,omitempty"`    // For string regex validation
}

// YAMLOperationDefinition represents an operation-specific tool definition in YAML format.
type YAMLOperationDefinition struct {
	Parameters []YAMLToolParameter `yaml:"parameters,omitempty"` // Array format: - name: foo ...
}

// YAMLToolDefinition represents a tool definition in YAML format
type YAMLToolDefinition struct {
	Name        string                             `yaml:"name"`
	Description string                             `yaml:"description"`
	Parameters  []YAMLToolParameter                `yaml:"parameters,omitempty"` // Array format: - name: foo ...
	Operations  map[string]YAMLOperationDefinition `yaml:"operations,omitempty"` // Per-operation parameters
}

// PluginConfig represents the complete plugin configuration from plugin.yaml
type PluginConfig struct {
	Name         string              `yaml:"name"`
	Version      string              `yaml:"version"`
	Description  string              `yaml:"description"`
	Tags         []string            `yaml:"tags,omitempty"`
	License      string              `yaml:"license"`
	Repository   string              `yaml:"repository"`
	Platforms    []YAMLPlatform      `yaml:"platforms"`
	Maintainers  []YAMLMaintainer    `yaml:"maintainers"`
	Requirements YAMLRequirements    `yaml:"requirements,omitempty"`
	Config       YAMLConfig          `yaml:"config,omitempty"`
	Tool         *YAMLToolDefinition `yaml:"tool_definition,omitempty"` // Optional tool definition
	Assets       []string            `yaml:"assets,omitempty"`
	WebPages     []string            `yaml:"web_pages,omitempty"`
}

// readPluginConfig parses and validates plugin configuration from embedded YAML.
// This is an internal function used by ServePlugin.
// Returns an error if the configuration is invalid.
func readPluginConfig(embeddedYAML string) (PluginConfig, error) {
	var config PluginConfig

	// Parse YAML
	if err := yaml.Unmarshal([]byte(embeddedYAML), &config); err != nil {
		return PluginConfig{}, fmt.Errorf("invalid plugin config YAML: %w", err)
	}

	// Validate required fields
	if config.Name == "" {
		return PluginConfig{}, fmt.Errorf("invalid plugin config: missing required field: name")
	}
	if config.Version == "" {
		return PluginConfig{}, fmt.Errorf("invalid plugin config: missing required field: version")
	}
	if config.Description == "" {
		return PluginConfig{}, fmt.Errorf("invalid plugin config: missing required field: description")
	}
	if config.License == "" {
		return PluginConfig{}, fmt.Errorf("invalid plugin config: missing required field: license")
	}
	if config.Repository == "" {
		return PluginConfig{}, fmt.Errorf("invalid plugin config: missing required field: repository")
	}
	if len(config.Platforms) == 0 {
		return PluginConfig{}, fmt.Errorf("invalid plugin config: missing required field: platforms")
	}
	if len(config.Maintainers) == 0 {
		return PluginConfig{}, fmt.Errorf("invalid plugin config: missing required field: maintainers")
	}

	// Validate version field is valid semver
	if _, err := semver.NewVersion(config.Version); err != nil {
		return PluginConfig{}, fmt.Errorf("invalid plugin config: invalid semver format for version: %s", config.Version)
	}

	// Validate repository field is a valid URL
	if _, err := url.ParseRequestURI(config.Repository); err != nil {
		return PluginConfig{}, fmt.Errorf("invalid plugin config: invalid URL format for repository: %s", config.Repository)
	}

	// Validate platforms
	for i, platform := range config.Platforms {
		if platform.OS == "" {
			return PluginConfig{}, fmt.Errorf("invalid plugin config: platform[%d] missing os field", i)
		}
		if len(platform.Architectures) == 0 {
			return PluginConfig{}, fmt.Errorf("invalid plugin config: platform[%d] has empty architectures array", i)
		}
	}

	// Validate maintainers
	for i, maintainer := range config.Maintainers {
		if maintainer.Name == "" {
			return PluginConfig{}, fmt.Errorf("invalid plugin config: maintainer[%d] missing name field", i)
		}
		if maintainer.Email == "" {
			return PluginConfig{}, fmt.Errorf("invalid plugin config: maintainer[%d] missing email field", i)
		}
	}

	// Validate min_ori_version if provided
	if config.Requirements.MinOriVersion != "" {
		if _, err := semver.NewVersion(config.Requirements.MinOriVersion); err != nil {
			return PluginConfig{}, fmt.Errorf("invalid plugin config: invalid semver format for min_ori_version: %s", config.Requirements.MinOriVersion)
		}
	}

	return config, nil
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
		Tags:         c.Tags,
		License:      c.License,
		Repository:   c.Repository,
		Maintainers:  maintainers,
		Platforms:    platforms,
		Requirements: requirements,
	}, nil
}

// ToConfigVariables converts YAMLConfig to a slice of ConfigVariable with template expansion
func (c *PluginConfig) ToConfigVariables() []ConfigVariable {
	if len(c.Config.Variables) == 0 {
		return nil
	}

	result := make([]ConfigVariable, 0, len(c.Config.Variables))
	for _, yamlVar := range c.Config.Variables {
		// Expand templates for default value and placeholder
		defaultValue := expandTemplates(yamlVar.DefaultValue)
		placeholder := ""
		if yamlVar.Placeholder != "" {
			if expanded := expandTemplates(yamlVar.Placeholder); expanded != nil {
				if str, ok := expanded.(string); ok {
					placeholder = str
				}
			}
		}

		configVar := ConfigVariable{
			Key:          yamlVar.Key,
			Name:         yamlVar.Name,
			Description:  yamlVar.Description,
			Type:         ConfigVariableType(yamlVar.Type),
			Required:     yamlVar.Required,
			DefaultValue: defaultValue,
			Validation:   yamlVar.Validation,
			Options:      yamlVar.Options,
			Placeholder:  placeholder,
		}

		// Apply platform-specific defaults if they exist
		if len(yamlVar.PlatformDefaults) > 0 {
			if platformDefault, ok := yamlVar.PlatformDefaults[getCurrentPlatform()]; ok {
				configVar.DefaultValue = expandTemplates(platformDefault)
				// Also update placeholder if it was using default
				if placeholder == "" {
					if expanded := expandTemplates(platformDefault); expanded != nil {
						if str, ok := expanded.(string); ok {
							configVar.Placeholder = str
						}
					}
				}
			}
		}

		result = append(result, configVar)
	}

	return result
}

// expandTemplates expands template variables in a string or interface{} value
// Supports: {{USER_HOME}}, {{OS}}, {{ARCH}}, ~ (home directory expansion)
func expandTemplates(value interface{}) interface{} {
	strValue, ok := value.(string)
	if !ok {
		return value
	}

	// Get user home directory
	usr, err := user.Current()
	homeDir := ""
	if err == nil {
		homeDir = usr.HomeDir
	}

	// Template replacements
	replacements := map[string]string{
		"{{USER_HOME}}": homeDir,
		"{{OS}}":        runtime.GOOS,
		"{{ARCH}}":      runtime.GOARCH,
	}

	result := strValue
	for template, replacement := range replacements {
		result = strings.ReplaceAll(result, template, replacement)
	}

	// Expand ~ to home directory (Unix-style)
	if strings.HasPrefix(result, "~/") && homeDir != "" {
		result = filepath.Join(homeDir, result[2:])
	}

	// Expand environment variables like %APPDATA% (Windows-style)
	if strings.Contains(result, "%") {
		result = os.ExpandEnv(result)
	}

	return result
}

// getCurrentPlatform returns the current platform name (darwin, windows, linux)
func getCurrentPlatform() string {
	return runtime.GOOS
}
