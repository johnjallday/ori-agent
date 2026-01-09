package workspace

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// AddScheduledTask adds a scheduled task to the workspace
func (w *Workspace) AddScheduledTask(st ScheduledTask) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Validate sender is part of workspace (allow system sources like "scheduler", "system")
	systemSources := map[string]bool{
		"scheduler": true,
		"system":    true,
	}
	if !systemSources[st.From] && !w.hasAgent(st.From) {
		return fmt.Errorf("task delegator %s is not part of workspace", st.From)
	}

	// Validate recipient
	if st.To != "" && !w.hasAgent(st.To) {
		return fmt.Errorf("task recipient %s is not part of workspace", st.To)
	}

	// Set ID and timestamps if not set
	if st.ID == "" {
		st.ID = uuid.New().String()
	}
	if st.CreatedAt.IsZero() {
		st.CreatedAt = time.Now()
	}
	st.UpdatedAt = time.Now()

	// Ensure workspace ID matches
	st.WorkspaceID = w.ID

	w.ScheduledTasks = append(w.ScheduledTasks, st)
	w.UpdatedAt = time.Now()

	return nil
}

// GetScheduledTask retrieves a scheduled task by ID
func (w *Workspace) GetScheduledTask(id string) (*ScheduledTask, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	for i := range w.ScheduledTasks {
		if w.ScheduledTasks[i].ID == id {
			return &w.ScheduledTasks[i], nil
		}
	}

	return nil, fmt.Errorf("scheduled task %s not found in workspace", id)
}

// UpdateScheduledTask updates an existing scheduled task
func (w *Workspace) UpdateScheduledTask(st ScheduledTask) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := range w.ScheduledTasks {
		if w.ScheduledTasks[i].ID == st.ID {
			st.UpdatedAt = time.Now()
			w.ScheduledTasks[i] = st
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("scheduled task %s not found in workspace", st.ID)
}

// DeleteScheduledTask removes a scheduled task from the workspace
func (w *Workspace) DeleteScheduledTask(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := range w.ScheduledTasks {
		if w.ScheduledTasks[i].ID == id {
			w.ScheduledTasks = append(w.ScheduledTasks[:i], w.ScheduledTasks[i+1:]...)
			w.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("scheduled task %s not found in workspace", id)
}

// GetEnabledScheduledTasks returns all enabled scheduled tasks
func (w *Workspace) GetEnabledScheduledTasks() []ScheduledTask {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var enabled []ScheduledTask
	for _, st := range w.ScheduledTasks {
		if st.Enabled {
			enabled = append(enabled, st)
		}
	}
	return enabled
}
