package agentstudio

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"
)

// EventType represents the type of workspace event
type EventType string

const (
	// Workspace lifecycle events
	EventWorkspaceCreated   EventType = "studio.created"
	EventWorkspaceUpdated   EventType = "studio.updated"
	EventWorkspaceCompleted EventType = "studio.completed"
	EventWorkspaceDeleted   EventType = "studio.deleted"

	// Task events
	EventTaskCreated   EventType = "task.created"
	EventTaskAssigned  EventType = "task.assigned"
	EventTaskStarted   EventType = "task.started"
	EventTaskCompleted EventType = "task.completed"
	EventTaskFailed    EventType = "task.failed"
	EventTaskTimeout   EventType = "task.timeout"

	// Workflow events
	EventWorkflowStarted   EventType = "workflow.started"
	EventWorkflowCompleted EventType = "workflow.completed"
	EventWorkflowFailed    EventType = "workflow.failed"
	EventStepStarted       EventType = "step.started"
	EventStepCompleted     EventType = "step.completed"
	EventStepFailed        EventType = "step.failed"
	EventStepSkipped       EventType = "step.skipped"

	// Agent events
	EventAgentJoined EventType = "agent.joined"
	EventAgentLeft   EventType = "agent.left"
	EventMessageSent EventType = "message.sent"

	// System events
	EventError   EventType = "error"
	EventWarning EventType = "warning"
)

// Event represents a workspace event
type Event struct {
	ID          string                 `json:"id"`
	Type        EventType              `json:"type"`
	WorkspaceID string                 `json:"studio_id"`
	Timestamp   time.Time              `json:"timestamp"`
	Source      string                 `json:"source"`      // Agent or system component that generated event
	Data        map[string]interface{} `json:"data"`        // Event-specific payload
	Metadata    map[string]string      `json:"metadata"`    // Additional context
}

// EventSubscriber is a function that receives events
type EventSubscriber func(event Event)

// EventFilter determines whether an event should be delivered to a subscriber
type EventFilter func(event Event) bool

// subscription represents a subscriber's registration
type subscription struct {
	ID         string
	Subscriber EventSubscriber
	Filter     EventFilter
}

// EventBus manages event publishing and subscription
type EventBus struct {
	mu            sync.RWMutex
	subscriptions map[string]*subscription  // ID -> subscription
	bufferSize    int
	eventHistory  []Event                    // Ring buffer of recent events
	historySize   int
	historyIndex  int
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewEventBus creates a new event bus
func NewEventBus(bufferSize, historySize int) *EventBus {
	ctx, cancel := context.WithCancel(context.Background())

	return &EventBus{
		subscriptions: make(map[string]*subscription),
		bufferSize:    bufferSize,
		eventHistory:  make([]Event, historySize),
		historySize:   historySize,
		historyIndex:  0,
		ctx:           ctx,
		cancel:        cancel,
	}
}

// DefaultEventBus creates an event bus with default settings
func DefaultEventBus() *EventBus {
	return NewEventBus(100, 1000) // Buffer 100, keep last 1000 events
}

// Publish publishes an event to all matching subscribers
func (eb *EventBus) Publish(event Event) {
	// Set timestamp if not already set
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Generate ID if not set
	if event.ID == "" {
		event.ID = generateEventID()
	}

	// Add to history
	eb.addToHistory(event)

	// Get all matching subscriptions
	eb.mu.RLock()
	subs := make([]*subscription, 0, len(eb.subscriptions))
	for _, sub := range eb.subscriptions {
		if sub.Filter == nil || sub.Filter(event) {
			subs = append(subs, sub)
		}
	}
	eb.mu.RUnlock()

	// Deliver to subscribers (non-blocking)
	for _, sub := range subs {
		go func(s *subscription) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("❌ Event subscriber panic: %v", r)
				}
			}()
			s.Subscriber(event)
		}(sub)
	}
}

// Subscribe registers a subscriber with optional filter
func (eb *EventBus) Subscribe(subscriber EventSubscriber, filter EventFilter) string {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	sub := &subscription{
		ID:         generateSubscriptionID(),
		Subscriber: subscriber,
		Filter:     filter,
	}

	eb.subscriptions[sub.ID] = sub
	return sub.ID
}

// SubscribeToWorkspace creates a subscription filtered by workspace ID
func (eb *EventBus) SubscribeToWorkspace(workspaceID string, subscriber EventSubscriber) string {
	filter := func(event Event) bool {
		return event.WorkspaceID == workspaceID
	}
	return eb.Subscribe(subscriber, filter)
}

// SubscribeToEventType creates a subscription filtered by event type
func (eb *EventBus) SubscribeToEventType(eventType EventType, subscriber EventSubscriber) string {
	filter := func(event Event) bool {
		return event.Type == eventType
	}
	return eb.Subscribe(subscriber, filter)
}

