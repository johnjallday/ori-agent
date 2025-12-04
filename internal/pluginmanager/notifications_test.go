package pluginmanager

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewNotificationManager(t *testing.T) {
	nm := NewNotificationManager("")
	if nm == nil {
		t.Fatal("NewNotificationManager returned nil")
	}
	if nm.notifications == nil {
		t.Error("notifications slice not initialized")
	}
}

func TestNotificationManager_CreateNotification(t *testing.T) {
	nm := NewNotificationManager("")

	notif := Notification{
		Type:       NotificationTypePluginError,
		PluginName: "test-plugin",
		Message:    "Test error message",
	}

	err := nm.CreateNotification(notif)
	if err != nil {
		t.Errorf("CreateNotification() error = %v", err)
	}

	notifications := nm.GetNotifications()
	if len(notifications) != 1 {
		t.Errorf("Expected 1 notification, got %d", len(notifications))
	}

	created := notifications[0]
	if created.Type != NotificationTypePluginError {
		t.Errorf("Expected type %s, got %s", NotificationTypePluginError, created.Type)
	}
	if created.ID == "" {
		t.Error("Expected notification ID to be generated")
	}
	if created.Timestamp.IsZero() {
		t.Error("Expected timestamp to be set")
	}
}

func TestNotificationManager_GetNotifications(t *testing.T) {
	nm := NewNotificationManager("")

	// Create multiple notifications at different times
	notif1 := Notification{
		Type:       NotificationTypePluginError,
		PluginName: "plugin1",
		Message:    "Error 1",
		Timestamp:  time.Now().Add(-2 * time.Hour),
	}
	notif2 := Notification{
		Type:       NotificationTypeUpdateAvailable,
		PluginName: "plugin2",
		Message:    "Update available",
		Timestamp:  time.Now().Add(-1 * time.Hour),
	}
	notif3 := Notification{
		Type:       NotificationTypeHealthCheckFailed,
		PluginName: "plugin3",
		Message:    "Health check failed",
		Timestamp:  time.Now(),
	}

	nm.CreateNotification(notif1)
	nm.CreateNotification(notif2)
	nm.CreateNotification(notif3)

	notifications := nm.GetNotifications()

	if len(notifications) != 3 {
		t.Errorf("Expected 3 notifications, got %d", len(notifications))
	}

	// Verify sorted by newest first
	if !notifications[0].Timestamp.After(notifications[1].Timestamp) {
		t.Error("Notifications not sorted by newest first")
	}
	if !notifications[1].Timestamp.After(notifications[2].Timestamp) {
		t.Error("Notifications not sorted by newest first")
	}
}

func TestNotificationManager_GetUnreadNotifications(t *testing.T) {
	nm := NewNotificationManager("")

	// Create read and unread notifications
	notif1 := Notification{
		Type:       NotificationTypePluginError,
		PluginName: "plugin1",
		Message:    "Error 1",
		Read:       false,
	}
	notif2 := Notification{
		Type:       NotificationTypeUpdateAvailable,
		PluginName: "plugin2",
		Message:    "Update available",
		Read:       true,
	}
	notif3 := Notification{
		Type:       NotificationTypeHealthCheckFailed,
		PluginName: "plugin3",
		Message:    "Health check failed",
		Read:       false,
		Dismissed:  true,
	}

	nm.CreateNotification(notif1)
	nm.CreateNotification(notif2)
	nm.CreateNotification(notif3)

	unread := nm.GetUnreadNotifications()

	// Only notif1 should be unread and not dismissed
	if len(unread) != 1 {
		t.Errorf("Expected 1 unread notification, got %d", len(unread))
	}
	if unread[0].PluginName != "plugin1" {
		t.Errorf("Expected plugin1 notification to be unread, got %s", unread[0].PluginName)
	}
}

