package pluginmanager

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// NotificationType represents the type of notification.
type NotificationType string

const (
	NotificationTypePluginError        NotificationType = "plugin_error"
	NotificationTypeUpdateAvailable    NotificationType = "update_available"
	NotificationTypeHealthCheckFailed  NotificationType = "health_check_failed"
	NotificationTypePermissionRequired NotificationType = "permission_required"
)

// Notification represents a plugin-related notification.
type Notification struct {
	ID         string           `json:"id"`
	Type       NotificationType `json:"type"`
	PluginName string           `json:"plugin_name"`
	Message    string           `json:"message"`
	Timestamp  time.Time        `json:"timestamp"`
	Read       bool             `json:"read"`
	Dismissed  bool             `json:"dismissed"`
	// Additional metadata (optional)
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

const (
	// MaxNotificationHistory is the maximum number of notifications to keep
	MaxNotificationHistory = 100
)

// NotificationManager manages plugin-related notifications.
type NotificationManager struct {
	mu            sync.RWMutex
	notifications []Notification
	storagePath   string // path to persist notifications
}

// NewNotificationManager creates a new NotificationManager instance.
func NewNotificationManager(storagePath string) *NotificationManager {
	return &NotificationManager{
		notifications: make([]Notification, 0),
		storagePath:   storagePath,
	}
}

// CreateNotification creates a new notification.
func (nm *NotificationManager) CreateNotification(notif Notification) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	// Generate ID if not provided
	if notif.ID == "" {
		notif.ID = uuid.New().String()
	}

	// Set timestamp if not provided
	if notif.Timestamp.IsZero() {
		notif.Timestamp = time.Now()
	}

	// Add to notifications list
	nm.notifications = append(nm.notifications, notif)

	// Clean up old notifications if we exceed the limit
	if len(nm.notifications) > MaxNotificationHistory {
		nm.notifications = nm.notifications[len(nm.notifications)-MaxNotificationHistory:]
	}

	// Persist to disk
	return nm.save()
}

// GetNotifications retrieves all notifications.
// Returns notifications sorted by timestamp (newest first).
func (nm *NotificationManager) GetNotifications() []Notification {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	// Return a copy, sorted by timestamp (newest first)
	result := make([]Notification, len(nm.notifications))
	copy(result, nm.notifications)

	// Sort by timestamp descending (newest first)
	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].Timestamp.Before(result[j].Timestamp) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result
}

// GetUnreadNotifications retrieves all unread notifications.
func (nm *NotificationManager) GetUnreadNotifications() []Notification {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	var unread []Notification
	for _, notif := range nm.notifications {
		if !notif.Read && !notif.Dismissed {
			unread = append(unread, notif)
		}
	}

	return unread
}

// GetUnreadCount returns the count of unread, non-dismissed notifications.
func (nm *NotificationManager) GetUnreadCount() int {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	count := 0
	for _, notif := range nm.notifications {
		if !notif.Read && !notif.Dismissed {
			count++
		}
	}
	return count
}

// MarkAsRead marks a notification as read.
func (nm *NotificationManager) MarkAsRead(notificationID string) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	for i := range nm.notifications {
		if nm.notifications[i].ID == notificationID {
			nm.notifications[i].Read = true
			return nm.save()
		}
	}

	return fmt.Errorf("notification not found: %s", notificationID)
}

// MarkAllAsRead marks all notifications as read.
func (nm *NotificationManager) MarkAllAsRead() error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	for i := range nm.notifications {
		nm.notifications[i].Read = true
	}

	return nm.save()
}

// DismissNotification dismisses a notification.
func (nm *NotificationManager) DismissNotification(notificationID string) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	for i := range nm.notifications {
		if nm.notifications[i].ID == notificationID {
			nm.notifications[i].Dismissed = true
			return nm.save()
		}
	}

	return fmt.Errorf("notification not found: %s", notificationID)
}

