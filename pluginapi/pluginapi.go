package pluginapi

import (
	"context"
)

// PluginTool is the interface that plugins must implement to be used as tools.
// This interface is provider-agnostic and works with any LLM provider (OpenAI, Claude, Ollama, etc.).
type PluginTool interface {
	// Definition returns the generic tool definition that will be automatically
	// translated to the appropriate format for the active LLM provider.
	Definition() Tool
	// Call executes the tool logic with the given arguments JSON string and returns the result JSON string.
	Call(ctx context.Context, args string) (string, error)
}

// VersionedTool extends PluginTool with version information.
// Plugins can optionally implement this interface to provide version info.
type VersionedTool interface {
	PluginTool
	// Version returns the plugin version (e.g., "1.0.0", "1.2.3-beta")
	Version() string
}

// PluginCompatibility extends PluginTool with detailed version compatibility information.
// Plugins should implement this interface to enable health checking and compatibility validation.
// Note: This was renamed from PluginMetadata to avoid conflict with the proto-generated struct.
type PluginCompatibility interface {
	PluginTool
	// Version returns the plugin version (e.g., "0.0.5", "1.2.3-beta")
	Version() string
	// MinAgentVersion returns the minimum ori-agent version required (e.g., "0.0.6")
	// Return empty string if no minimum requirement
	MinAgentVersion() string
	// MaxAgentVersion returns the maximum compatible ori-agent version (e.g., "1.0.0")
	// Return empty string if no maximum limit
	MaxAgentVersion() string
	// APIVersion returns the plugin API version (e.g., "v1", "v2")
	// This should match the agent's API version for compatibility
	APIVersion() string
}

// HealthCheckProvider allows plugins to implement custom health checks.
// Plugins can optionally implement this to validate their runtime state.
type HealthCheckProvider interface {
	// HealthCheck performs a plugin-specific health check
	// Return nil if healthy, error with description if unhealthy
	HealthCheck() error
}

// DefaultSettingsProvider allows plugins to provide default configuration values.
// This is useful for plugins that need default file paths or configuration.
type DefaultSettingsProvider interface {
	// GetDefaultSettings returns default settings as a JSON string
	GetDefaultSettings() (string, error)
}

// AgentContext provides information about the current agent to plugins.
type AgentContext struct {
	// Name is the name of the current agent (e.g., "reaper-project-manager", "default")
	Name string
	// ConfigPath is the path to the agent's main config file (agents/{name}/config.json)
	ConfigPath string
	// SettingsPath is the path to the agent's settings file (agents/{name}/agent_settings.json)
	SettingsPath string
	// AgentDir is the path to the agent's directory (agents/{name}/)
	AgentDir string
	// CurrentLocation is the current detected location zone name (e.g., "Home", "Office", "Unknown")
	// This field is populated by the location manager and provides environmental context to plugins
	CurrentLocation string
}

// AgentAwareTool extends PluginTool with agent context information.
// Plugins can optionally implement this interface to receive current agent info.
type AgentAwareTool interface {
	PluginTool
	// SetAgentContext provides the current agent information to the plugin
	SetAgentContext(ctx AgentContext)
}

// ConfigVariableType represents the type of a configuration variable.
type ConfigVariableType string

const (
	ConfigTypeString   ConfigVariableType = "string"
	ConfigTypeInt      ConfigVariableType = "int"
	ConfigTypeFloat    ConfigVariableType = "float"
	ConfigTypeBool     ConfigVariableType = "bool"
	ConfigTypeFilePath ConfigVariableType = "filepath"
	ConfigTypeDirPath  ConfigVariableType = "dirpath"
	ConfigTypePassword ConfigVariableType = "password"
	ConfigTypeURL      ConfigVariableType = "url"
	ConfigTypeEmail    ConfigVariableType = "email"
)