func TestNotificationManager_GetUnreadCount(t *testing.T) {
	nm := NewNotificationManager("")

	// Initially 0
	if count := nm.GetUnreadCount(); count != 0 {
		t.Errorf("Expected 0 unread, got %d", count)
	}

	// Add unread notification
	nm.CreateNotification(Notification{
		Type:       NotificationTypePluginError,
		PluginName: "plugin1",
		Message:    "Error",
		Read:       false,
	})

	if count := nm.GetUnreadCount(); count != 1 {
		t.Errorf("Expected 1 unread, got %d", count)
	}

	// Add read notification
	nm.CreateNotification(Notification{
		Type:       NotificationTypeUpdateAvailable,
		PluginName: "plugin2",
		Message:    "Update",
		Read:       true,
	})

	if count := nm.GetUnreadCount(); count != 1 {
		t.Errorf("Expected 1 unread (read notification should not count), got %d", count)
	}

	// Add dismissed notification
	nm.CreateNotification(Notification{
		Type:       NotificationTypeHealthCheckFailed,
		PluginName: "plugin3",
		Message:    "Health",
		Read:       false,
		Dismissed:  true,
	})

	if count := nm.GetUnreadCount(); count != 1 {
		t.Errorf("Expected 1 unread (dismissed notification should not count), got %d", count)
	}
}

func TestNotificationManager_MarkAsRead(t *testing.T) {
	nm := NewNotificationManager("")

	notif := Notification{
		ID:         "test-id",
		Type:       NotificationTypePluginError,
		PluginName: "plugin1",
		Message:    "Error",
		Read:       false,
	}
	nm.CreateNotification(notif)

	// Get the actual ID that was generated
	notifications := nm.GetNotifications()
	actualID := notifications[0].ID

	// Mark as read
	err := nm.MarkAsRead(actualID)
	if err != nil {
		t.Errorf("MarkAsRead() error = %v", err)
	}

	// Verify marked as read
	notifications = nm.GetNotifications()
	if !notifications[0].Read {
		t.Error("Expected notification to be marked as read")
	}

	// Test marking non-existent notification
	err = nm.MarkAsRead("nonexistent-id")
	if err == nil {
		t.Error("Expected error for non-existent notification")
	}
}

func TestNotificationManager_MarkAllAsRead(t *testing.T) {
	nm := NewNotificationManager("")

	// Create multiple unread notifications
	for i := 0; i < 3; i++ {
		nm.CreateNotification(Notification{
			Type:       NotificationTypePluginError,
			PluginName: "plugin",
			Message:    "Error",
			Read:       false,
		})
	}

	// Mark all as read
	err := nm.MarkAllAsRead()
	if err != nil {
		t.Errorf("MarkAllAsRead() error = %v", err)
	}

	// Verify all are read
	notifications := nm.GetNotifications()
	for _, notif := range notifications {
		if !notif.Read {
			t.Error("Expected all notifications to be marked as read")
		}
	}

	if count := nm.GetUnreadCount(); count != 0 {
		t.Errorf("Expected 0 unread after marking all as read, got %d", count)
	}
}

func TestNotificationManager_DismissNotification(t *testing.T) {
	nm := NewNotificationManager("")

	notif := Notification{
		Type:       NotificationTypePluginError,
		PluginName: "plugin1",
		Message:    "Error",
		Dismissed:  false,
	}
	nm.CreateNotification(notif)

	// Get the actual ID
	notifications := nm.GetNotifications()
	actualID := notifications[0].ID

	// Dismiss notification
	err := nm.DismissNotification(actualID)
	if err != nil {
		t.Errorf("DismissNotification() error = %v", err)
	}

	// Verify dismissed
	notifications = nm.GetNotifications()
	if !notifications[0].Dismissed {
		t.Error("Expected notification to be dismissed")
	}

	// Dismissed notifications should not count as unread
	if count := nm.GetUnreadCount(); count != 0 {
		t.Errorf("Expected 0 unread (dismissed), got %d", count)
	}
}

