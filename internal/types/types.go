package types

import (
	"strings"
	"time"

	"github.com/oriagent/ori-pluginapi"
)

// Settings represents LLM configuration shared across agents
type Settings struct {
	Model           string  `json:"model"`
	Temperature     float64 `json:"temperature"`
	APIKey          string  `json:"api_key,omitempty"`           // OpenAI API key (optional, falls back to env var)
	SystemPrompt    string  `json:"system_prompt,omitempty"`     // Custom system prompt for the agent
	Provider        string  `json:"provider,omitempty"`          // LLM provider backing the model (e.g., openai, anthropic)
	ReasoningEffort string  `json:"reasoning_effort,omitempty"`  // Optional reasoning depth for providers that support it (currently Codex)
	MaxOutputTokens int     `json:"max_output_tokens,omitempty"` // Optional max tokens for responses
	AllowWebSearch  *bool   `json:"allow_web_search,omitempty"`  // Nil defaults to true for backward compatibility
}

// IsWebSearchAllowed returns whether this agent can use native web tools.
// Nil means legacy/default behavior (allowed).
func (s Settings) IsWebSearchAllowed() bool {
	if s.AllowWebSearch == nil {
		return true
	}
	return *s.AllowWebSearch
}

// NormalizeReasoningEffort normalizes supported reasoning effort values.
// Returns an empty string when the input is unset or invalid.
func NormalizeReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh":
		return "xhigh"
	default:
		return ""
	}
}

// EffectiveReasoningEffort returns the reasoning effort that should be used for
// the configured provider/model. Codex defaults to medium when unset.
func (s Settings) EffectiveReasoningEffort(providerName string) string {
	normalizedProvider := strings.ToLower(strings.TrimSpace(providerName))
	normalizedModel := strings.ToLower(strings.TrimSpace(s.Model))
	if normalizedProvider != "codex" && !strings.Contains(normalizedModel, "codex") {
		return ""
	}

	if normalized := NormalizeReasoningEffort(s.ReasoningEffort); normalized != "" {
		return normalized
	}
	return "medium"
}

