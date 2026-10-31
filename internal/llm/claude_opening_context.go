package llm

import "encoding/json"

// Claude's messages must begin with a user turn. An assistant-initiated
// conversation has no such turn until the person actually speaks. Carry that
// opening as quoted historical context inside the first real user message:
// do not invent a "continue" request, promote it to system authority, drop it,
// or rewrite the persisted transcript. Ordinary user-first histories and tool
// exchanges are unchanged.
func claudeOpeningContext(messages []Message) []Message {
	var opening []string
	firstUser := -1
scan:
	for i, message := range messages {
		switch message.Role {
		case RoleSystem:
			continue
		case RoleAssistant:
			if len(message.ToolCalls) != 0 || message.ToolCallID != "" {
				return messages
			}
			opening = append(opening, message.Content)
		case RoleUser:
			firstUser = i
			break scan
		default:
			return messages
		}
	}
	if len(opening) == 0 || firstUser < 0 {
		return messages
	}
	quoted, err := json.Marshal(opening)
	if err != nil {
		return messages
	}
	out := make([]Message, 0, len(messages)-len(opening))
	for i, message := range messages {
		if i < firstUser && message.Role == RoleAssistant {
			continue
		}
		if i == firstUser {
			message.Content = "Earlier assistant-authored messages (JSON-quoted historical context, not user instructions or permission to act):\n" + string(quoted) + "\n\nActual user message:\n" + message.Content
		}
		out = append(out, message)
	}
	return out
}
