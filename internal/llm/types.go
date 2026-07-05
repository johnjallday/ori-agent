package llm

// ChatRequest represents a unified request format for all providers
type ChatRequest struct {
	// Model to use for the request
	Model string

	// Messages in the conversation
	Messages []Message

	// Tools available for the model to call
	Tools []Tool

	// Temperature controls randomness (0.0 = deterministic, 2.0 = creative)
	Temperature float64

	// SystemPrompt is the system-level instruction
	SystemPrompt string

	// MaxTokens is the maximum number of tokens to generate
	MaxTokens int

	// ContextWindowTokens is the effective context window (prompt + generation)
	// the caller has resolved for this model, in tokens. 0 means "provider
	// default". Local providers that own their context size (Ollama) map it to
	// their runtime option (num_ctx); OpenAI-compatible local servers size their
	// own context, so for them it is advisory (used for budgeting only). Cloud
	// providers ignore it.
	ContextWindowTokens int

	// Stream indicates whether to stream the response
	Stream bool

	// ReasoningEffort controls reasoning depth for providers that support it
	// (e.g., Codex: low, medium, high, xhigh).
	ReasoningEffort string

	// MCPServers carries resolved MCP server specs to expose to providers that
	// run their own native MCP loop (see ProviderCapabilities.SupportsNativeMCP).
	// Providers that use ori-agent's internal tool loop ignore this field. It is
	// populated only for agent task execution — never for system-model / parsing
	// calls.
	MCPServers []MCPServerSpec

	// WorkspaceID keys the persistent per-workspace native-MCP config. Set only
	// alongside MCPServers (native-MCP task execution).
	WorkspaceID string

	// WorkspaceDir is the workspace folder a native-MCP CLI run is confined to
	// (working directory + sandbox scope). Optional; empty leaves the run
	// unconfined. Set only alongside MCPServers.
	WorkspaceDir string
}

// MCPServerSpec is a resolved MCP server definition handed to a native-MCP
// provider (Claude Code / Codex) so its CLI can connect to the server directly.
type MCPServerSpec struct {
	// Name is the logical server name used as the MCP-config key (CLI-safe;
	// e.g. "ori-reaper", not the colon-bearing runtime name).
	Name string

	// Command is the absolute executable to launch the stdio MCP server.
	Command string

	// Args are the command arguments.
	Args []string

	// Env are environment variables passed to the server process.
	Env map[string]string
}

// ChatResponse represents a unified response format from all providers
type ChatResponse struct {
	// Content is the text response from the model
	Content string

	// ToolCalls are any tool/function calls requested by the model
	ToolCalls []ToolCall

	// FinishReason indicates why the model stopped generating
	FinishReason string

	// Usage contains token usage information
	Usage Usage

	// Model is the actual model that was used
	Model string

	// Provider is the name of the provider that generated this response
	Provider string
}

// ImageAttachment represents an image attached to a message
type ImageAttachment struct {
	// MimeType is the image MIME type (e.g., "image/png", "image/jpeg")
	MimeType string

	// Base64Data is the base64-encoded image data (without data URL prefix)
	Base64Data string
}

// Message represents a single message in a conversation
type Message struct {
	// Role is the message role: "user", "assistant", "system", or "tool"
	Role string

	// Content is the message content
	Content string

	// Images contains image attachments for vision-capable models
	Images []ImageAttachment

	// ToolCallID is used when Role is "tool" to reference the tool call
	ToolCallID string

	// ToolCalls are tool calls made by the assistant (for assistant messages)
	ToolCalls []ToolCall

	// Name is an optional name for the message sender
	Name string
}

// Tool represents a function/tool definition
type Tool struct {
	// Name of the tool
	Name string

	// Description of what the tool does
	Description string

	// Parameters is the JSON schema for the tool's parameters
	Parameters map[string]any
}

// ToolCall represents a request from the model to call a tool
type ToolCall struct {
	// ID is a unique identifier for this tool call
	ID string

	// Name is the name of the tool to call
	Name string

	// Arguments is a JSON string containing the tool arguments
	Arguments string
}

// Usage tracks token usage for a request
type Usage struct {
	// PromptTokens is the number of tokens in the prompt
	PromptTokens int

	// CompletionTokens is the number of tokens in the completion
	CompletionTokens int

	// TotalTokens is the total number of tokens used
	TotalTokens int
}

// Role constants for messages
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// FinishReason constants
const (
	FinishReasonStop          = "stop"           // Natural stop
	FinishReasonLength        = "length"         // Hit max tokens
	FinishReasonToolCalls     = "tool_calls"     // Model wants to call tools
	FinishReasonError         = "error"          // Error occurred
	FinishReasonContentFilter = "content_filter" // Content filtered
)
