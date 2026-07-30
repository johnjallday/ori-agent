package workspace

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// NewWorkspace creates a new workspace
func NewWorkspace(params CreateWorkspaceParams) *Workspace {
	now := time.Now()
	ws := &Workspace{
		ID:          uuid.New().String(),
		Name:        params.Name,
		Description: params.Description,
		SharedData:  params.InitialData,
		Messages:    []AgentMessage{},
		Tasks:       []Task{},
		Folders:     []Folder{},
		Status:      StatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
		taskIndex:   make(map[string]int),
	}
	for _, agentName := range params.Agents {
		_ = ws.AddAgent(agentName)
	}
	return ws
}

// AddMessage adds a message to the workspace
func (w *Workspace) AddMessage(msg AgentMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Validate sender is part of workspace
	if !w.hasAgent(msg.From) {
		return fmt.Errorf("sender %s is not part of workspace", msg.From)
	}

	// Validate recipient if specified
	if msg.To != "" && !w.hasAgent(msg.To) {
		return fmt.Errorf("recipient %s is not part of workspace", msg.To)
	}

	// Set message ID and timestamp if not set
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}

	w.Messages = append(w.Messages, msg)
	w.UpdatedAt = time.Now()

	return nil
}

// GetMessagesForAgent returns all messages relevant to a specific agent
func (w *Workspace) GetMessagesForAgent(agentName string) []AgentMessage {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var messages []AgentMessage
	for _, msg := range w.Messages {
		// Include messages sent to this agent, broadcast messages, or messages from this agent
		if msg.To == agentName || msg.To == "" || msg.From == agentName {
			messages = append(messages, msg)
		}
	}
	return messages
}

// GetMessagesSince returns messages added after the specified time
func (w *Workspace) GetMessagesSince(since time.Time) []AgentMessage {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var messages []AgentMessage
	for _, msg := range w.Messages {
		if msg.Timestamp.After(since) {
			messages = append(messages, msg)
		}
	}
	return messages
}

// SetSharedData sets a value in the shared data store
func (w *Workspace) SetSharedData(key string, value any) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.SharedData == nil {
		w.SharedData = make(map[string]any)
	}
	w.SharedData[key] = value
	w.UpdatedAt = time.Now()
}

// GetSharedData retrieves a value from the shared data store
func (w *Workspace) GetSharedData(key string) (any, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	val, ok := w.SharedData[key]
	return val, ok
}

// SetStatus updates the workspace status
func (w *Workspace) SetStatus(status WorkspaceStatus) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.Status = status
	w.UpdatedAt = time.Now()
}

// GetStatus returns the current workspace status
func (w *Workspace) GetStatus() WorkspaceStatus {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.Status
}

// ToJSON serializes the workspace to JSON
func (w *Workspace) ToJSON() ([]byte, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return json.MarshalIndent(w, "", "  ")
}

// FromJSON deserializes a workspace from JSON
func FromJSON(data []byte) (*Workspace, error) {
	var ws Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, err
	}
	ws.NormalizeAgentInstances()        // Collapse duplicate agent instances to one per profile
	ws.NormalizeInstalledCapabilities() // Canonicalize IDs, drop unusable records, one per capability
	ws.MigrateScheduledTasksToTasks()   // Auto-migrate legacy scheduled tasks
	if ws.Folders == nil {
		ws.Folders = []Folder{}
	}
	ws.rebuildTaskIndex() // Build index for O(1) task lookups
	return &ws, nil
}

// discardJSON is a json.Unmarshaler that ignores its input. It lets a decode skip
// building a heavy field's value while the decoder still scans past it.
type discardJSON struct{}

func (discardJSON) UnmarshalJSON([]byte) error { return nil }

