package chathttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"

	"github.com/johnjallday/ori-agent/internal/types"
)

const defaultWorkspaceSavedNoteName = "Saved Chat Note"

type workspaceSaveNoteToolResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Action  string `json:"action"`
	Message string `json:"message"`
}

func (h *Handler) maybeHandleWorkspaceSaveNoteWithoutModel(
	w http.ResponseWriter,
	ag *resolvedChatAgent,
	agentName string,
	userMessage string,
	baseCtx context.Context,
	sessionID string,
	plannerDecision *types.PlannerDecision,
) bool {
	if h == nil || ag == nil || ag.WorkspaceTools == nil {
		return false
	}
	if !matchesWorkspaceSaveNoteIntent(userMessage) {
		return false
	}

	content := latestAssistantMessageText(ag.Messages)
	if strings.TrimSpace(content) == "" {
		responseText := "I couldn't find earlier assistant content in this chat to save as a note."
		h.finishWorkspaceNoteFallbackResponse(w, ag, agentName, baseCtx, sessionID, responseText, nil, plannerDecision)
		return true
	}

	args, err := json.Marshal(map[string]string{
		"name":    deriveWorkspaceSavedNoteName(content),
		"content": content,
	})
	if err != nil {
		responseText := "I couldn't prepare the note payload for saving."
		h.finishWorkspaceNoteFallbackResponse(w, ag, agentName, baseCtx, sessionID, responseText, nil, plannerDecision)
		return true
	}

	result := h.executeDirectTool(baseCtx, ag, agentName, &DirectToolCommand{
		ToolName: "workspace_save_note",
		Args:     string(args),
	})

	receipt := buildActionReceipt(
		"workspace_note",
		"Saved prior assistant response to a workspace note",
		"non-tool-provider workspace save fallback",
		result.ToolName,
		result.ToolArgs,
		result.Result,
		result.ExecutionTimeMs,
		result.Success,
		result.Error,
	)

	responseText := "I couldn't save that to a note."
	if result.Success {
		savedName := deriveWorkspaceSavedNoteName(content)
		var payload workspaceSaveNoteToolResponse
		if err := json.Unmarshal([]byte(result.Result), &payload); err == nil && strings.TrimSpace(payload.Name) != "" {
			savedName = strings.TrimSpace(payload.Name)
		}
		responseText = "Saved the previous answer to note " + quoteForResponse(savedName) + "."
	} else if strings.TrimSpace(result.Error) != "" {
		responseText = "I couldn't save that to a note: " + strings.TrimSpace(result.Error)
	}

	toolCall := ToolCallResult{
		Function:   result.ToolName,
		Args:       result.ToolArgs,
		Result:     result.Result,
		DurationMs: result.ExecutionTimeMs,
		Success:    result.Success,
	}

	h.finishWorkspaceNoteFallbackResponse(
		w,
		ag,
		agentName,
		baseCtx,
		sessionID,
		responseText,
		[]ActionReceipt{receipt},
		plannerDecision,
		toolCall,
	)
	return true
}

func (h *Handler) finishWorkspaceNoteFallbackResponse(
	w http.ResponseWriter,
	ag *resolvedChatAgent,
	agentName string,
	baseCtx context.Context,
	sessionID string,
	responseText string,
	receipts []ActionReceipt,
	plannerDecision *types.PlannerDecision,
	toolCalls ...ToolCallResult,
) {
	responseText = strings.TrimSpace(responseText)
	if responseText == "" {
		responseText = emptyResponseText
	}

	ag.Messages = append(ag.Messages, openai.AssistantMessage(responseText))
	_ = h.persistAgent(agentName, ag.Agent)
	h.storeMessageInSession(baseCtx, sessionID, "assistant", responseText)

	payload := map[string]any{
		"response": responseText,
	}
	if len(toolCalls) > 0 {
		payload["toolCalls"] = toolCalls
	}

	writeJSONResponse(w, attachPlannerDecision(attachActionReceipts(attachRouteMetadata(payload, chatRouteMetadata{
		Mode:      routeModeAssistantChat,
		Reason:    "server-side workspace note save fallback",
		ToolCount: len(toolCalls),
	}), receipts), plannerDecision))
}

func matchesWorkspaceSaveNoteIntent(userMessage string) bool {
	message := normalizeWorkspaceSaveIntentText(userMessage)
	if message == "" {
		return false
	}

	hasSaveVerb := strings.Contains(message, "save") ||
		strings.Contains(message, "store") ||
		strings.Contains(message, "remember")
	if !hasSaveVerb {
		return false
	}

	if strings.Contains(message, "note") || strings.Contains(message, "notes") {
		return true
	}

	switch message {
	case "save it", "save this", "save that", "store it", "store this", "store that", "remember it", "remember this", "remember that":
		return true
	default:
		return false
	}
}

func latestAssistantMessageText(messages []openai.ChatCompletionMessageParamUnion) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.OfAssistant == nil {
			continue
		}
		if content := assistantMessageText(msg.OfAssistant); strings.TrimSpace(content) != "" {
			return content
		}
	}
	return ""
}

func assistantMessageText(msg *openai.ChatCompletionAssistantMessageParam) string {
	if msg == nil || param.IsOmitted(msg.Content.OfString) {
		return ""
	}
	return msg.Content.OfString.Value
}

func deriveWorkspaceSavedNoteName(content string) string {
	for _, line := range strings.Split(content, "\n") {
		name := strings.TrimSpace(line)
		name = strings.TrimLeft(name, "#*- >\t")
		name = strings.Trim(name, "`*_ ")
		name = strings.TrimSuffix(name, ":")
		if name == "" {
			continue
		}
		if utf8.RuneCountInString(name) > 80 {
			runes := []rune(name)
			name = strings.TrimSpace(string(runes[:77])) + "..."
		}
		return name
	}
	return defaultWorkspaceSavedNoteName
}

func normalizeWorkspaceSaveIntentText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\n", " ")
	return strings.Join(strings.Fields(text), " ")
}

func quoteForResponse(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return quoteForResponse(defaultWorkspaceSavedNoteName)
	}
	return `"` + value + `"`
}
