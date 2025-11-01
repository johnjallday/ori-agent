package workspace

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// NotificationPriority represents the priority of a notification
type NotificationPriority int

const (
	PriorityLow NotificationPriority = iota
	PriorityMedium
	PriorityHigh
	PriorityCritical
)

// Notification represents a user-facing notification
type Notification struct {
	ID          string                 `json:"id"`
	Title       string                 `json:"title"`
	Message     string                 `json:"message"`
	Priority    NotificationPriority   `json:"priority"`
	WorkspaceID string                 `json:"workspace_id,omitempty"`
	AgentName   string                 `json:"agent_name,omitempty"`
	Timestamp   time.Time              `json:"timestamp"`
	Read        bool                   `json:"read"`
	ActionURL   string                 `json:"action_url,omitempty"`
	Data        map[string]interface{} `json:"data,omitempty"`
}

// NotificationChannel is a channel for delivering notifications
type NotificationChannel chan Notification

// NotificationService manages notifications and their delivery
type NotificationService struct {
	eventBus     *EventBus
	mu           sync.RWMutex
	channels     map[string]NotificationChannel // agent_name -> channel
	history      []Notification                  // Recent notifications
	historySize  int
	historyIndex int
	subID        string // Event bus subscription ID
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewNotificationService creates a new notification service
func NewNotificationService(eventBus *EventBus, historySize int) *NotificationService {
	ctx, cancel := context.WithCancel(context.Background())

	ns := &NotificationService{
		eventBus:     eventBus,
		channels:     make(map[string]NotificationChannel),
		history:      make([]Notification, historySize),
		historySize:  historySize,
		historyIndex: 0,
		ctx:          ctx,
		cancel:       cancel,
	}

	// Subscribe to all events to generate notifications
	ns.subID = eventBus.Subscribe(ns.handleEvent, nil)

	return ns
}

// handleEvent processes events and generates notifications
func (ns *NotificationService) handleEvent(event Event) {
	notification := ns.eventToNotification(event)
	if notification != nil {
		ns.addToHistory(*notification)
		ns.broadcast(*notification)
	}
}

// eventToNotification converts an event to a notification
func (ns *NotificationService) eventToNotification(event Event) *Notification {
	var title, message string
	var priority NotificationPriority

	switch event.Type {
	case EventTaskCreated:
		title = "New Task Assigned"
		message = fmt.Sprintf("A new task has been created in workspace %s", event.WorkspaceID)
		priority = PriorityMedium
	case EventTaskCompleted:
		title = "Task Completed"
		message = fmt.Sprintf("Task completed successfully in workspace %s", event.WorkspaceID)
		priority = PriorityMedium
	case EventTaskFailed:
		title = "Task Failed"
		message = fmt.Sprintf("Task failed in workspace %s", event.WorkspaceID)
		priority = PriorityHigh
	case EventTaskTimeout:
		title = "Task Timeout"
		message = fmt.Sprintf("Task exceeded timeout in workspace %s", event.WorkspaceID)
		priority = PriorityHigh
	case EventWorkspaceCreated:
		title = "Workspace Created"
		message = fmt.Sprintf("New workspace created: %s", event.WorkspaceID)
		priority = PriorityLow
	case EventWorkspaceCompleted:
		title = "Workspace Completed"
		message = fmt.Sprintf("Workspace completed: %s", event.WorkspaceID)
		priority = PriorityMedium
	case EventWorkflowStarted:
		title = "Workflow Started"
		message = fmt.Sprintf("Workflow execution started in workspace %s", event.WorkspaceID)
		priority = PriorityMedium
	case EventWorkflowCompleted:
		title = "Workflow Completed"
		message = fmt.Sprintf("Workflow completed successfully in workspace %s", event.WorkspaceID)
		priority = PriorityMedium
	case EventWorkflowFailed:
		title = "Workflow Failed"
		message = fmt.Sprintf("Workflow failed in workspace %s", event.WorkspaceID)
		priority = PriorityCritical
	case EventStepFailed:
		title = "Workflow Step Failed"
		message = fmt.Sprintf("A workflow step failed in workspace %s", event.WorkspaceID)
		priority = PriorityHigh
	case EventError:
		title = "Error Occurred"
		if msg, ok := event.Data["message"].(string); ok {
			message = msg
		} else {
			message = "An error occurred in the workspace"
		}
		priority = PriorityCritical
	case EventWarning:
		title = "Warning"
		if msg, ok := event.Data["message"].(string); ok {
			message = msg
		} else {
			message = "A warning was generated"
		}
		priority = PriorityMedium
	default:
		// Don't create notifications for all events
		return nil
	}

	notification := &Notification{
		ID:          generateNotificationID(),
		Title:       title,
		Message:     message,
		Priority:    priority,
		WorkspaceID: event.WorkspaceID,
		Timestamp:   event.Timestamp,
		Read:        false,
		Data:        event.Data,
	}

	// Extract agent name if available
	if agent, ok := event.Data["agent"].(string); ok {
		notification.AgentName = agent
	}

	// Set action URL for workspace-related notifications
	if event.WorkspaceID != "" {
		notification.ActionURL = fmt.Sprintf("/workspaces?id=%s", event.WorkspaceID)
	}

	return notification
}

// Subscribe creates a notification channel for an agent
func (ns *NotificationService) Subscribe(agentName string) NotificationChannel {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	// Create buffered channel
	ch := make(NotificationChannel, 100)
	ns.channels[agentName] = ch

	log.Printf("📬 Agent %s subscribed to notifications", agentName)
	return ch
}

// Unsubscribe removes a notification channel
func (ns *NotificationService) Unsubscribe(agentName string) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	if ch, exists := ns.channels[agentName]; exists {
		close(ch)
		delete(ns.channels, agentName)
		log.Printf("📪 Agent %s unsubscribed from notifications", agentName)
	}
}