// SubscribeToEventTypes creates a subscription filtered by multiple event types
func (eb *EventBus) SubscribeToEventTypes(eventTypes []EventType, subscriber EventSubscriber) string {
	filter := func(event Event) bool {
		for _, et := range eventTypes {
			if event.Type == et {
				return true
			}
		}
		return false
	}
	return eb.Subscribe(subscriber, filter)
}

// Unsubscribe removes a subscription
func (eb *EventBus) Unsubscribe(subscriptionID string) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	delete(eb.subscriptions, subscriptionID)
}

// GetHistory returns recent events, optionally filtered
func (eb *EventBus) GetHistory(filter EventFilter, limit int) []Event {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	events := make([]Event, 0, limit)

	// Iterate through history in reverse chronological order
	for i := 0; i < eb.historySize && len(events) < limit; i++ {
		idx := (eb.historyIndex - 1 - i + eb.historySize) % eb.historySize
		event := eb.eventHistory[idx]

		// Skip empty slots
		if event.ID == "" {
			continue
		}

		if filter == nil || filter(event) {
			events = append(events, event)
		}
	}

	return events
}

// GetWorkspaceHistory returns events for a specific workspace
func (eb *EventBus) GetWorkspaceHistory(workspaceID string, limit int) []Event {
	filter := func(event Event) bool {
		return event.WorkspaceID == workspaceID
	}
	return eb.GetHistory(filter, limit)
}

// GetEventsSince returns events since a given timestamp
func (eb *EventBus) GetEventsSince(since time.Time, limit int) []Event {
	filter := func(event Event) bool {
		return event.Timestamp.After(since)
	}
	return eb.GetHistory(filter, limit)
}

// SubscriberCount returns the number of active subscriptions
func (eb *EventBus) SubscriberCount() int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	return len(eb.subscriptions)
}

// ClearHistory clears the event history
func (eb *EventBus) ClearHistory() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.eventHistory = make([]Event, eb.historySize)
	eb.historyIndex = 0
}

// Shutdown gracefully shuts down the event bus
func (eb *EventBus) Shutdown() {
	eb.cancel()
	eb.mu.Lock()
	defer eb.mu.Unlock()

	// Clear all subscriptions
	eb.subscriptions = make(map[string]*subscription)
	log.Println("✅ Event bus shutdown complete")
}

// addToHistory adds an event to the ring buffer history
func (eb *EventBus) addToHistory(event Event) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	eb.eventHistory[eb.historyIndex] = event
	eb.historyIndex = (eb.historyIndex + 1) % eb.historySize
}

// Helper functions

var eventIDCounter uint64
var eventIDMutex sync.Mutex

func generateEventID() string {
	eventIDMutex.Lock()
	defer eventIDMutex.Unlock()
	eventIDCounter++
	return time.Now().Format("20060102150405") + "-" + string(rune(eventIDCounter%10000))
}

var subIDCounter uint64
var subIDMutex sync.Mutex

func generateSubscriptionID() string {
	subIDMutex.Lock()
	defer subIDMutex.Unlock()
	subIDCounter++
	return "sub-" + time.Now().Format("20060102150405") + "-" + string(rune(subIDCounter%10000))
}

// Helper methods to create events

// NewWorkspaceEvent creates a workspace-related event
func NewWorkspaceEvent(eventType EventType, workspaceID, source string, data map[string]interface{}) Event {
	return Event{
		Type:        eventType,
		WorkspaceID: workspaceID,
		Source:      source,
		Data:        data,
		Metadata:    make(map[string]string),
	}
}

// NewTaskEvent creates a task-related event
func NewTaskEvent(eventType EventType, workspaceID, taskID, agentName string, data map[string]interface{}) Event {
	if data == nil {
		data = make(map[string]interface{})
	}
	data["task_id"] = taskID
	data["agent"] = agentName

	return Event{
		Type:        eventType,
		WorkspaceID: workspaceID,
		Source:      "task-executor",
		Data:        data,
		Metadata:    make(map[string]string),
	}
}

// NewWorkflowEvent creates a workflow-related event
func NewWorkflowEvent(eventType EventType, workspaceID, workflowID string, data map[string]interface{}) Event {
	if data == nil {
		data = make(map[string]interface{})
	}
	data["workflow_id"] = workflowID

	return Event{
		Type:        eventType,
		WorkspaceID: workspaceID,
		Source:      "workflow-executor",
		Data:        data,
		Metadata:    make(map[string]string),
	}
}

// ToJSON serializes event to JSON
func (e *Event) ToJSON() (string, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// FromJSON deserializes event from JSON
func EventFromJSON(jsonData string) (*Event, error) {
	var event Event
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		return nil, err
	}
	return &event, nil
}