// ConfigVariable describes a configuration variable that the plugin requires.
type ConfigVariable struct {
	// Key is the configuration key (e.g., "api_key", "base_url", "project_path")
	Key string `json:"key"`
	// Name is the human-readable name for the variable
	Name string `json:"name"`
	// Description explains what this variable is used for
	Description string `json:"description"`
	// Type specifies the data type and input method
	Type ConfigVariableType `json:"type"`
	// Required indicates whether this variable must be provided
	Required bool `json:"required"`
	// DefaultValue provides a default value (optional)
	DefaultValue interface{} `json:"default_value,omitempty"`
	// Validation provides regex or other validation rules (optional)
	Validation string `json:"validation,omitempty"`
	// Options provides a list of valid options for enum-like variables (optional)
	Options []string `json:"options,omitempty"`
	// Placeholder text to show in input fields
	Placeholder string `json:"placeholder,omitempty"`
}

// InitializationProvider allows plugins to describe their required configuration.
// Plugins can optionally implement this interface to enable automatic initialization prompts.
type InitializationProvider interface {
	// GetRequiredConfig returns a list of configuration variables that need to be set
	GetRequiredConfig() []ConfigVariable
	// ValidateConfig checks if the provided configuration is valid
	ValidateConfig(config map[string]interface{}) error
	// InitializeWithConfig sets up the plugin with the provided configuration
	InitializeWithConfig(config map[string]interface{}) error
}

// InitializableTool combines PluginTool with InitializationProvider for full initialization support.
type InitializableTool interface {
	PluginTool
	InitializationProvider
}

// MetadataProvider allows plugins to provide detailed authorship and licensing information.
// Plugins can optionally implement this interface to provide metadata about maintainers, license, etc.
// Note: Maintainer and PluginMetadata types are generated from proto/tool.proto
type MetadataProvider interface {
	// GetMetadata returns plugin metadata (maintainers, license, repository)
	// Returns the proto-generated PluginMetadata struct
	GetMetadata() (*PluginMetadata, error)

	// GetTags returns plugin tags (typically sourced from plugin.yaml).
	// Tags should be lowercase and hyphen-separated; ori-agent may normalize/validate them.
	GetTags() []string
}

// WebPageProvider allows plugins to serve custom web pages through ori-agent.
// Plugins can optionally implement this interface to provide custom UI pages.
// Example use cases: script marketplace, configuration UI, data visualization, etc.
type WebPageProvider interface {
	// ServeWebPage handles a web page request and returns HTML content
	// path: The requested path (e.g., "marketplace", "config", "stats")
	// query: URL query parameters as key-value pairs
	// Returns: HTML content, content-type (e.g., "text/html", "application/json"), error
	ServeWebPage(path string, query map[string]string) (content string, contentType string, err error)

	// GetWebPages returns a list of available web pages this plugin provides
	// Each entry should be a path like "marketplace", "settings", etc.
	GetWebPages() []string
}

// CategoryProvider allows plugins to declare their category/tags for organization.
// Plugins can optionally implement this interface to specify which category they belong to.
type CategoryProvider interface {
	// GetCategory returns the plugin's category (e.g., "System Tools", "AI/ML", "Data Processing")
	// Can return a single category string or comma-separated categories for multiple tags
	GetCategory() string
}

// OperationInfo describes a single operation and its parameters.
type OperationInfo struct {
	// Name is the operation name (e.g., "create_project", "list_audio_plugins")
	Name string
	// Parameters is a list of parameter names for this operation
	Parameters []string
	// RequiredParameters is a list of required parameter names
	RequiredParameters []string
}

// OperationsProvider allows plugins to expose their operation-specific parameters.
// This enables better display in /tools command showing correct params per operation.
type OperationsProvider interface {
	// GetOperations returns a list of operations with their specific parameters
	GetOperations() []OperationInfo
}

// PermissionType represents the type of system permission a plugin requires.
type PermissionType string

const (
	PermissionFileAccess     PermissionType = "file_access"
	PermissionNetworkAccess  PermissionType = "network_access"
	PermissionSystemCommands PermissionType = "system_commands"
)

// PluginPermissions describes what system permissions a plugin requires.
type PluginPermissions struct {
	// FileAccess indicates if the plugin needs to read/write files
	FileAccess bool `json:"file_access"`
	// NetworkAccess indicates if the plugin needs to make network requests
	NetworkAccess bool `json:"network_access"`
	// SystemCommands indicates if the plugin needs to execute system commands
	SystemCommands bool `json:"system_commands"`
	// Description provides context about why these permissions are needed
	Description string `json:"description,omitempty"`
}