func TestNotificationManager_ClearDismissed(t *testing.T) {
	nm := NewNotificationManager("")

	// Create mix of dismissed and non-dismissed notifications
	nm.CreateNotification(Notification{
		Type:       NotificationTypePluginError,
		PluginName: "plugin1",
		Message:    "Error 1",
		Dismissed:  true,
	})
	nm.CreateNotification(Notification{
		Type:       NotificationTypeUpdateAvailable,
		PluginName: "plugin2",
		Message:    "Update",
		Dismissed:  false,
	})
	nm.CreateNotification(Notification{
		Type:       NotificationTypeHealthCheckFailed,
		PluginName: "plugin3",
		Message:    "Health",
		Dismissed:  true,
	})

	// Clear dismissed
	err := nm.ClearDismissed()
	if err != nil {
		t.Errorf("ClearDismissed() error = %v", err)
	}

	// Should only have 1 notification left
	notifications := nm.GetNotifications()
	if len(notifications) != 1 {
		t.Errorf("Expected 1 notification after clearing dismissed, got %d", len(notifications))
	}
	if notifications[0].PluginName != "plugin2" {
		t.Error("Wrong notification remained after clearing dismissed")
	}
}

func TestNotificationManager_ClearAll(t *testing.T) {
	nm := NewNotificationManager("")

	// Create some notifications
	for i := 0; i < 5; i++ {
		nm.CreateNotification(Notification{
			Type:       NotificationTypePluginError,
			PluginName: "plugin",
			Message:    "Error",
		})
	}

	// Clear all
	err := nm.ClearAll()
	if err != nil {
		t.Errorf("ClearAll() error = %v", err)
	}

	// Verify all cleared
	notifications := nm.GetNotifications()
	if len(notifications) != 0 {
		t.Errorf("Expected 0 notifications after ClearAll, got %d", len(notifications))
	}
}

func TestNotificationManager_GetNotificationsByPlugin(t *testing.T) {
	nm := NewNotificationManager("")

	// Create notifications for different plugins
	nm.CreateNotification(Notification{
		Type:       NotificationTypePluginError,
		PluginName: "plugin1",
		Message:    "Error 1",
	})
	nm.CreateNotification(Notification{
		Type:       NotificationTypeUpdateAvailable,
		PluginName: "plugin2",
		Message:    "Update",
	})
	nm.CreateNotification(Notification{
		Type:       NotificationTypeHealthCheckFailed,
		PluginName: "plugin1",
		Message:    "Health",
	})

	// Get notifications for plugin1
	plugin1Notifs := nm.GetNotificationsByPlugin("plugin1")
	if len(plugin1Notifs) != 2 {
		t.Errorf("Expected 2 notifications for plugin1, got %d", len(plugin1Notifs))
	}

	// Get notifications for plugin2
	plugin2Notifs := nm.GetNotificationsByPlugin("plugin2")
	if len(plugin2Notifs) != 1 {
		t.Errorf("Expected 1 notification for plugin2, got %d", len(plugin2Notifs))
	}

	// Get notifications for non-existent plugin
	nonexistent := nm.GetNotificationsByPlugin("nonexistent")
	if len(nonexistent) != 0 {
		t.Errorf("Expected 0 notifications for nonexistent plugin, got %d", len(nonexistent))
	}
}

func TestNotificationManager_GetNotificationsByType(t *testing.T) {
	nm := NewNotificationManager("")

	// Create notifications of different types
	nm.CreateNotification(Notification{
		Type:       NotificationTypePluginError,
		PluginName: "plugin1",
		Message:    "Error",
	})
	nm.CreateNotification(Notification{
		Type:       NotificationTypePluginError,
		PluginName: "plugin2",
		Message:    "Another error",
	})
	nm.CreateNotification(Notification{
		Type:       NotificationTypeUpdateAvailable,
		PluginName: "plugin3",
		Message:    "Update",
	})

	// Get error notifications
	errors := nm.GetNotificationsByType(NotificationTypePluginError)
	if len(errors) != 2 {
		t.Errorf("Expected 2 error notifications, got %d", len(errors))
	}

	// Get update notifications
	updates := nm.GetNotificationsByType(NotificationTypeUpdateAvailable)
	if len(updates) != 1 {
		t.Errorf("Expected 1 update notification, got %d", len(updates))
	}
}

