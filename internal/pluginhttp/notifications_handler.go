package pluginhttp

import (
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
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
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
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
	orihttp.WriteJSON(w, response)
}

// HandleDismissNotification dismisses a specific notification
// POST /api/plugins/notifications/:id/dismiss
func (h *NotificationsHandler) HandleDismissNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if respErr := orihttp.RespondMethodNotAllowed(w); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	notificationID := h.extractNotificationID(r.URL.Path)
	if notificationID == "" {
		if respErr := orihttp.RespondBadRequest(w, "Notification ID required"); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	if err := h.NotificationManager.DismissNotification(notificationID); err != nil {
		if respErr := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to dismiss notification: %v", err)); respErr != nil {
			logger.Error("Failed to write response", logger.Fields{"error": respErr})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, map[string]interface{}{
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