// OnboardingState tracks user's onboarding progress
type OnboardingState struct {
	Completed      bool      `json:"completed"`
	CurrentStep    int       `json:"current_step"`
	StepsCompleted []string  `json:"steps_completed"`
	StepsSkipped   []string  `json:"steps_skipped,omitempty"`
	SkippedAt      time.Time `json:"skipped_at,omitempty"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
}

// GPUInfo contains GPU/graphics hardware information
type GPUInfo struct {
	Name           string `json:"name"`             // GPU name (e.g., "Apple M2 Pro", "NVIDIA GeForce RTX 4090")
	Vendor         string `json:"vendor"`           // GPU vendor (e.g., "Apple", "NVIDIA", "AMD", "Intel")
	VRAM           int64  `json:"vram"`             // Dedicated VRAM in bytes (0 for integrated/unified memory)
	IsDiscrete     bool   `json:"is_discrete"`      // True if discrete GPU, false if integrated
	IsAppleSilicon bool   `json:"is_apple_silicon"` // True if Apple Silicon (M1/M2/M3/etc.)
}

// DeviceInfo tracks information about the user's device
type DeviceInfo struct {
	Type           string   `json:"type"`                       // "desktop", "server", "laptop", "unknown"
	OS             string   `json:"os"`                         // Operating system (darwin, linux, windows)
	Arch           string   `json:"arch"`                       // Architecture (amd64, arm64, etc.)
	Detected       bool     `json:"detected"`                   // Whether device detection has been completed
	UserSet        bool     `json:"user_set"`                   // Whether user manually set device type
	GPU            *GPUInfo `json:"gpu,omitempty"`              // GPU information (nil if not detected)
	TotalRAMBytes  int64    `json:"total_ram_bytes,omitempty"`  // Total system RAM in bytes
	MaxModelParams string   `json:"max_model_params,omitempty"` // Maximum model parameter size (e.g., "7B", "13B", "70B")
	MemoryTier     string   `json:"memory_tier,omitempty"`      // Memory tier classification (e.g., "Basic", "Standard", "Advanced", "Professional")
	MachineName    string   `json:"machine_name,omitempty"`     // Machine/model name (e.g., "MacBook Pro", "iMac", "Dell XPS")
	ChipType       string   `json:"chip_type,omitempty"`        // Chip/processor type (e.g., "Apple M5", "Intel Core i9")
}

// MenuBarSettings tracks menu bar app preferences
type MenuBarSettings struct {
	AutoStartOnLogin bool `json:"auto_start_on_login"` // Launch Ori Agent menu bar on system startup
	Port             int  `json:"port,omitempty"`      // Server port (defaults to 8765 if not set)
}

// UserProfile represents the user's inferred or described profile
type UserProfile struct {
	PrimaryCategory     string    `json:"primary_category"`               // developer, devops, designer, data_scientist, writer, project_manager, general
	SecondaryCategories []string  `json:"secondary_categories,omitempty"` // Additional relevant categories
	Specializations     []string  `json:"specializations,omitempty"`      // e.g., "Go developer", "iOS developer"
	Summary             string    `json:"summary"`                        // Natural language description
	Confidence          float64   `json:"confidence"`                     // AI confidence (0-1)
	DetectedApps        []string  `json:"detected_apps,omitempty"`        // Apps that influenced this profile
	Description         string    `json:"description,omitempty"`          // User's self-description (if provided)
	InferredAt          time.Time `json:"inferred_at,omitempty"`          // When the profile was created
	Interests           []string  `json:"interests,omitempty"`            // e.g., "coding", "data-science", "devops"
	PreferredTools      []string  `json:"preferred_tools,omitempty"`      // e.g., "Go", "Docker", "PostgreSQL"
	WorkStyle           string    `json:"work_style,omitempty"`           // "detailed", "concise", "formal", "casual"
	PersonalizedAt      time.Time `json:"personalized_at,omitempty"`      // Non-zero = personalization completed
}

// AssistantProgress tracks global assistant progression across all agents.
type AssistantProgress struct {
	Level      int       `json:"level"`
	Experience int64     `json:"experience"`
	Rank       string    `json:"rank,omitempty"`
	Unlocks    []string  `json:"unlocks,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// NewAssistantProgress creates a default AssistantProgress object.
func NewAssistantProgress() *AssistantProgress {
	now := time.Now()
	return &AssistantProgress{
		Level:      0,
		Experience: 0,
		Rank:       "novice",
		Unlocks:    []string{},
		UpdatedAt:  now,
	}
}

// EnsureDefaults applies migration-safe defaults for assistant progress.
func (p *AssistantProgress) EnsureDefaults() {
	if p == nil {
		return
	}
	if p.Level < 0 {
		p.Level = 0
	}
	if p.Experience < 0 {
		p.Experience = 0
	}
	if p.Rank == "" {
		p.Rank = "novice"
	}
	if p.Unlocks == nil {
		p.Unlocks = []string{}
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Now()
	}
}

// AppState tracks application-level state (persisted separately from agent data)
type AppState struct {
	Onboarding        OnboardingState    `json:"onboarding"`
	Device            DeviceInfo         `json:"device"`
	UserProfile       *UserProfile       `json:"user_profile,omitempty"`       // User's inferred profile from onboarding
	AssistantProgress *AssistantProgress `json:"assistant_progress,omitempty"` // Global progression state for evolution features
	UserName          string             `json:"user_name,omitempty"`          // Optional user-provided display name
	AssistantName     string             `json:"assistant_name,omitempty"`     // Optional assistant name chosen during onboarding
	Version           string             `json:"version"`
	Theme             string             `json:"theme,omitempty"`   // "light" or "dark", defaults to "light"
	MenuBar           *MenuBarSettings   `json:"menubar,omitempty"` // Menu bar app settings
}

// LoadedPlugin represents a plugin that has been loaded and is ready to use
type LoadedPlugin struct {
	Tool              pluginapi.PluginTool `json:"-"`
	Definition        pluginapi.Tool       `json:"Definition"`
	Path              string               `json:"Path"`
	Version           string               `json:"Version,omitempty"`
	SupportsFiles     bool                 `json:"SupportsFiles,omitempty"`     // Whether plugin implements FileAttachmentHandler
	AcceptedFileTypes []string             `json:"AcceptedFileTypes,omitempty"` // List of accepted MIME types or extensions
}

// Maintainer represents a single plugin maintainer/contributor
type Maintainer struct {
	Name         string `json:"name"`                   // Full name
	Email        string `json:"email,omitempty"`        // Contact email
	Organization string `json:"organization,omitempty"` // Organization affiliation
	Website      string `json:"website,omitempty"`      // Personal/project website
	Role         string `json:"role"`                   // "author", "maintainer", "contributor"
	Primary      bool   `json:"primary,omitempty"`      // Is this the primary/original author?
}

// Platform represents a supported operating system and its architectures
type Platform struct {
	Os            string   `json:"os"`            // Operating system (e.g., "darwin", "linux", "windows")
	Architectures []string `json:"architectures"` // Supported architectures (e.g., "amd64", "arm64")
}

// Requirements represents plugin dependencies and version requirements
type Requirements struct {
	MinOriVersion string   `json:"min_ori_version,omitempty"` // Minimum ori-agent version required
	Dependencies  []string `json:"dependencies,omitempty"`    // List of required plugin names
}

// PluginMetadata contains comprehensive plugin information
type PluginMetadata struct {
	Name         string       `json:"name,omitempty"`        // Plugin name
	Version      string       `json:"version,omitempty"`     // Plugin version (semver)
	Description  string       `json:"description,omitempty"` // Short description of the plugin
	Tags         []string     `json:"tags,omitempty"`        // Normalized tags (e.g., "dev-tools", "audio")
	Maintainers  []Maintainer `json:"maintainers,omitempty"`
	License      string       `json:"license,omitempty"`      // e.g., "MIT", "Apache-2.0", "GPL-3.0"
	Repository   string       `json:"repository,omitempty"`   // Source code repository URL
	Platforms    []Platform   `json:"platforms,omitempty"`    // Supported platforms
	Requirements Requirements `json:"requirements,omitempty"` // Plugin requirements
}

// PluginRegistryEntry represents a plugin in the plugin registry
type PluginRegistryEntry struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Tags          []string        `json:"tags,omitempty"`           // Normalized tags (e.g., "dev-tools", "audio")
	Path          string          `json:"path,omitempty"`           // Local path (for local plugins)
	URL           string          `json:"url,omitempty"`            // External URL (for remote plugins)
	Version       string          `json:"version,omitempty"`        // Plugin version
	Checksum      string          `json:"checksum,omitempty"`       // SHA256 checksum for verification
	AutoUpdate    bool            `json:"auto_update,omitempty"`    // Whether to auto-update this plugin
	GitHubRepo    string          `json:"github_repo,omitempty"`    // GitHub repository (user/repo format)
	DownloadURL   string          `json:"download_url,omitempty"`   // Direct download URL for GitHub releases
	SupportedOS   []string        `json:"supported_os,omitempty"`   // Supported operating systems (darwin, linux, windows, all)
	SupportedArch []string        `json:"supported_arch,omitempty"` // Supported architectures (amd64, arm64, all)
	Platforms     []string        `json:"platforms,omitempty"`      // Supported platform strings (e.g., "darwin-arm64", "linux-amd64")
	Metadata      *PluginMetadata `json:"metadata,omitempty"`       // Plugin metadata (maintainers, license, repository)

	// Plugin Management Fields (added for dedicated plugins page)
	SourceMarketplace      string                     `json:"source_marketplace,omitempty"`      // ID of the marketplace this plugin came from (first/primary)
	SourceMarketplaces     []string                   `json:"source_marketplaces,omitempty"`     // All marketplaces this plugin appears in
	Category               string                     `json:"category,omitempty"`                // Plugin category (e.g., "System Tools", "AI/ML")
	Permissions            map[string]interface{}     `json:"permissions,omitempty"`             // Required permissions (file_access, network_access, system_commands)
	PermissionsApproved    bool                       `json:"permissions_approved,omitempty"`    // Whether permissions have been approved
	Enabled                bool                       `json:"enabled,omitempty"`                 // Whether plugin is enabled
	HealthStatus           string                     `json:"health_status,omitempty"`           // Health status (healthy, degraded, failed)
	LastUsed               *time.Time                 `json:"last_used,omitempty"`               // When plugin was last used
	VersionHistory         []VersionHistoryEntry      `json:"version_history,omitempty"`         // Previous versions for rollback
	SupportsInitialization bool                       `json:"supports_initialization,omitempty"` // Whether plugin implements InitializationProvider (cached)
	RequiredConfig         []pluginapi.ConfigVariable `json:"required_config,omitempty"`         // Cached required config variables (if any)
	InitSupportChecked     bool                       `json:"init_support_checked,omitempty"`    // Whether init support was checked and cached
}

