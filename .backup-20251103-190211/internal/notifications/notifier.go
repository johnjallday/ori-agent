package notifications

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// NotificationType represents the type of notification
type NotificationType string

const (
	// Plugin health notifications
	NotifyPluginHealthy   NotificationType = "plugin_healthy"
	NotifyPluginDegraded  NotificationType = "plugin_degraded"
	NotifyPluginUnhealthy NotificationType = "plugin_unhealthy"

	// Update notifications
	NotifyUpdateAvailable NotificationType = "update_available"
	NotifyUpdateSuccess   NotificationType = "update_success"
	NotifyUpdateFailed    NotificationType = "update_failed"

	// Call notifications
	NotifyPluginError NotificationType = "plugin_error"
	NotifyHighFailureRate NotificationType = "high_failure_rate"
)

// Notification represents a notification event
type Notification struct {
	Type      NotificationType       `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	Title     string                 `json:"title"`
	Message   string                 `json:"message"`
	Severity  string                 `json:"severity"` // "info", "warning", "error"
	Data      map[string]interface{} `json:"data,omitempty"`
}

// Channel represents a notification delivery channel
type Channel interface {
	Send(notification Notification) error
	Name() string
}

// Notifier manages notification delivery
type Notifier struct {
	mu                sync.RWMutex
	channels          []Channel
	enabled           bool
	notificationQueue chan Notification
	stopChan          chan struct{}
	history           []Notification
	maxHistorySize    int
}

// NewNotifier creates a new notification manager
func NewNotifier() *Notifier {
	n := &Notifier{
		channels:          []Channel{},
		enabled:           true,
		notificationQueue: make(chan Notification, 100),
		stopChan:          make(chan struct{}),
		history:           make([]Notification, 0, 100),
		maxHistorySize:    100,
	}

	// Add default log channel
	n.AddChannel(NewLogChannel())

	// Start notification processor
	go n.processNotifications()

	return n
}

// AddChannel adds a notification channel
func (n *Notifier) AddChannel(channel Channel) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.channels = append(n.channels, channel)
	log.Printf("Added notification channel: %s", channel.Name())
}

// RemoveChannel removes a notification channel by name
func (n *Notifier) RemoveChannel(name string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	for i, ch := range n.channels {
		if ch.Name() == name {
			n.channels = append(n.channels[:i], n.channels[i+1:]...)
			log.Printf("Removed notification channel: %s", name)
			return
		}
	}
}

// Notify sends a notification to all channels
func (n *Notifier) Notify(notifType NotificationType, title, message, severity string, data map[string]interface{}) {
	if !n.enabled {
		return
	}

	notification := Notification{
		Type:      notifType,
		Timestamp: time.Now(),
		Title:     title,
		Message:   message,
		Severity:  severity,
		Data:      data,
	}

	select {
	case n.notificationQueue <- notification:
		// Queued successfully
	default:
		log.Printf("⚠️  Notification queue full, dropping notification: %s", title)
	}
}

// processNotifications processes queued notifications
func (n *Notifier) processNotifications() {
	for {
		select {
		case notification := <-n.notificationQueue:
			n.deliverNotification(notification)
		case <-n.stopChan:
			return
		}
	}
}

// deliverNotification delivers a notification to all channels
func (n *Notifier) deliverNotification(notification Notification) {
	n.mu.RLock()
	channels := make([]Channel, len(n.channels))
	copy(channels, n.channels)
	n.mu.RUnlock()

	// Add to history
	n.addToHistory(notification)

	// Send to all channels
	for _, channel := range channels {
		go func(ch Channel) {
			if err := ch.Send(notification); err != nil {
				log.Printf("Failed to send notification via %s: %v", ch.Name(), err)
			}
		}(channel)
	}
}

// addToHistory adds a notification to history
func (n *Notifier) addToHistory(notification Notification) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.history = append(n.history, notification)

	// Trim history if it exceeds max size
	if len(n.history) > n.maxHistorySize {
		n.history = n.history[len(n.history)-n.maxHistorySize:]
	}
}

// GetHistory returns notification history
func (n *Notifier) GetHistory(limit int) []Notification {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if limit <= 0 || limit > len(n.history) {
		limit = len(n.history)
	}

	result := make([]Notification, limit)
	copy(result, n.history[len(n.history)-limit:])

	// Reverse to get newest first
	for i := 0; i < len(result)/2; i++ {
		result[i], result[len(result)-1-i] = result[len(result)-1-i], result[i]
	}

	return result
}

// Enable enables notifications
func (n *Notifier) Enable() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.enabled = true
	log.Println("Notifications enabled")
}

// Disable disables notifications
func (n *Notifier) Disable() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.enabled = false
	log.Println("Notifications disabled")
}

// Stop stops the notifier
func (n *Notifier) Stop() {
	close(n.stopChan)
}

// LogChannel logs notifications to the console
type LogChannel struct{}

// NewLogChannel creates a new log channel
func NewLogChannel() *LogChannel {
	return &LogChannel{}
}

// Name returns the channel name
func (c *LogChannel) Name() string {
	return "log"
}

// Send sends a notification to the log
func (c *LogChannel) Send(notification Notification) error {
	icon := "📢"
	switch notification.Severity {
	case "error":
		icon = "🚨"
	case "warning":
		icon = "⚠️ "
	case "info":
		icon = "ℹ️ "
	}

	log.Printf("%s %s: %s - %s", icon, notification.Title, notification.Message, notification.Type)
	return nil
}

// WebhookChannel sends notifications to an HTTP webhook
type WebhookChannel struct {
	url    string
	client *http.Client
}

// NewWebhookChannel creates a new webhook channel
func NewWebhookChannel(url string) *WebhookChannel {
	return &WebhookChannel{
		url: url,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Name returns the channel name
func (c *WebhookChannel) Name() string {
	return fmt.Sprintf("webhook:%s", c.url)
}

// Send sends a notification to the webhook
func (c *WebhookChannel) Send(notification Notification) error {
	data, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	resp, err := c.client.Post(c.url, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to post to webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}

// FileChannel writes notifications to a file
type FileChannel struct {
	filePath string
}

// NewFileChannel creates a new file channel
func NewFileChannel(filePath string) *FileChannel {
	return &FileChannel{
		filePath: filePath,
	}
}

// Name returns the channel name
func (c *FileChannel) Name() string {
	return fmt.Sprintf("file:%s", c.filePath)
}

// Send writes a notification to the file
func (c *FileChannel) Send(notification Notification) error {
	// TODO: Implement file writing with proper locking and rotation
	// For now, this is a placeholder
	return nil
}
