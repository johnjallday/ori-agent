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

	// Stream indicates whether to stream the response
	Stream bool
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

// Message represents a single message in a conversation
type Message struct {
	// Role is the message role: "user", "assistant", "system", or "tool"
	Role string

	// Content is the message content
	Content string

	// ToolCallID is used when Role is "tool" to reference the tool call
	ToolCallID string

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
	Parameters map[string]interface{}
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
	FinishReasonStop       = "stop"        // Natural stop
	FinishReasonLength     = "length"      // Hit max tokens
	FinishReasonToolCalls  = "tool_calls"  // Model wants to call tools
	FinishReasonError      = "error"       // Error occurred
	FinishReasonContentFilter = "content_filter" // Content filtered
)
