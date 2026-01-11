package sessionhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/config"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
)

// AutoClassifyHandler handles auto-classification of chat sessions
type AutoClassifyHandler struct {
	sessionStore  session.HybridStore
	agentStore    store.Store
	llmFactory    *llm.Factory
	configManager *config.Manager
}

// NewAutoClassifyHandler creates a new AutoClassifyHandler
func NewAutoClassifyHandler(
	sessionStore session.HybridStore,
	agentStore store.Store,
	llmFactory *llm.Factory,
	configManager *config.Manager,
) *AutoClassifyHandler {
	return &AutoClassifyHandler{
		sessionStore:  sessionStore,
		agentStore:    agentStore,
		llmFactory:    llmFactory,
		configManager: configManager,
	}
}

// AutoClassifyRequest represents the request to auto-classify a session
type AutoClassifyRequest struct {
	SessionID string `json:"session_id"`
}

// AutoClassifyResponse represents the classification result
type AutoClassifyResponse struct {
	WorkspaceID   string `json:"workspace_id,omitempty"`
	WorkspaceName string `json:"workspace_name,omitempty"`
	AgentName     string `json:"agent_name,omitempty"`
	Reasoning     string `json:"reasoning,omitempty"`
	Applied       bool   `json:"applied"`
}

// HandleAutoClassify handles POST /api/sessions/auto-classify
func (h *AutoClassifyHandler) HandleAutoClassify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}

	var req AutoClassifyRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}

	if req.SessionID == "" {
		_ = orihttp.RespondBadRequest(w, "session_id is required")
		return
	}

	// Get the session
	sess, err := h.sessionStore.GetSession(r.Context(), req.SessionID)
	if err != nil {
		logger.Error("Failed to get session", logger.Fields{"error": err, "session_id": req.SessionID})
		_ = orihttp.RespondNotFound(w, "Session not found")
		return
	}

	// Get messages for the session
	messages, err := h.sessionStore.GetMessages(r.Context(), req.SessionID)
	if err != nil {
		logger.Error("Failed to get messages", logger.Fields{"error": err, "session_id": req.SessionID})
		_ = orihttp.RespondInternalError(w, "Failed to get session messages")
		return
	}

	// Need at least 2 messages (1 user + 1 assistant) for classification
	if len(messages) < 2 {
		orihttp.WriteJSON(w, AutoClassifyResponse{
			Applied:   false,
			Reasoning: "Not enough messages for classification. Need at least 2 messages.",
		})
		return
	}

	// Get all workspaces
	workspaceList, err := h.sessionStore.ListWorkspaces(r.Context())
	if err != nil {
		logger.Error("Failed to list workspaces", logger.Fields{"error": err})
		workspaceList = nil // Continue without workspaces
	}

	// Convert to pointer slice for consistency
	var workspaces []*session.Workspace
	for i := range workspaceList {
		workspaces = append(workspaces, &workspaceList[i])
	}

	// Get all agents
	agents, _ := h.agentStore.ListAgents()

	// Get the configured system model
	systemProvider, systemModel := h.configManager.GetSystemModel()
	result, err := h.llmFactory.GetSystemModelProvider(systemProvider, systemModel)
	if err != nil {
		logger.Error("System model not available for auto-classify", logger.Fields{"error": err})
		_ = orihttp.RespondServiceUnavailable(w, "System model not configured")
		return
	}

	// Generate classification using LLM
	classification, err := h.classifySession(r.Context(), result.Provider, result.Model, sess, messages, workspaces, agents)
	if err != nil {
		logger.Error("Auto-classify failed", logger.Fields{"error": err})
		orihttp.WriteJSON(w, AutoClassifyResponse{
			Applied:   false,
			Reasoning: "Classification failed: " + err.Error(),
		})
		return
	}

	// Apply the classification if we have results
	applied := false
	if classification.WorkspaceID != "" || classification.AgentName != "" {
		// Check if any changes are needed
		needsUpdate := false

		if classification.WorkspaceID != "" && classification.WorkspaceID != sess.FolderID {
			sess.FolderID = classification.WorkspaceID
			needsUpdate = true
		}

		if classification.AgentName != "" && classification.AgentName != sess.AgentName {
			sess.AgentName = classification.AgentName
			needsUpdate = true
		}

		if needsUpdate {
			err = h.sessionStore.UpdateSession(r.Context(), sess)
			if err != nil {
				logger.Error("Failed to update session", logger.Fields{"error": err})
			} else {
				applied = true
			}
		}
	}

	classification.Applied = applied
	orihttp.WriteJSON(w, classification)
}