// broadcast sends notification to all subscribed channels
func (ns *NotificationService) broadcast(notification Notification) {
	ns.mu.RLock()
	defer ns.mu.RUnlock()

	for agentName, ch := range ns.channels {
		// Filter notifications relevant to the agent
		if ns.isRelevantToAgent(notification, agentName) {
			select {
			case ch <- notification:
				// Successfully sent
			default:
				// Channel full, skip
				log.Printf("⚠️  Notification channel full for agent %s", agentName)
			}
		}
	}
}

// isRelevantToAgent determines if a notification is relevant to an agent
func (ns *NotificationService) isRelevantToAgent(notification Notification, agentName string) bool {
	// If notification is agent-specific, only deliver to that agent
	if notification.AgentName != "" {
		return notification.AgentName == agentName
	}

	// Otherwise, deliver to all agents (workspace-level notifications)
	return true
}

// SendNotification manually sends a notification
func (ns *NotificationService) SendNotification(notification Notification) {
	if notification.ID == "" {
		notification.ID = generateNotificationID()
	}
	if notification.Timestamp.IsZero() {
		notification.Timestamp = time.Now()
	}

	ns.addToHistory(notification)
	ns.broadcast(notification)
}

// SendToAgent sends a notification to a specific agent
func (ns *NotificationService) SendToAgent(agentName, title, message string, priority NotificationPriority) {
	notification := Notification{
		ID:        generateNotificationID(),
		Title:     title,
		Message:   message,
		Priority:  priority,
		AgentName: agentName,
		Timestamp: time.Now(),
		Read:      false,
	}

	ns.addToHistory(notification)
	ns.broadcast(notification)
}

// GetHistory returns recent notifications
func (ns *NotificationService) GetHistory(limit int) []Notification {
	ns.mu.RLock()
	defer ns.mu.RUnlock()

	notifications := make([]Notification, 0, limit)

	// Iterate through history in reverse chronological order
	for i := 0; i < ns.historySize && len(notifications) < limit; i++ {
		idx := (ns.historyIndex - 1 - i + ns.historySize) % ns.historySize
		notif := ns.history[idx]

		// Skip empty slots
		if notif.ID == "" {
			continue
		}

		notifications = append(notifications, notif)
	}

	return notifications
}

// GetUnreadForAgent returns unread notifications for an agent
func (ns *NotificationService) GetUnreadForAgent(agentName string) []Notification {
	ns.mu.RLock()
	defer ns.mu.RUnlock()

	unread := make([]Notification, 0)

	for i := 0; i < ns.historySize; i++ {
		idx := (ns.historyIndex - 1 - i + ns.historySize) % ns.historySize
		notif := ns.history[idx]

		if notif.ID == "" {
			continue
		}

		if !notif.Read && ns.isRelevantToAgent(notif, agentName) {
			unread = append(unread, notif)
		}
	}

	return unread
}

// MarkAsRead marks a notification as read
func (ns *NotificationService) MarkAsRead(notificationID string) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	for i := 0; i < ns.historySize; i++ {
		if ns.history[i].ID == notificationID {
			ns.history[i].Read = true
			break
		}
	}
}

// MarkAllAsRead marks all notifications for an agent as read
func (ns *NotificationService) MarkAllAsRead(agentName string) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	for i := 0; i < ns.historySize; i++ {
		if ns.history[i].ID != "" && ns.isRelevantToAgent(ns.history[i], agentName) {
			ns.history[i].Read = true
		}
	}
}

// ClearHistory clears notification history
func (ns *NotificationService) ClearHistory() {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	ns.history = make([]Notification, ns.historySize)
	ns.historyIndex = 0
}

// Shutdown gracefully shuts down the notification service
func (ns *NotificationService) Shutdown() {
	ns.cancel()

	// Unsubscribe from event bus
	if ns.eventBus != nil && ns.subID != "" {
		ns.eventBus.Unsubscribe(ns.subID)
	}

	// Close all channels
	ns.mu.Lock()
	defer ns.mu.Unlock()

	for agentName, ch := range ns.channels {
		close(ch)
		log.Printf("📪 Closed notification channel for agent %s", agentName)
	}
	ns.channels = make(map[string]NotificationChannel)

	log.Println("✅ Notification service shutdown complete")
}

// addToHistory adds a notification to the ring buffer history
func (ns *NotificationService) addToHistory(notification Notification) {
	ns.mu.Lock()
	defer ns.mu.Unlock()

	ns.history[ns.historyIndex] = notification
	ns.historyIndex = (ns.historyIndex + 1) % ns.historySize
}

// Helper functions

var notifIDCounter uint64
var notifIDMutex sync.Mutex

func generateNotificationID() string {
	notifIDMutex.Lock()
	defer notifIDMutex.Unlock()
	notifIDCounter++
	return fmt.Sprintf("notif-%s-%d", time.Now().Format("20060102150405"), notifIDCounter)
}

// Stats returns notification service statistics
func (ns *NotificationService) Stats() map[string]interface{} {
	ns.mu.RLock()
	defer ns.mu.RUnlock()

	totalNotifications := 0
	unreadNotifications := 0

	for i := 0; i < ns.historySize; i++ {
		if ns.history[i].ID != "" {
			totalNotifications++
			if !ns.history[i].Read {
				unreadNotifications++
			}
		}
	}

	return map[string]interface{}{
		"total_notifications":  totalNotifications,
		"unread_notifications": unreadNotifications,
		"active_channels":      len(ns.channels),
	}
}