// VersionHistoryEntry tracks information about a specific plugin version for rollback
type VersionHistoryEntry struct {
	Version     string    `json:"version"`
	Path        string    `json:"path"`
	InstalledAt time.Time `json:"installed_at"`
	Changelog   string    `json:"changelog,omitempty"`
}

// PluginRegistry contains all available plugins
type PluginRegistry struct {
	Plugins []PluginRegistryEntry `json:"plugins"`
}

// IsCompatibleWithSystem checks if a plugin is compatible with the given OS and architecture
func (p *PluginRegistryEntry) IsCompatibleWithSystem(os, arch string) bool {
	// If no supported OS specified, assume it works on all platforms
	if len(p.SupportedOS) == 0 {
		return true
	}

	// Check if "all" is in supported OS
	for _, supportedOS := range p.SupportedOS {
		if supportedOS == "all" || supportedOS == os {
			// Also check architecture if specified
			if len(p.SupportedArch) == 0 {
				return true
			}
			for _, supportedArch := range p.SupportedArch {
				if supportedArch == "all" || supportedArch == arch {
					return true
				}
			}
			// OS matches but arch doesn't
			return false
		}
	}

	return false
}

// IsCompatibleWith checks if a plugin is compatible with a given platform string (e.g., "darwin-arm64")
func (p *PluginRegistryEntry) IsCompatibleWith(platform string) bool {
	// If Platforms list is empty or contains "unknown", fall back to checking SupportedOS/SupportedArch
	if len(p.Platforms) == 0 || (len(p.Platforms) == 1 && p.Platforms[0] == "unknown") {
		// Parse platform string into OS and arch
		parts := strings.Split(platform, "-")
		if len(parts) != 2 {
			return false
		}
		return p.IsCompatibleWithSystem(parts[0], parts[1])
	}

	// Check if platform is in the Platforms list
	for _, supportedPlatform := range p.Platforms {
		if supportedPlatform == platform {
			return true
		}
		// Special case: if platform is "all", it's compatible
		if supportedPlatform == "all" {
			return true
		}
	}

	return false
}