// classifySession uses LLM to analyze the session and suggest workspace/agent
func (h *AutoClassifyHandler) classifySession(
	ctx context.Context,
	provider llm.Provider,
	model string,
	sess *session.Session,
	messages []session.Message,
	workspaces []*session.Workspace,
	agents []string,
) (*AutoClassifyResponse, error) {

	// Build context about available workspaces
	var workspaceInfo strings.Builder
	workspaceInfo.WriteString("Available workspaces:\n")
	if len(workspaces) == 0 {
		workspaceInfo.WriteString("- (none)\n")
	} else {
		for _, ws := range workspaces {
			desc := ws.Description
			if desc == "" {
				desc = "No description"
			}
			workspaceInfo.WriteString(fmt.Sprintf("- ID: %s, Name: %s, Description: %s\n", ws.ID, ws.Name, desc))
		}
	}

	// Build context about available agents
	var agentInfo strings.Builder
	agentInfo.WriteString("Available agents:\n")
	if len(agents) == 0 {
		agentInfo.WriteString("- (none)\n")
	} else {
		for _, agent := range agents {
			agentInfo.WriteString(fmt.Sprintf("- %s\n", agent))
		}
	}

	// Build conversation summary (limit to recent messages)
	var conversationSummary strings.Builder
	conversationSummary.WriteString("Recent conversation:\n")
	maxMessages := 10
	startIdx := 0
	if len(messages) > maxMessages {
		startIdx = len(messages) - maxMessages
	}
	for i := startIdx; i < len(messages); i++ {
		msg := messages[i]
		content := msg.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		conversationSummary.WriteString(fmt.Sprintf("%s: %s\n\n", msg.Role, content))
	}

	systemPrompt := `You are an AI assistant that classifies conversations into workspaces and suggests the best agent to handle them.

Based on the conversation content, determine:
1. Which workspace (if any) best fits this conversation topic
2. Which agent (if any) would be best suited to continue this conversation

You must respond with a valid JSON object (and nothing else) with these fields:
- workspace_id: The ID of the most relevant workspace, or empty string "" if no workspace fits well
- workspace_name: The name of the selected workspace (for display), or empty string if none
- agent_name: The name of the most suitable agent, or empty string "" if the current agent is fine
- reasoning: Brief explanation of your classification decision

Guidelines:
- Only suggest a workspace if the conversation clearly fits its purpose
- Only suggest a different agent if another agent would be significantly better suited
- If unsure, leave workspace_id and agent_name as empty strings
- Consider the conversation topic, user intent, and any domain-specific content

Example response:
{
  "workspace_id": "abc-123",
  "workspace_name": "Project Alpha",
  "agent_name": "research-agent",
  "reasoning": "The conversation is about research tasks related to Project Alpha."
}`

	userMessage := fmt.Sprintf(`%s

%s

%s

Current session agent: %s
Current session workspace: %s

Please analyze this conversation and suggest the best workspace and agent.`,
		workspaceInfo.String(),
		agentInfo.String(),
		conversationSummary.String(),
		sess.AgentName,
		sess.FolderID,
	)

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: "user", Content: userMessage},
		},
		SystemPrompt: systemPrompt,
		Temperature:  0.3,
		MaxTokens:    500,
	})

	if err != nil {
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}

	// Parse the JSON response
	var classification AutoClassifyResponse
	responseText := strings.TrimSpace(resp.Content)

	// Try to extract JSON if wrapped in markdown code blocks
	if strings.HasPrefix(responseText, "```") {
		lines := strings.Split(responseText, "\n")
		var jsonLines []string
		inJSON := false
		for _, line := range lines {
			if strings.HasPrefix(line, "```") {
				inJSON = !inJSON
				continue
			}
			if inJSON {
				jsonLines = append(jsonLines, line)
			}
		}
		responseText = strings.Join(jsonLines, "\n")
	}

	if err := json.Unmarshal([]byte(responseText), &classification); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response as JSON: %w (response: %s)", err, responseText)
	}

	// Validate the response
	classification = h.validateClassification(classification, workspaces, agents)

	return &classification, nil
}

// validateClassification ensures the classification values are valid
func (h *AutoClassifyHandler) validateClassification(
	classification AutoClassifyResponse,
	workspaces []*session.Workspace,
	agents []string,
) AutoClassifyResponse {

	// Validate workspace ID exists
	if classification.WorkspaceID != "" {
		found := false
		for _, ws := range workspaces {
			if ws.ID == classification.WorkspaceID {
				found = true
				classification.WorkspaceName = ws.Name
				break
			}
		}
		if !found {
			classification.WorkspaceID = ""
			classification.WorkspaceName = ""
		}
	}

	// Validate agent name exists
	if classification.AgentName != "" {
		found := false
		for _, agent := range agents {
			if agent == classification.AgentName {
				found = true
				break
			}
		}
		if !found {
			classification.AgentName = ""
		}
	}

	return classification
}
