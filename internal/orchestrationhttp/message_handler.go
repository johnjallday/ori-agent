package orchestrationhttp

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
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
		http.Error(w, "workspace_id parameter required", http.StatusBadRequest)
		return
	}

	// Get workspace
	ws, err := mh.workspaceStore.Get(wsID)
	if err != nil {
		log.Printf("❌ Error getting workspace %s: %v", wsID, err)
		http.Error(w, err.Error(), http.StatusNotFound)
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
			http.Error(w, "Invalid since timestamp format (use RFC3339)", http.StatusBadRequest)
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

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"messages": messages,
		"count":    len(messages),
	})
}

// handleSendMessage sends a message to workspace
func (mh *MessageHandler) handleSendMessage(w http.ResponseWriter, r *http.Request, ws *agentstudio.Workspace) {
	var msg agentstudio.AgentMessage

	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		log.Printf("❌ Error decoding message: %v", err)
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if msg.From == "" {
		http.Error(w, "from field is required", http.StatusBadRequest)
		return
	}
	if msg.Content == "" {
		http.Error(w, "content field is required", http.StatusBadRequest)
		return
	}

	// Add message to workspace
	if err := ws.AddMessage(msg); err != nil {
		log.Printf("❌ Error adding message to workspace: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Save updated workspace
	if err := mh.workspaceStore.Save(ws); err != nil {
		log.Printf("❌ Error saving workspace after adding message: %v", err)
		http.Error(w, "Failed to save workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Added message from %s to workspace %s", msg.From, ws.ID)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"message_id": msg.ID,
		"timestamp":  msg.Timestamp,
	})
}
