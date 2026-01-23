package orchestrationhttp

import (
	"encoding/json"
	"fmt"

	"net/http"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// NotificationHandler manages notification and event history operations
type NotificationHandler struct {
	workspaceStore      workspace.Store
	notificationService *workspace.NotificationService
	eventBus            *workspace.EventBus
}

// NewNotificationHandler creates a new notification handler
func NewNotificationHandler(workspaceStore workspace.Store,
	notificationService *workspace.NotificationService,
	eventBus *workspace.EventBus) *NotificationHandler {
	return &NotificationHandler{
		workspaceStore:      workspaceStore,
		notificationService: notificationService,
		eventBus:            eventBus,
	}
}

// NotificationsHandler handles notification operations
// GET: Retrieve notifications for an agent
// POST: Mark notification(s) as read
func (nh *NotificationHandler) NotificationsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if nh.notificationService == nil {
		orihttp.ServiceUnavailable(w, "notification service not initialized")
		return
	}

	switch r.Method {
	case http.MethodGet:
		nh.handleGetNotifications(w, r)
	case http.MethodPost:
		nh.handleMarkNotificationsRead(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGetNotifications retrieves notifications
func (nh *NotificationHandler) handleGetNotifications(w http.ResponseWriter, r *http.Request) {
	agentName := r.URL.Query().Get("agent")
	unreadOnly := r.URL.Query().Get("unread") == "true"

	if agentName != "" && unreadOnly {
		// Get unread notifications for agent
		notifications := nh.notificationService.GetUnreadForAgent(agentName)
		orihttp.WriteJSON(w, map[string]interface{}{
			"notifications": notifications,
			"count":         len(notifications),
		})
		return
	}

	// Get notification history
	limit := 50
	notifications := nh.notificationService.GetHistory(limit)

	orihttp.WriteJSON(w, map[string]interface{}{
		"notifications": notifications,
		"count":         len(notifications),
	})
}

// handleMarkNotificationsRead marks notifications as read
func (nh *NotificationHandler) handleMarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NotificationID string `json:"notification_id,omitempty"`
		AgentName      string `json:"agent_name,omitempty"`
		MarkAll        bool   `json:"mark_all,omitempty"`
	}

	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	// Mark all notifications for agent as read

	if req.MarkAll && req.AgentName != "" {

		nh.notificationService.MarkAllAsRead(req.AgentName)
		orihttp.WriteJSON(w, map[string]interface{}{
			"success": true,
			"message": "All notifications marked as read",
		})
		return
	}

	if req.NotificationID != "" {
		// Mark specific notification as read
		nh.notificationService.MarkAsRead(req.NotificationID)
		orihttp.WriteJSON(w, map[string]interface{}{
			"success": true,
			"message": "Notification marked as read",
		})
		return
	}
	orihttp.BadRequest(w, "notification_id or agent_name with mark_all required")
}

// NotificationStreamHandler streams notifications using Server-Sent Events (SSE)
// GET /api/orchestration/notifications/stream?agent=<name>

func (nh *NotificationHandler) NotificationStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	if nh.notificationService == nil {
		orihttp.ServiceUnavailable(w, "notification service not initialized")
		return
	}

	agentName := r.URL.Query().Get("agent")
	if agentName == "" {
		orihttp.BadRequest(w, "agent parameter required")
		// Set headers for SSE
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		orihttp.InternalError(w, "streaming not supported")
		// Subscribe to notifications
		return
	}

	notifChan := nh.notificationService.Subscribe(agentName)
	defer nh.notificationService.Unsubscribe(agentName)

	// Context with cancellation
	ctx := r.Context()

	logger.Debug("🔔 Starting notification stream for agent", logger.Fields{"agent": agentName})

	// Send initial unread notifications
	unread := nh.notificationService.GetUnreadForAgent(agentName)
	if len(unread) > 0 {
		data, _ := json.Marshal(map[string]interface{}{
			"notifications": unread,
			"count":         len(unread),
		})
		_, _ = fmt.Fprintf(w, "event: initial\ndata: %s\n\n", data)
		flusher.Flush()
	}

	// Stream notifications
	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			logger.Debug("🔕 Notification stream closed for agent", logger.Fields{"agent": agentName})
			return

		case notification, ok := <-notifChan:
			if !ok {
				// Channel closed
				logger.Debug("🔕 Notification channel closed for agent", logger.Fields{"agent": agentName})
				return
			}

			// Send notification to client
			data, err := json.Marshal(notification)
			if err != nil {
				logger.Error("Failed to marshal notification", logger.Fields{"err": err})
				continue
			}

			_, err = fmt.Fprintf(w, "event: notification\ndata: %s\n\n", data)
			if err != nil {
				logger.Error("Failed to write notification", logger.Fields{"err": err})
				return
			}
			flusher.Flush()
		}
	}
}

// EventHistoryHandler retrieves event history
// GET /api/orchestration/events?workspace_id=<id>&limit=<n>&since=<timestamp>
func (nh *NotificationHandler) EventHistoryHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	if nh.eventBus == nil {
		orihttp.ServiceUnavailable(w, "event bus not initialized")
		return
	}

	workspaceID := r.URL.Query().Get("studio_id")
	sinceStr := r.URL.Query().Get("since")
	limit := 100 // Default limit

	var events []workspace.Event

	if sinceStr != "" {
		// Get events since timestamp
		since, err := time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			orihttp.BadRequest(w, "invalid since timestamp (use RFC3339)")
			return
		}
		events = nh.eventBus.GetEventsSince(since, limit)
	} else if workspaceID != "" {
		// Get events for workspace
		events = nh.eventBus.GetWorkspaceHistory(workspaceID, limit)
	} else {
		// Get general event history
		events = nh.eventBus.GetHistory(nil, limit)
	}

	orihttp.WriteJSON(w, map[string]interface{}{
		"events": events,
		"count":  len(events),
	})
}
