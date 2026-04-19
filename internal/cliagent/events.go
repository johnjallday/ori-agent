package cliagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// EventLogger stores and retrieves CLI event logs per task.
type EventLogger struct {
	mu      sync.RWMutex
	events  map[string][]CLIEvent // taskID -> events
	dataDir string
}

// NewEventLogger creates a new EventLogger that persists logs under dataDir.
func NewEventLogger(dataDir string) *EventLogger {
	return &EventLogger{
		events:  make(map[string][]CLIEvent),
		dataDir: dataDir,
	}
}

// LogEvent appends an event to the given task's log.
func (l *EventLogger) LogEvent(taskID string, event CLIEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events[taskID] = append(l.events[taskID], event)
}

// LogEvents appends multiple events to the given task's log.
func (l *EventLogger) LogEvents(taskID string, events []CLIEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events[taskID] = append(l.events[taskID], events...)
}

// GetEvents returns all events for a task.
func (l *EventLogger) GetEvents(taskID string) []CLIEvent {
	l.mu.RLock()
	defer l.mu.RUnlock()

	events := l.events[taskID]
	if events == nil {
		return []CLIEvent{}
	}
	// Return a copy to prevent mutation
	out := make([]CLIEvent, len(events))
	copy(out, events)
	return out
}

// taskDir returns the directory for a task's persisted data.
func (l *EventLogger) taskDir(taskID string) string {
	return filepath.Join(l.dataDir, "cli_agent_tasks", taskID)
}

// Persist writes the event log to disk as JSON.
func (l *EventLogger) Persist(taskID string) error {
	l.mu.RLock()
	events := l.events[taskID]
	l.mu.RUnlock()

	if events == nil {
		events = []CLIEvent{}
	}

	dir := l.taskDir(taskID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create task dir: %w", err)
	}

	data, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal events: %w", err)
	}

	path := filepath.Join(dir, "events.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write events: %w", err)
	}

	return nil
}

// LoadEvents reads a persisted event log from disk.
func (l *EventLogger) LoadEvents(taskID string) ([]CLIEvent, error) {
	path := filepath.Join(l.taskDir(taskID), "events.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}

	var events []CLIEvent
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("unmarshal events: %w", err)
	}

	// Update in-memory cache
	l.mu.Lock()
	l.events[taskID] = events
	l.mu.Unlock()

	return events, nil
}

// Clear removes the in-memory events for a task.
func (l *EventLogger) Clear(taskID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.events, taskID)
}