// ClearDismissed removes all dismissed notifications.
func (nm *NotificationManager) ClearDismissed() error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	kept := make([]Notification, 0, len(nm.notifications))
	for _, notif := range nm.notifications {
		if !notif.Dismissed {
			kept = append(kept, notif)
		}
	}

	nm.notifications = kept
	return nm.save()
}

// ClearAll removes all notifications.
func (nm *NotificationManager) ClearAll() error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	nm.notifications = make([]Notification, 0)
	return nm.save()
}

// GetNotificationsByPlugin retrieves all notifications for a specific plugin.
func (nm *NotificationManager) GetNotificationsByPlugin(pluginName string) []Notification {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	var result []Notification
	for _, notif := range nm.notifications {
		if notif.PluginName == pluginName {
			result = append(result, notif)
		}
	}

	return result
}

// GetNotificationsByType retrieves all notifications of a specific type.
func (nm *NotificationManager) GetNotificationsByType(notifType NotificationType) []Notification {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	var result []Notification
	for _, notif := range nm.notifications {
		if notif.Type == notifType {
			result = append(result, notif)
		}
	}

	return result
}

// LoadFromFile loads notifications from the storage file.
func (nm *NotificationManager) LoadFromFile() error {
	if nm.storagePath == "" {
		return nil // No storage configured
	}

	data, err := os.ReadFile(nm.storagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist yet, that's okay
		}
		return fmt.Errorf("failed to read notifications file: %w", err)
	}

	var notifications []Notification
	if err := json.Unmarshal(data, &notifications); err != nil {
		return fmt.Errorf("failed to unmarshal notifications: %w", err)
	}

	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.notifications = notifications

	return nil
}

// save persists notifications to the storage file.
// This is an internal method and assumes the caller holds the lock.
func (nm *NotificationManager) save() error {
	if nm.storagePath == "" {
		return nil // No storage configured
	}

	data, err := json.MarshalIndent(nm.notifications, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal notifications: %w", err)
	}

	if err := os.WriteFile(nm.storagePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write notifications file: %w", err)
	}

	return nil
}

// Helper functions for creating specific notification types

// CreatePluginErrorNotification creates a notification for a plugin error.
func CreatePluginErrorNotification(pluginName, errorMsg string) Notification {
	return Notification{
		Type:       NotificationTypePluginError,
		PluginName: pluginName,
		Message:    fmt.Sprintf("Plugin '%s' encountered an error: %s", pluginName, errorMsg),
		Metadata: map[string]interface{}{
			"error": errorMsg,
		},
	}
}

// CreateUpdateAvailableNotification creates a notification for an available update.
func CreateUpdateAvailableNotification(pluginName, currentVersion, newVersion string) Notification {
	return Notification{
		Type:       NotificationTypeUpdateAvailable,
		PluginName: pluginName,
		Message:    fmt.Sprintf("Update available for '%s': %s → %s", pluginName, currentVersion, newVersion),
		Metadata: map[string]interface{}{
			"current_version": currentVersion,
			"new_version":     newVersion,
		},
	}
}

// CreateHealthCheckFailedNotification creates a notification for a failed health check.
func CreateHealthCheckFailedNotification(pluginName, reason string) Notification {
	return Notification{
		Type:       NotificationTypeHealthCheckFailed,
		PluginName: pluginName,
		Message:    fmt.Sprintf("Health check failed for '%s': %s", pluginName, reason),
		Metadata: map[string]interface{}{
			"reason": reason,
		},
	}
}

// CreatePermissionRequiredNotification creates a notification for required permissions.
func CreatePermissionRequiredNotification(pluginName string, permissions []string) Notification {
	return Notification{
		Type:       NotificationTypePermissionRequired,
		PluginName: pluginName,
		Message:    fmt.Sprintf("Plugin '%s' requires permission approval: %v", pluginName, permissions),
		Metadata: map[string]interface{}{
			"permissions": permissions,
		},
	}
}
