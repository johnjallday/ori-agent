package workspace

import (
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

// GetSummary returns a summary of the workspace
func (w *Workspace) GetSummary() map[string]any {
	w.mu.RLock()
	defer w.mu.RUnlock()

	agentNames := w.agentNamesLocked()
	return map[string]any{
		"id":            w.ID,
		"name":          w.Name,
		"description":   w.Description,
		"tags":          append([]string(nil), w.Tags...),
		"agents":        agentNames,
		"agent_count":   len(agentNames),
		"message_count": len(w.Messages),
		"task_count":    len(w.Tasks),
		"status":        w.Status,
		"created_at":    w.CreatedAt,
		"updated_at":    w.UpdatedAt,
	}
}

// MapSummaryFields holds the additional display fields the Workspace Map view
// needs but GetSummary() omits: entry agent, roster, tool/skill counts, ops
// mode, and open-task/active state. Shared by orchestrationhttp (GetSummary
// callers) and sessionhttp (session.Workspace list/tree callers) so both
// surfaces derive the same values from one place.
type MapSummaryFields struct {
	EntryAgentName      string
	AgentNames          []string
	AgentCount          int
	OpenTaskCount       int
	NeedsAttentionCount int
	// BacklogCount is the workspace's own (non-descendant) Backlog item count
	// (tasks/prd-workspace-backlog.md FR40, 49, 58). It is tracked separately
	// from OpenTaskCount, which remains Ready-and-later only — Backlog is
	// never "open" work (FR7).
	BacklogCount int
	MCPCount     int
	SkillCount   int
	OpsMode      string
	Active       bool
}

// ComputeMapSummaryFields derives entry agent, roster, tool/skill counts, ops
// mode, and open-task/active state from a workspace in a single locked pass.
func ComputeMapSummaryFields(w *Workspace) MapSummaryFields {
	if w == nil {
		return MapSummaryFields{}
	}

	w.mu.RLock()
	defer w.mu.RUnlock()

	agentNames := w.agentNamesLocked()
	fields := MapSummaryFields{
		EntryAgentName: w.entryAgentNameLocked(),
		AgentNames:     agentNames,
		AgentCount:     len(agentNames),
		MCPCount:       len(w.MCPBindings),
		SkillCount:     len(w.SkillBindings),
		OpsMode:        workspacesettings.Extract(w.SharedData).Workflow.Mode,
	}

	for _, t := range w.Tasks {
		switch t.Status {
		case TaskStatusBacklog:
			fields.BacklogCount++
		case TaskStatusPending:
			fields.OpenTaskCount++
		case TaskStatusInProgress:
			fields.OpenTaskCount++
			fields.Active = true
		case TaskStatusFailed, TaskStatusTimeout:
			fields.NeedsAttentionCount++
		}
	}

	return fields
}

// busyQueueThreshold is the queued-task count beyond which an agent is
// reported "busy" regardless of its other activity.
const busyQueueThreshold = 5

// GetAgentStats returns statistics for all agents in the workspace
func (w *Workspace) GetAgentStats() map[string]AgentStats {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.agentStatsLocked()
}

// agentStatsLocked computes per-agent task statistics. Callers must hold
// w.mu (read or write). Single implementation shared by GetAgentStats and
// GetWorkspaceProgress so the status handling cannot drift between them
// (a previous private copy switched on raw strings and silently missed
// TaskStatusTimeout).
func (w *Workspace) agentStatsLocked() map[string]AgentStats {
	stats := make(map[string]AgentStats)

	// Initialize stats for all agents
	for _, agentName := range w.agentNamesLocked() {
		stats[agentName] = AgentStats{
			Name:         agentName,
			Status:       "idle",
			CurrentTasks: []string{},
			QueuedTasks:  []string{},
		}
	}

	// Analyze tasks to calculate agent stats
	for _, task := range w.Tasks {
		if task.To == "" || task.To == "unassigned" {
			continue
		}

		agentStat, exists := stats[task.To]
		if !exists {
			continue // Skip tasks for agents not in this workspace
		}

		switch task.Status {
		case TaskStatusInProgress:
			agentStat.CurrentTasks = append(agentStat.CurrentTasks, task.ID)
			agentStat.Status = "active"
			if task.StartedAt != nil && task.StartedAt.After(agentStat.LastActive) {
				agentStat.LastActive = *task.StartedAt
			}

		case TaskStatusPending, TaskStatusAssigned:
			agentStat.QueuedTasks = append(agentStat.QueuedTasks, task.ID)
			if agentStat.Status == "idle" {
				agentStat.Status = "queued"
			}

		case TaskStatusWaitingForChoice:
			agentStat.CurrentTasks = append(agentStat.CurrentTasks, task.ID)
			if agentStat.Status == "idle" || agentStat.Status == "queued" {
				agentStat.Status = "waiting"
			}

		case TaskStatusCompleted:
			agentStat.CompletedTasks++
			agentStat.TotalExecutions++
			if task.CompletedAt != nil && task.CompletedAt.After(agentStat.LastActive) {
				agentStat.LastActive = *task.CompletedAt
			}

		case TaskStatusFailed, TaskStatusTimeout:
			agentStat.FailedTasks++
			agentStat.TotalExecutions++
			if agentStat.Status == "idle" || agentStat.Status == "queued" {
				agentStat.Status = "error"
			}
			if task.CompletedAt != nil && task.CompletedAt.After(agentStat.LastActive) {
				agentStat.LastActive = *task.CompletedAt
			}
		}

		stats[task.To] = agentStat
	}

	// Determine if agent is busy (multiple tasks queued)
	for agentName, agentStat := range stats {
		if len(agentStat.QueuedTasks) > busyQueueThreshold {
			agentStat.Status = "busy"
			stats[agentName] = agentStat
		}
	}

	return stats
}

// GetWorkspaceProgress calculates overall workspace progress
func (w *Workspace) GetWorkspaceProgress() Progress {
	w.mu.RLock()
	defer w.mu.RUnlock()

	progress := Progress{
		TotalTasks:  len(w.Tasks),
		TotalAgents: len(w.agentNamesLocked()),
	}

	if progress.TotalTasks == 0 {
		return progress
	}

	// Count tasks by status
	var firstStartTime time.Time
	var totalDuration float64
	var completedCount int

	for _, task := range w.Tasks {
		switch task.Status {
		case "completed":
			progress.CompletedTasks++
			completedCount++

			// Calculate task duration if we have timestamps
			if task.StartedAt != nil && task.CompletedAt != nil && !task.StartedAt.IsZero() && !task.CompletedAt.IsZero() {
				duration := task.CompletedAt.Sub(*task.StartedAt).Milliseconds()
				totalDuration += float64(duration)
			}
		case "in_progress":
			progress.InProgressTasks++

			// Track earliest start time
			if task.StartedAt != nil && !task.StartedAt.IsZero() {
				if firstStartTime.IsZero() || task.StartedAt.Before(firstStartTime) {
					firstStartTime = *task.StartedAt
				}
			}
		case "pending", "assigned", "waiting_for_choice":
			progress.PendingTasks++
		case "failed":
			progress.FailedTasks++
		}

		// Track earliest task creation time for workspace start
		if !task.CreatedAt.IsZero() {
			if firstStartTime.IsZero() || task.CreatedAt.Before(firstStartTime) {
				firstStartTime = task.CreatedAt
			}
		}
	}

	// Calculate percentage (completed / total)
	if progress.TotalTasks > 0 {
		progress.Percentage = (progress.CompletedTasks * 100) / progress.TotalTasks
	}

	// Calculate average task time
	if completedCount > 0 {
		progress.AverageTaskTime = totalDuration / float64(completedCount)
	}

	// Calculate elapsed time
	if !firstStartTime.IsZero() {
		progress.StartedAt = firstStartTime
		progress.ElapsedTimeMs = float64(time.Since(firstStartTime).Milliseconds())
	}

	// Estimate remaining time based on average task time and remaining tasks
	if progress.AverageTaskTime > 0 {
		remainingTasks := progress.InProgressTasks + progress.PendingTasks
		progress.RemainingTimeMs = progress.AverageTaskTime * float64(remainingTasks)

		// Estimate completion time
		if progress.RemainingTimeMs > 0 {
			progress.EstimatedEnd = time.Now().Add(time.Duration(progress.RemainingTimeMs) * time.Millisecond)
		}
	}

	// Count active vs idle agents using agent stats
	agentStats := w.agentStatsLocked() // already holding the read lock
	for _, stats := range agentStats {
		if stats.Status == "active" || stats.Status == "busy" {
			progress.ActiveAgents++
		} else {
			progress.IdleAgents++
		}
	}

	return progress
}
