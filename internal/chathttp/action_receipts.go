package chathttp

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ActionReceipt captures a structured audit entry for a completed action.
type ActionReceipt struct {
	ID            string    `json:"id"`
	Timestamp     time.Time `json:"timestamp"`
	Type          string    `json:"type"`
	Action        string    `json:"action"`
	Reason        string    `json:"reason,omitempty"`
	ToolName      string    `json:"tool_name,omitempty"`
	Arguments     string    `json:"arguments,omitempty"`
	ResultPreview string    `json:"result_preview,omitempty"`
	DurationMs    int64     `json:"duration_ms,omitempty"`
	Success       bool      `json:"success"`
	Error         string    `json:"error,omitempty"`
	Commands      []string  `json:"commands,omitempty"`
	Targets       []string  `json:"targets,omitempty"`
	Locations     []string  `json:"locations,omitempty"`
}

var fileLocationPattern = regexp.MustCompile(`([A-Za-z0-9._/\-\\]+:\d+(?::\d+)?)`)

func newActionReceiptID() string {
	return "action_" + uuid.NewString()
}

func buildActionReceipt(
	actionType string,
	action string,
	reason string,
	toolName string,
	arguments string,
	result string,
	durationMs int64,
	success bool,
	errorText string,
) ActionReceipt {
	commands, targets := extractActionHints(arguments)
	locations := extractLocationHints(result)

	return ActionReceipt{
		ID:            newActionReceiptID(),
		Timestamp:     time.Now(),
		Type:          strings.TrimSpace(actionType),
		Action:        strings.TrimSpace(action),
		Reason:        strings.TrimSpace(reason),
		ToolName:      strings.TrimSpace(toolName),
		Arguments:     strings.TrimSpace(arguments),
		ResultPreview: truncateResultPreview(result, 280),
		DurationMs:    durationMs,
		Success:       success,
		Error:         strings.TrimSpace(errorText),
		Commands:      commands,
		Targets:       targets,
		Locations:     locations,
	}
}

func attachActionReceipts(payload map[string]any, receipts []ActionReceipt) map[string]any {
	if payload == nil {
		payload = make(map[string]any)
	}
	if len(receipts) == 0 {
		return payload
	}
	payload["action_receipts"] = receipts
	payload["action_count"] = len(receipts)
	return payload
}

func extractLocationHints(result string) []string {
	if strings.TrimSpace(result) == "" {
		return nil
	}

	matches := fileLocationPattern.FindAllString(result, 8)
	return uniqueNonEmpty(matches)
}

func extractActionHints(args string) (commands []string, targets []string) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return nil, nil
	}

	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		if looksLikeCommand(trimmed) {
			return []string{trimmed}, nil
		}
		if looksLikeTarget(trimmed) {
			return nil, []string{trimmed}
		}
		return nil, nil
	}

	collectActionHints(payload, "", &commands, &targets)
	return uniqueNonEmpty(commands), uniqueNonEmpty(targets)
}

func collectActionHints(value any, key string, commands *[]string, targets *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for k, v := range typed {
			collectActionHints(v, strings.ToLower(strings.TrimSpace(k)), commands, targets)
		}
	case []any:
		for _, item := range typed {
			collectActionHints(item, key, commands, targets)
		}
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return
		}
		switch {
		case isCommandKey(key):
			*commands = append(*commands, text)
		case isTargetKey(key):
			*targets = append(*targets, text)
		default:
			if looksLikeCommand(text) {
				*commands = append(*commands, text)
			}
			if looksLikeTarget(text) {
				*targets = append(*targets, text)
			}
		}
	}
}

func isCommandKey(key string) bool {
	switch key {
	case "cmd", "command", "shell_command", "script", "exec":
		return true
	default:
		return false
	}
}

func isTargetKey(key string) bool {
	switch key {
	case "path", "file", "filepath", "filename", "directory", "cwd", "workdir", "target", "url", "location":
		return true
	default:
		return false
	}
}

func looksLikeCommand(text string) bool {
	if text == "" {
		return false
	}
	if strings.Contains(text, "\n") {
		return true
	}
	return strings.Contains(text, " ") && !looksLikeTarget(text)
}

func looksLikeTarget(text string) bool {
	if text == "" {
		return false
	}
	if strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") {
		return true
	}
	if strings.HasPrefix(text, "/") || strings.HasPrefix(text, "./") || strings.HasPrefix(text, "../") {
		return true
	}
	if strings.Contains(text, `\`) {
		return true
	}
	return strings.Contains(text, "/") && !strings.Contains(text, " ")
}

func uniqueNonEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
