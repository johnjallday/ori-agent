package types

import (
	"time"

	"github.com/johnjallday/ori-agent/pluginapi"
	"github.com/openai/openai-go/v2"
)

// Settings represents LLM configuration shared across agents
type Settings struct {
	Model        string  `json:"model"`
	Temperature  float64 `json:"temperature"`
	APIKey       string  `json:"api_key,omitempty"`       // OpenAI API key (optional, falls back to env var)
	SystemPrompt string  `json:"system_prompt,omitempty"` // Custom system prompt for the agent
}

// OnboardingState tracks user's onboarding progress
type OnboardingState struct {
	Completed      bool      `json:"completed"`
	CurrentStep    int       `json:"current_step"`
	StepsCompleted []string  `json:"steps_completed"`
	SkippedAt      time.Time `json:"skipped_at,omitempty"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
}

// DeviceInfo tracks information about the user's device
type DeviceInfo struct {
	Type     string `json:"type"`      // "desktop", "server", "laptop", "unknown"
	OS       string `json:"os"`        // Operating system (darwin, linux, windows)
	Arch     string `json:"arch"`      // Architecture (amd64, arm64, etc.)
	Detected bool   `json:"detected"`  // Whether device detection has been completed
	UserSet  bool   `json:"user_set"`  // Whether user manually set device type
}

// AppState tracks application-level state (persisted separately from agent data)
type AppState struct {
	Onboarding OnboardingState `json:"onboarding"`
	Device     DeviceInfo      `json:"device"`
	Version    string          `json:"version"`
	Theme      string          `json:"theme,omitempty"` // "light" or "dark", defaults to "light"
}

// LoadedPlugin represents a plugin that has been loaded and is ready to use
type LoadedPlugin struct {
	Tool       pluginapi.Tool                 `json:"-"`
	Definition openai.FunctionDefinitionParam `json:"Definition"`
	Path       string                         `json:"Path"`
	Version    string                         `json:"Version,omitempty"`
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

// PluginMetadata contains comprehensive plugin information
type PluginMetadata struct {
	Maintainers []Maintainer `json:"maintainers,omitempty"`
	License     string       `json:"license,omitempty"`    // e.g., "MIT", "Apache-2.0", "GPL-3.0"
	Repository  string       `json:"repository,omitempty"` // Source code repository URL
}

// PluginRegistryEntry represents a plugin in the plugin registry
type PluginRegistryEntry struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Path          string          `json:"path,omitempty"`           // Local path (for local plugins)
	URL           string          `json:"url,omitempty"`            // External URL (for remote plugins)
	Version       string          `json:"version,omitempty"`        // Plugin version
	Checksum      string          `json:"checksum,omitempty"`       // SHA256 checksum for verification
	AutoUpdate    bool            `json:"auto_update,omitempty"`    // Whether to auto-update this plugin
	GitHubRepo    string          `json:"github_repo,omitempty"`    // GitHub repository (user/repo format)
	DownloadURL   string          `json:"download_url,omitempty"`   // Direct download URL for GitHub releases
	SupportedOS   []string        `json:"supported_os,omitempty"`   // Supported operating systems (darwin, linux, windows, all)
	SupportedArch []string        `json:"supported_arch,omitempty"` // Supported architectures (amd64, arm64, all)
	Metadata      *PluginMetadata `json:"metadata,omitempty"`       // Plugin metadata (maintainers, license, repository)
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
	CapabilityWebSearch      = "web_search"       // Can search the web
	CapabilityCodeAnalysis   = "code_analysis"    // Can analyze code
	CapabilityDataProcessing = "data_processing"  // Can process and analyze data
	CapabilityFileOperations = "file_operations"  // Can perform file operations
	CapabilityAPIIntegration = "api_integration"  // Can integrate with external APIs
	CapabilityResearch       = "research"         // Research and information gathering
	CapabilitySynthesis      = "synthesis"        // Can synthesize information
	CapabilityValidation     = "validation"       // Can validate and fact-check
)
