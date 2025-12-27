package orchestrationhttp

import (
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// MessageHandler manages message-related operations for workspaces
type MessageHandler struct {
	workspaceStore agentstudio.Store
	eventBus       *agentstudio.EventBus
}

// NewMessageHandler creates a new message handler
func NewMessageHandler(workspaceStore agentstudio.Store, eventBus *agentstudio.EventBus) *MessageHandler {
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

	wsID := r.URL.Query().Get("studio_id")
	if wsID == "" {
		if respErr := orihttp.RespondBadRequest(w, "workspace_id parameter required"); respErr != nil {
			logger.

				// Get workspace
				Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	ws, err := mh.workspaceStore.Get(wsID)
	if err != nil {
		logger.Error("Error getting workspace", logger.Fields{"workspace_id": wsID, "error": err})
		if respErr := orihttp.RespondNotFound(w, err.Error()); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
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
func (mh *MessageHandler) handleGetMessages(w http.ResponseWriter, r *http.Request, ws *agentstudio.Workspace) {
	agentName := r.URL.Query().Get("agent")
	sinceStr := r.URL.Query().Get("since")

	var messages []agentstudio.AgentMessage

	if sinceStr != "" {
		// Get messages since timestamp
		since, err := time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			if respErr := orihttp.RespondBadRequest(w, "Invalid since timestamp format (use RFC3339)"); respErr != nil {
				logger.Error("Failed to write response", logger.Fields{"error": respErr})
			}
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
func (mh *MessageHandler) handleSendMessage(w http.ResponseWriter, r *http.Request, ws *agentstudio.Workspace) {
	var msg agentstudio.AgentMessage

	if !orihttp.ParseJSONBody(w, r, &msg) {
		return
	}

	// Validate required fields
	if msg.From == "" {
		if respErr := orihttp.RespondBadRequest(w, "from field is required"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}
	if msg.Content == "" {
		if respErr := orihttp.RespondBadRequest(w, "content field is required"); respErr != nil {
			logger.

				// Add message to workspace
				Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	if err := ws.AddMessage(msg); err != nil {
		logger.Error("Error adding message to workspace", logger.Fields{"error": err})
		if respErr := orihttp.RespondBadRequest(w, err.Error()); respErr != nil {
			logger.

				// Save updated workspace
				Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

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
