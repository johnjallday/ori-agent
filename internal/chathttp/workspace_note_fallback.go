package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
)

const defaultWorkspaceSavedNoteName = "Saved Chat Note"

type workspaceNoteFallbackMode string

const (
	workspaceNoteFallbackNone         workspaceNoteFallbackMode = ""
	workspaceNoteFallbackCreateNew    workspaceNoteFallbackMode = "create_new"
	workspaceNoteFallbackAppendRecent workspaceNoteFallbackMode = "append_recent"
)

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
	latestAssistant := latestAssistantMessageText(ag.Messages)
	mode := detectWorkspaceNoteFallbackMode(userMessage, latestAssistant)
	if mode == workspaceNoteFallbackNone {
		return false
	}

	content := sanitizeAssistantContentForNote(latestAssistant)
	if strings.TrimSpace(content) == "" {
		responseText := "I couldn't find earlier assistant content in this chat to save as a note."
		h.finishWorkspaceNoteFallbackResponse(w, ag, agentName, baseCtx, sessionID, responseText, nil, plannerDecision)
		return true
	}

	request := map[string]string{
		"content": content,
	}
	actionSummary := "Saved prior assistant response to a workspace note"
	actionReason := "workspace note save fallback"
	expectedNoteName := deriveWorkspaceSavedNoteName(content)

	if mode == workspaceNoteFallbackAppendRecent {
		noteID, noteName, mergedContent, err := prepareWorkspaceNoteAppend(baseCtx, ag.WorkspaceTools, content)
		if err != nil {
			responseText := "I couldn't update the existing note: " + strings.TrimSpace(err.Error())
			h.finishWorkspaceNoteFallbackResponse(w, ag, agentName, baseCtx, sessionID, responseText, nil, plannerDecision)
			return true
		}
		request["note_id"] = noteID
		request["content"] = mergedContent
		if noteName != "" {
			expectedNoteName = noteName
		}
		actionSummary = "Updated the most recent workspace note with prior assistant response"
		actionReason = "workspace note append fallback"
	} else {
		request["name"] = expectedNoteName
	}

	args, err := json.Marshal(request)
	if err != nil {
		responseText := "I couldn't prepare the note payload for saving."
		h.finishWorkspaceNoteFallbackResponse(w, ag, agentName, baseCtx, sessionID, responseText, nil, plannerDecision)
		return true
	}

	result := h.executeDirectTool(baseCtx, ag, &DirectToolCommand{
		ToolName: "workspace_save_note",
		Args:     string(args),
	})

	receipt := buildActionReceipt(
		"workspace_note",
		actionSummary,
		actionReason,
		result.ToolName,
		result.ToolArgs,
		result.Result,
		result.ExecutionTimeMs,
		result.Success,
		result.Error,
	)

	responseText := "I couldn't save that to a note."
	if result.Success {
		savedName := expectedNoteName
		var payload workspaceSaveNoteToolResponse
		if err := json.Unmarshal([]byte(result.Result), &payload); err == nil && strings.TrimSpace(payload.Name) != "" {
			savedName = strings.TrimSpace(payload.Name)
		}
		if mode == workspaceNoteFallbackAppendRecent {
			responseText = "Added the previous answer to note " + quoteForResponse(savedName) + "."
		} else {
			responseText = "Saved the previous answer to note " + quoteForResponse(savedName) + "."
		}
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
	if err := h.persistAgent(agentName, ag.Agent); err != nil {
		logger.Warn("Failed to persist agent after note fallback", logger.Fields{"agent": agentName, "error": err})
	}
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

func detectWorkspaceNoteFallbackMode(userMessage, latestAssistant string) workspaceNoteFallbackMode {
	message := normalizeWorkspaceSaveIntentText(userMessage)
	if message == "" {
		return workspaceNoteFallbackNone
	}

	if matchesWorkspaceSeparateNoteIntent(message) {
		return workspaceNoteFallbackCreateNew
	}
	if matchesWorkspaceAppendNoteIntent(message) {
		return workspaceNoteFallbackAppendRecent
	}
	if matchesWorkspaceSaveNoteIntent(message) {
		return workspaceNoteFallbackCreateNew
	}
	if isWorkspaceNoteAffirmation(message) && assistantPromptRequestsNoteAppend(latestAssistant) {
		return workspaceNoteFallbackAppendRecent
	}

	return workspaceNoteFallbackNone
}

func matchesWorkspaceSeparateNoteIntent(message string) bool {
	if message == "" || !strings.Contains(message, "note") {
		return false
	}
	return strings.Contains(message, "separate note") ||
		strings.Contains(message, "another note") ||
		strings.Contains(message, "new note") ||
		(strings.Contains(message, "separate ") && strings.Contains(message, " note")) ||
		(strings.HasPrefix(message, "start ") && strings.Contains(message, " note"))
}

func matchesWorkspaceAppendNoteIntent(message string) bool {
	if message == "" || !strings.Contains(message, "note") {
		return false
	}
	return strings.Contains(message, "add this to") ||
		strings.Contains(message, "add it to") ||
		strings.Contains(message, "append") ||
		strings.Contains(message, "update the note") ||
		strings.Contains(message, "to my note") ||
		strings.Contains(message, "to the note")
}

func isWorkspaceNoteAffirmation(message string) bool {
	switch message {
	case "yes", "yes please", "yeah", "yep", "sure", "ok", "okay", "please do", "do it", "go ahead", "sounds good":
		return true
	default:
		return false
	}
}

func assistantPromptRequestsNoteAppend(message string) bool {
	normalized := normalizeWorkspaceSaveIntentText(message)
	if normalized == "" || !strings.Contains(normalized, "note") {
		return false
	}
	return (strings.Contains(normalized, "want me to add") ||
		strings.Contains(normalized, "would you like me to add") ||
		strings.Contains(normalized, "should i add")) &&
		strings.Contains(normalized, "note")
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

func sanitizeAssistantContentForNote(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}

	parts := strings.Split(content, "\n\n")
	for len(parts) > 0 {
		last := strings.TrimSpace(parts[len(parts)-1])
		if !assistantMessageLooksLikeNotePrompt(last) {
			break
		}
		parts = parts[:len(parts)-1]
	}

	sanitized := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if sanitized == "" {
		return content
	}
	return sanitized
}

func assistantMessageLooksLikeNotePrompt(message string) bool {
	normalized := normalizeWorkspaceSaveIntentText(message)
	if normalized == "" || !strings.Contains(normalized, "note") {
		return false
	}
	return strings.Contains(normalized, "want me to") ||
		strings.Contains(normalized, "would you like me to") ||
		strings.Contains(normalized, "should i")
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

func prepareWorkspaceNoteAppend(
	ctx context.Context,
	workspaceTools *WorkspaceToolProvider,
	addition string,
) (string, string, string, error) {
	if workspaceTools == nil || workspaceTools.sessionStore == nil || strings.TrimSpace(workspaceTools.workspaceID) == "" {
		return "", "", "", fmt.Errorf("workspace note tools are unavailable")
	}

	notes, err := workspaceTools.sessionStore.ListNotesByWorkspace(ctx, workspaceTools.workspaceID)
	if err != nil {
		return "", "", "", err
	}
	if len(notes) == 0 {
		return "", "", "", fmt.Errorf("no existing workspace notes found")
	}

	sort.SliceStable(notes, func(i, j int) bool {
		return notes[i].UpdatedAt.After(notes[j].UpdatedAt)
	})
	target := notes[0]
	existing, err := workspaceTools.sessionStore.GetNote(ctx, target.ID)
	if err != nil {
		return "", "", "", err
	}

	return existing.ID, existing.Name, mergeWorkspaceNoteContent(existing.Content, addition), nil
}

func mergeWorkspaceNoteContent(existing, addition string) string {
	existing = strings.TrimSpace(existing)
	addition = strings.TrimSpace(addition)
	switch {
	case existing == "":
		return addition
	case addition == "":
		return existing
	case strings.Contains(existing, addition):
		return existing
	default:
		return existing + "\n\n" + addition
	}
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
		value = defaultWorkspaceSavedNoteName
	}
	return `"` + value + `"`
}
