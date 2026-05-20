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
	return &Workspace{
		ID:          uuid.New().String(),
		Name:        params.Name,
		Description: params.Description,
		Agents:      params.Agents,
		SharedData:  params.InitialData,
		Messages:    []AgentMessage{},
		Tasks:       []Task{},
		Status:      StatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
		taskIndex:   make(map[string]int),
	}
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
	ws.MigrateToAgentInstances()      // Auto-migrate legacy agent format
	ws.NormalizeAgentInstances()      // Collapse duplicate agent instances to one per profile
	ws.MigrateScheduledTasksToTasks() // Auto-migrate legacy scheduled tasks
	ws.rebuildTaskIndex()             // Build index for O(1) task lookups
	return &ws, nil
}

// MigrateToAgentInstances migrates legacy Agents []string to AgentInstances
func (w *Workspace) MigrateToAgentInstances() {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Skip if already migrated or no legacy agents
	if len(w.AgentInstances) > 0 || len(w.Agents) == 0 {
		return
	}

	// Count instances of each agent type to assign instance numbers
	instanceCounts := make(map[string]int)

	for _, agentName := range w.Agents {
		instanceCounts[agentName]++
		instanceNumber := instanceCounts[agentName]

		instance := AgentInstance{
			ID:             uuid.New().String(),
			Name:           agentName,
			InstanceNumber: instanceNumber,
			NodeID:         fmt.Sprintf("%s-node-%d", agentName, instanceNumber),
			CreatedAt:      time.Now(),
		}
		w.AgentInstances = append(w.AgentInstances, instance)
	}

	logger.Debug("Migrated workspace : legacy agents -> agent instances", logger.Fields{"workspace_id": w.ID, "agents)": len(w.Agents), "agentinstances)": len(w.AgentInstances)})
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
