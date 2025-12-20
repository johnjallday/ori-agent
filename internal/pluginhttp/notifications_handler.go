package pluginhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/pluginmanager"
)
import

// NotificationsHandler handles plugin notification operations
"github.com/johnjallday/ori-agent/internal/logger"

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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	notificationID := h.extractNotificationID(r.URL.Path)
	if notificationID == "" {
		if err := orihttp.RespondBadRequest(w, "Notification ID required"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.NotificationManager.DismissNotification(notificationID); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to dismiss notification: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
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
