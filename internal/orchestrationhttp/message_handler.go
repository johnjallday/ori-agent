package orchestrationhttp

import (
	"net/http"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// MessageHandler manages message-related operations for workspaces
type MessageHandler struct {
	workspaceStore workspace.Store
	eventBus       *workspace.EventBus
}

// NewMessageHandler creates a new message handler
func NewMessageHandler(workspaceStore workspace.Store, eventBus *workspace.EventBus) *MessageHandler {
	return &MessageHandler{
		workspaceStore: workspaceStore,
		eventBus:       eventBus,
	}
}

// MessagesHandler handles workspace message operations
// GET: Retrieve messages from workspace (with optional filters: agent, since)
// POST: Send message to workspace
func (mh *MessageHandler) MessagesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	wsID := r.URL.Query().Get("workspace_id")
	if wsID == "" {
		orihttp.BadRequest(w, "workspace_id parameter required")
		return
	}

	// Get workspace
	ws, err := mh.workspaceStore.Get(wsID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": wsID, "error": err})
		orihttp.NotFound(w, err.Error())
		return
	}

	switch r.Method {
	case http.MethodGet:
		mh.handleGetMessages(w, r, ws)
	case http.MethodPost:
		mh.handleSendMessage(w, r, ws)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGetMessages retrieves messages from workspace
func (mh *MessageHandler) handleGetMessages(w http.ResponseWriter, r *http.Request, ws *workspace.Workspace) {
	agentName := r.URL.Query().Get("agent")
	sinceStr := r.URL.Query().Get("since")

	var messages []workspace.AgentMessage

	if sinceStr != "" {
		// Get messages since timestamp
		since, err := time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			orihttp.BadRequest(w, "Invalid since timestamp format (use RFC3339)")
			return
		}
		messages = ws.GetMessagesSince(since)
	} else if agentName != "" {
		// Get messages for specific agent
		messages = ws.GetMessagesForAgent(agentName)
	} else {
		// Get all messages (direct field access through getter method)
		messages = ws.GetMessagesSince(time.Time{}) // epoch time returns all messages
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"messages": messages,
		"count":    len(messages),
	})
}

// handleSendMessage sends a message to workspace
func (mh *MessageHandler) handleSendMessage(w http.ResponseWriter, r *http.Request, ws *workspace.Workspace) {
	var msg workspace.AgentMessage

	if !orihttp.ParseJSONBody(w, r, &msg) {
		return
	}

	// Validate required fields
	if msg.From == "" {
		orihttp.BadRequest(w, "from field is required")
		return
	}
	if msg.Content == "" {
		orihttp.BadRequest(w, "content field is required")
		return
	}

	// Add message to workspace
	if err := ws.AddMessage(msg); err != nil {
		logger.Error("Error adding message to workspace", logger.Fields{"error": err})
		orihttp.BadRequest(w, err.Error())
		return
	}

	// Save updated workspace
	if err := mh.workspaceStore.Save(ws); err != nil {
		logger.Error("Error saving workspace after adding message", logger.Fields{"error": err})
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to save workspace", err)
		return
	}

	logger.Info("Added message from agent to workspace", logger.Fields{"from": msg.From, "workspace_id": ws.ID})

	w.WriteHeader(http.StatusOK)
	orihttp.WriteJSON(w, map[string]interface{}{
		"success":    true,
		"message_id": msg.ID,
		"timestamp":  msg.Timestamp,
	})
}