// AgentRole represents the role of an agent in collaborative workflows
type AgentRole string

const (
	RoleOrchestrator AgentRole = "orchestrator" // Coordinates multi-agent workflows
	RoleResearcher   AgentRole = "researcher"   // Gathers information and data
	RoleAnalyzer     AgentRole = "analyzer"     // Processes and analyzes data
	RoleSynthesizer  AgentRole = "synthesizer"  // Combines findings into reports
	RoleValidator    AgentRole = "validator"    // Fact-checks and validates results
	RoleSpecialist   AgentRole = "specialist"   // Domain-specific specialist
	RoleGeneral      AgentRole = "general"      // General-purpose agent (default)
)

// Capability constants for agent capabilities
const (
	CapabilityWebSearch      = "web_search"      // Can search the web
	CapabilityCodeAnalysis   = "code_analysis"   // Can analyze code
	CapabilityDataProcessing = "data_processing" // Can process and analyze data
	CapabilityFileOperations = "file_operations" // Can perform file operations
	CapabilityAPIIntegration = "api_integration" // Can integrate with external APIs
	CapabilityResearch       = "research"        // Research and information gathering
	CapabilitySynthesis      = "synthesis"       // Can synthesize information
	CapabilityValidation     = "validation"      // Can validate and fact-check
)