// FromJSONMetadata decodes a workspace.json like FromJSON but skips building the
// chat-history slice (Messages). The boot loader uses it because the in-memory
// cache is metadata-only (item 2.0): allocating every AgentMessage just to drop it
// is wasted work. Tasks are still decoded — migrations and the task index need them
// — and all of FromJSON's normalizations run, so the result equals FromJSON minus
// Messages. The result must NOT be persisted: it would write empty messages to
// disk. Callers that may rewrite the file re-parse with FromJSON first.
func FromJSONMetadata(data []byte) (*Workspace, error) {
	// Embedding Workspace and re-declaring Messages at the outer (shallower) level
	// shadows Workspace.Messages during unmarshal, so chat history is discarded
	// instead of decoded. Returning &lite.Workspace avoids copying the struct's lock.
	lite := &struct {
		Workspace
		Messages discardJSON `json:"messages"`
	}{}
	if err := json.Unmarshal(data, lite); err != nil {
		return nil, err
	}
	ws := &lite.Workspace
	ws.Messages = nil
	ws.NormalizeAgentInstances()
	ws.NormalizeInstalledCapabilities()
	ws.MigrateScheduledTasksToTasks()
	if ws.Folders == nil {
		ws.Folders = []Folder{}
	}
	ws.rebuildTaskIndex()
	return ws, nil
}

// MigrateScheduledTasksToTasks migrates legacy ScheduledTasks to Tasks with Schedule fields
// Returns the number of tasks migrated
func (w *Workspace) MigrateScheduledTasksToTasks() int {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Skip if no legacy scheduled tasks
	if len(w.ScheduledTasks) == 0 {
		return 0
	}

	migrated := 0

	for _, st := range w.ScheduledTasks {
		// Check if this scheduled task links to an existing task
		if st.TargetTaskID != "" {
			// Find the target task and copy schedule to it
			for i := range w.Tasks {
				if w.Tasks[i].ID == st.TargetTaskID {
					w.Tasks[i].Schedule = &st.Schedule
					w.Tasks[i].ScheduleEnabled = st.Enabled
					w.Tasks[i].ScheduleName = st.Name
					w.Tasks[i].NextRun = st.NextRun
					w.Tasks[i].LastRun = st.LastRun
					w.Tasks[i].ExecutionCount = st.ExecutionCount
					w.Tasks[i].FailureCount = st.FailureCount
					w.Tasks[i].ExecutionHistory = st.ExecutionHistory
					migrated++
					break
				}
			}
		} else {
			// Orphan scheduled task - create a new task with schedule
			now := time.Now()
			newTask := Task{
				ID:               uuid.New().String(),
				WorkspaceID:      w.ID,
				From:             st.From,
				To:               st.To,
				Description:      st.Prompt,
				Details:          st.Description,
				Priority:         st.Priority,
				Context:          st.Context,
				Status:           TaskStatusPending,
				Schedule:         &st.Schedule,
				ScheduleEnabled:  st.Enabled,
				ScheduleName:     st.Name,
				NextRun:          st.NextRun,
				LastRun:          st.LastRun,
				ExecutionCount:   st.ExecutionCount,
				FailureCount:     st.FailureCount,
				ExecutionHistory: st.ExecutionHistory,
				CreatedAt:        st.CreatedAt,
			}
			if newTask.CreatedAt.IsZero() {
				newTask.CreatedAt = now
			}
			w.Tasks = append(w.Tasks, newTask)
			migrated++
		}
	}

	// Note: We don't clear ScheduledTasks here - the store will detect the migration
	// (tasks have schedules AND ScheduledTasks exists) and persist, then clear.
	if migrated > 0 {
		w.UpdatedAt = time.Now()
		logger.Info("Migrated scheduled tasks to task schedules", logger.Fields{
			"workspace_id": w.ID,
			"migrated":     migrated,
		})
	}

	return migrated
}

// ClearLegacyScheduledTasks removes the deprecated ScheduledTasks array after migration
func (w *Workspace) ClearLegacyScheduledTasks() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ScheduledTasks = nil
}
