package llm

import (
	"encoding/json"
	"strings"
)

// NewUserMessage creates a new user message
func NewUserMessage(content string) Message {
	return Message{
		Role:    RoleUser,
		Content: content,
	}
}

// NewUserMessageWithImages creates a new user message with image attachments
func NewUserMessageWithImages(content string, images []ImageAttachment) Message {
	return Message{
		Role:    RoleUser,
		Content: content,
		Images:  images,
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
func ParseToolArguments(arguments string, v any) error {
	return json.Unmarshal([]byte(arguments), v)
}

// MarshalToolArguments converts a struct to tool arguments JSON string
func MarshalToolArguments(v any) (string, error) {
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

// StripCodeFence returns the content inside a markdown code fence when the
// text begins with one (e.g. "```json\n{...}\n```"); otherwise it returns
// the trimmed input unchanged. Models routinely wrap JSON answers in fences
// even when instructed not to, so callers should strip before unmarshaling.
func StripCodeFence(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return text
	}

	var inner []string
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			inner = append(inner, line)
		}
	}
	return strings.TrimSpace(strings.Join(inner, "\n"))
}