func TestNotificationManager_MaxHistory(t *testing.T) {
	nm := NewNotificationManager("")

	// Create more than MaxNotificationHistory notifications
	for i := 0; i < MaxNotificationHistory+10; i++ {
		nm.CreateNotification(Notification{
			Type:       NotificationTypePluginError,
			PluginName: "plugin",
			Message:    "Error",
		})
		time.Sleep(time.Millisecond) // Ensure different timestamps
	}

	// Should only keep MaxNotificationHistory
	notifications := nm.GetNotifications()
	if len(notifications) != MaxNotificationHistory {
		t.Errorf("Expected %d notifications (max history), got %d", MaxNotificationHistory, len(notifications))
	}
}

func TestNotificationManager_FileIO(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "notifications-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	storagePath := filepath.Join(tmpDir, "notifications.json")
	nm := NewNotificationManager(storagePath)

	// Create some notifications
	nm.CreateNotification(Notification{
		Type:       NotificationTypePluginError,
		PluginName: "plugin1",
		Message:    "Error",
	})
	nm.CreateNotification(Notification{
		Type:       NotificationTypeUpdateAvailable,
		PluginName: "plugin2",
		Message:    "Update",
	})

	// Create new manager and load from file
	nm2 := NewNotificationManager(storagePath)
	if err := nm2.LoadFromFile(); err != nil {
		t.Errorf("LoadFromFile() error = %v", err)
	}

	// Verify data was loaded
	notifications := nm2.GetNotifications()
	if len(notifications) != 2 {
		t.Errorf("Expected 2 notifications after loading, got %d", len(notifications))
	}
}

func TestCreatePluginErrorNotification(t *testing.T) {
	notif := CreatePluginErrorNotification("test-plugin", "test error")

	if notif.Type != NotificationTypePluginError {
		t.Errorf("Expected type %s, got %s", NotificationTypePluginError, notif.Type)
	}
	if notif.PluginName != "test-plugin" {
		t.Errorf("Expected plugin name 'test-plugin', got %s", notif.PluginName)
	}
	if notif.Message == "" {
		t.Error("Expected message to be set")
	}
	if notif.Metadata["error"] != "test error" {
		t.Error("Expected error in metadata")
	}
}

func TestCreateUpdateAvailableNotification(t *testing.T) {
	notif := CreateUpdateAvailableNotification("test-plugin", "1.0.0", "2.0.0")

	if notif.Type != NotificationTypeUpdateAvailable {
		t.Errorf("Expected type %s, got %s", NotificationTypeUpdateAvailable, notif.Type)
	}
	if notif.PluginName != "test-plugin" {
		t.Errorf("Expected plugin name 'test-plugin', got %s", notif.PluginName)
	}
	if notif.Metadata["current_version"] != "1.0.0" {
		t.Error("Expected current_version in metadata")
	}
	if notif.Metadata["new_version"] != "2.0.0" {
		t.Error("Expected new_version in metadata")
	}
}

func TestCreateHealthCheckFailedNotification(t *testing.T) {
	notif := CreateHealthCheckFailedNotification("test-plugin", "connection timeout")

	if notif.Type != NotificationTypeHealthCheckFailed {
		t.Errorf("Expected type %s, got %s", NotificationTypeHealthCheckFailed, notif.Type)
	}
	if notif.Metadata["reason"] != "connection timeout" {
		t.Error("Expected reason in metadata")
	}
}

func TestCreatePermissionRequiredNotification(t *testing.T) {
	perms := []string{"file_access", "network_access"}
	notif := CreatePermissionRequiredNotification("test-plugin", perms)

	if notif.Type != NotificationTypePermissionRequired {
		t.Errorf("Expected type %s, got %s", NotificationTypePermissionRequired, notif.Type)
	}
	metadataPerms, ok := notif.Metadata["permissions"].([]string)
	if !ok {
		t.Error("Expected permissions in metadata")
	}
	if len(metadataPerms) != 2 {
		t.Errorf("Expected 2 permissions in metadata, got %d", len(metadataPerms))
	}
}
