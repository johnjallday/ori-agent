package pluginhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/pluginmanager"
)

// NotificationsHandler handles plugin notification operations
type NotificationsHandler struct {
	NotificationManager *pluginmanager.NotificationManager
}

// NewNotificationsHandler creates a new notifications handler
func NewNotificationsHandler(notifMgr *pluginmanager.NotificationManager) *NotificationsHandler {
	return &NotificationsHandler{
		NotificationManager: notifMgr,
	}
}

// HandleGetNotifications returns all plugin notifications
// GET /api/plugins/notifications
func (h *NotificationsHandler) HandleGetNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	notifications := h.NotificationManager.GetNotifications()
	unreadCount := h.NotificationManager.GetUnreadCount()

	response := map[string]interface{}{
		"notifications": notifications,
		"unread_count":  unreadCount,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// HandleDismissNotification dismisses a specific notification
// POST /api/plugins/notifications/:id/dismiss
func (h *NotificationsHandler) HandleDismissNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	notificationID := h.extractNotificationID(r.URL.Path)
	if notificationID == "" {
		http.Error(w, "Notification ID required", http.StatusBadRequest)
		return
	}

	if err := h.NotificationManager.DismissNotification(notificationID); err != nil {
		http.Error(w, fmt.Sprintf("Failed to dismiss notification: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Notification dismissed successfully",
	})
}

func (h *NotificationsHandler) extractNotificationID(path string) string {
	// Path format: /api/plugins/notifications/:id/dismiss
	// Remove /api/plugins/notifications/ prefix
	remaining := strings.TrimPrefix(path, "/api/plugins/notifications/")

	// Split by / and take first component (notification ID)
	parts := strings.Split(remaining, "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}