// PermissionProvider allows plugins to declare required system permissions.
// Plugins can optionally implement this interface to specify what permissions they need.
type PermissionProvider interface {
	// GetRequiredPermissions returns the permissions this plugin requires
	GetRequiredPermissions() PluginPermissions
}

// =============================================================================
// File Attachment Support
// =============================================================================

// Common MIME types for file attachments
const (
	// Audio MIME types
	MIMETypeWAV  = "audio/wav"
	MIMETypeMP3  = "audio/mpeg"
	MIMETypeAIFF = "audio/aiff"
	MIMETypeFLAC = "audio/flac"
	MIMETypeOGG  = "audio/ogg"

	// MIDI MIME types
	MIMETypeMIDI = "audio/midi"

	// Archive MIME types
	MIMETypeZIP = "application/zip"

	// Document MIME types
	MIMETypePDF = "application/pdf"
)

// Common file extensions for file attachments
const (
	ExtWAV  = ".wav"
	ExtMP3  = ".mp3"
	ExtAIFF = ".aiff"
	ExtAIF  = ".aif"
	ExtFLAC = ".flac"
	ExtOGG  = ".ogg"
	ExtMID  = ".mid"
	ExtMIDI = ".midi"
	ExtZIP  = ".zip"
	ExtPDF  = ".pdf"
)

// FileAttachment represents a file attached to a plugin call.
// This struct holds metadata and content for files uploaded through the chat interface.
type FileAttachment struct {
	// Name is the original filename (e.g., "drums.wav")
	Name string
	// Type is the MIME type (e.g., "audio/wav", "application/zip")
	Type string
	// Size is the file size in bytes
	Size int64
	// Content is the raw file content
	Content []byte
}

// FileAttachmentHandler is an optional interface that plugins can implement
// to receive file attachments from the chat interface.
// Plugins that don't implement this interface will continue to work as before,
// with files converted to text and prepended to the message.
type FileAttachmentHandler interface {
	// AcceptsFiles returns a list of accepted file types.
	// Can include MIME types (e.g., "audio/wav") or extensions (e.g., ".wav", ".mp3")
	// Return an empty slice to explicitly reject all files.
	AcceptsFiles() []string

	// CallWithFiles executes the tool with the given arguments and file attachments.
	// This method is called instead of Call() when the plugin implements this interface
	// and files are present in the request.
	// args: JSON string of tool parameters
	// files: slice of file attachments filtered to only those matching AcceptsFiles()
	CallWithFiles(ctx context.Context, args string, files []FileAttachment) (string, error)
}

// IsFileTypeAccepted checks if a file matches any of the accepted types.
// acceptedTypes can contain MIME types (e.g., "audio/wav") or extensions (e.g., ".wav")
// filename is the original filename used for extension matching
// mimeType is the file's MIME type used for MIME type matching
// Returns true if either the extension or MIME type matches any accepted type.
func IsFileTypeAccepted(acceptedTypes []string, filename string, mimeType string) bool {
	if len(acceptedTypes) == 0 {
		return false
	}

	// Normalize filename extension (lowercase, ensure leading dot)
	ext := ""
	if idx := lastIndex(filename, '.'); idx >= 0 {
		ext = toLower(filename[idx:])
	}

	// Normalize MIME type (lowercase)
	mimeType = toLower(mimeType)

	for _, accepted := range acceptedTypes {
		accepted = toLower(accepted)

		// Check if it's an extension (starts with .)
		if len(accepted) > 0 && accepted[0] == '.' {
			if ext == accepted {
				return true
			}
		} else {
			// It's a MIME type
			if mimeType == accepted {
				return true
			}
		}
	}

	return false
}

// FilterFilesByAcceptedTypes filters a slice of FileAttachments to only include
// files that match the accepted types.
func FilterFilesByAcceptedTypes(files []FileAttachment, acceptedTypes []string) []FileAttachment {
	if len(acceptedTypes) == 0 {
		return nil
	}

	var filtered []FileAttachment
	for _, f := range files {
		if IsFileTypeAccepted(acceptedTypes, f.Name, f.Type) {
			filtered = append(filtered, f)
		}
	}
	return filtered
}

// Helper functions to avoid importing strings package
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func lastIndex(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}
