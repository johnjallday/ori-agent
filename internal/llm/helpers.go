package llm

import "encoding/json"

// NewUserMessage creates a new user message
func NewUserMessage(content string) Message {
	return Message{
		Role:    RoleUser,
		Content: content,
	}
}

// NewAssistantMessage creates a new assistant message
func NewAssistantMessage(content string) Message {
	return Message{
		Role:    RoleAssistant,
		Content: content,
	}
}

// NewSystemMessage creates a new system message
func NewSystemMessage(content string) Message {
	return Message{
		Role:    RoleSystem,
		Content: content,
	}
}

// NewToolMessage creates a new tool response message
func NewToolMessage(toolCallID, content string) Message {
	return Message{
		Role:       RoleTool,
		Content:    content,
		ToolCallID: toolCallID,
	}
}

// ParseToolArguments parses the tool arguments JSON into the given struct
func ParseToolArguments(arguments string, v interface{}) error {
	return json.Unmarshal([]byte(arguments), v)
}

// MarshalToolArguments converts a struct to tool arguments JSON string
func MarshalToolArguments(v interface{}) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// IsToolCallResponse checks if the response contains tool calls
func IsToolCallResponse(resp *ChatResponse) bool {
	return len(resp.ToolCalls) > 0
}

// HasContent checks if the response has content
func HasContent(resp *ChatResponse) bool {
	return resp.Content != ""
}
