package orchestrationhttp

import (
	"context"
	"encoding/json"
	"fmt"

	"net/http"
	"strings"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// StreamingHandler manages SSE streaming endpoints
type StreamingHandler struct {
	workspaceStore workspace.Store
	orchestrator   *orchestration.Orchestrator
	eventBus       *workspace.EventBus
}

// NewStreamingHandler creates a new streaming handler
func NewStreamingHandler(workspaceStore workspace.Store,
	orchestrator *orchestration.Orchestrator,
	eventBus *workspace.EventBus) *StreamingHandler {
	return &StreamingHandler{
		workspaceStore: workspaceStore,
		orchestrator:   orchestrator,
		eventBus:       eventBus,
	}
}

// WorkflowStatusHandler returns the status of a workspace workflow
// GET /api/orchestration/workflow/status?workspace_id=<id>
func (sh *StreamingHandler) WorkflowStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	if sh.orchestrator == nil {
		orihttp.InternalError(w, "orchestrator not initialized")
		return
	}

	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}

	// Get workflow status from orchestrator
	status, err := sh.orchestrator.GetWorkflowStatus(workspaceID)
	if err != nil {
		logger.Error("Failed to get workflow status", logger.Fields{"error": err})
		orihttp.NotFound(w, err.Error())
		return
	}

	orihttp.WriteJSON(w, status)
}

func (sh *StreamingHandler) WorkflowStatusStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		orihttp.InternalError(w, "streaming not supported")
		return
	}

	// Context with cancellation
	ctx := r.Context()

	logger.Debug("🔄 Starting SSE stream for workspace", logger.Fields{"workspace_id": workspaceID})

	// Use event bus if available for real-time updates
	if sh.eventBus != nil {
		sh.streamEventsFromBus(ctx, w, flusher, workspaceID)
	} else {
		// Fallback to polling-based streaming
		sh.streamEventsFromPolling(ctx, w, flusher, workspaceID)
	}
}

// streamEventsFromBus streams events using the event bus (real-time)
func (sh *StreamingHandler) streamEventsFromBus(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, workspaceID string) {
	// Create event channel
	eventChan := make(chan workspace.Event, 50)

	// Subscribe to workspace events
	subID := sh.eventBus.SubscribeToWorkspace(workspaceID, func(event workspace.Event) {
		select {
		case eventChan <- event:
		default:
			logger.Warn("Event channel full for workspace", logger.Fields{"workspace_id": workspaceID})
		}
	})
	defer sh.eventBus.Unsubscribe(subID)

	// Also create a ticker for periodic status updates (every 5 seconds)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Send initial status immediately
	sh.sendWorkspaceStatus(w, flusher, workspaceID)

	// Stream events
	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			logger.Debug("⏹ SSE stream closed for workspace", logger.Fields{"workspace_id": workspaceID})
			return

		case event := <-eventChan:
			// Send event to client
			eventData := map[string]any{
				"type":         event.Type,
				"workspace_id": event.WorkspaceID,
				"timestamp":    event.Timestamp,
				"source":       event.Source,
				"data":         event.Data,
			}

			data, err := json.Marshal(eventData)
			if err != nil {
				logger.Error("Failed to marshal event", logger.Fields{"err": err})
				continue
			}

			// Send with event type prefix
			_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			if err != nil {
				logger.Error("Failed to write SSE event", logger.Fields{"err": err})
				return
			}
			flusher.Flush()

			// Check for completion events
			if event.Type == workspace.EventWorkspaceCompleted || event.Type == workspace.EventWorkflowCompleted {
				logger.Info("Workspace completed, closing SSE stream", logger.Fields{"workspace_id": workspaceID})
				return
			}

		case <-ticker.C:
			// Send periodic status update
			sh.sendWorkspaceStatus(w, flusher, workspaceID)
		}
	}
}

// streamEventsFromPolling streams events using polling (fallback)
func (sh *StreamingHandler) streamEventsFromPolling(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, workspaceID string) {
	if sh.orchestrator == nil {
		orihttp.InternalError(w, "orchestrator not initialized and event bus not available")
		return
	}

	// Create ticker for periodic updates (every 2 seconds)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Send initial status immediately
	status, err := sh.orchestrator.GetWorkflowStatus(workspaceID)
	if err == nil {
		data, _ := json.Marshal(status)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// Stream updates
	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			logger.Debug("⏹ SSE stream closed for workspace", logger.Fields{"workspace_id": workspaceID})
			return

		case <-ticker.C:
			// Send status update
			status, err := sh.orchestrator.GetWorkflowStatus(workspaceID)
			if err != nil {
				// Workspace might have been deleted or completed
				logger.Error("Failed to get status for workspace", logger.Fields{"workspace_id": workspaceID, "err": err})
				_, _ = w.Write([]byte("event: error\ndata: workspace not found\n\n"))
				flusher.Flush()
				return
			}

			// Send status as JSON
			data, err := json.Marshal(status)
			if err != nil {
				logger.Error("Failed to marshal status", logger.Fields{"error": err})
				continue
			}

			_, err = fmt.Fprintf(w, "data: %s\n\n", data)
			if err != nil {
				logger.Error("Failed to write SSE data", logger.Fields{"err": err})
				return
			}
			flusher.Flush()

			// If workflow is completed, send completion event and close
			if status.Phase == "completed" {
				_, _ = w.Write([]byte("event: complete\ndata: workflow completed\n\n"))
				flusher.Flush()
				logger.Info("Workflow completed, closing SSE stream", logger.Fields{"workspaceID": workspaceID})
				return
			}
		}
	}
}

// sendWorkspaceStatus sends the current workspace status
func (sh *StreamingHandler) sendWorkspaceStatus(w http.ResponseWriter, flusher http.Flusher, workspaceID string) {
	// Try to get workspace
	ws, err := sh.workspaceStore.Get(workspaceID)
	if err != nil {
		return
	}

	statusData := map[string]any{
		"workspace_id": ws.ID,
		"status":       ws.Status,
		"updated_at":   ws.UpdatedAt,
	}

	// Add workflow status if orchestrator is available
	if sh.orchestrator != nil {
		workflowStatus, err := sh.orchestrator.GetWorkflowStatus(workspaceID)
		if err == nil {
			statusData["workflow"] = workflowStatus
		}
	}

	data, err := json.Marshal(statusData)
	if err != nil {
		return
	}

	_, err = fmt.Fprintf(w, "event: status\ndata: %s\n\n", data)
	if err == nil {
		flusher.Flush()
	}
}

// ProgressStreamHandler streams real-time progress updates using Server-Sent Events (SSE)
// GET /api/orchestration/progress/stream?workspace_id=<id>
func (sh *StreamingHandler) ProgressStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace_id is required")
		return
	}

	// Validate workspace exists before setting up SSE stream
	if _, err := sh.workspaceStore.Get(workspaceID); err != nil {
		logger.Debug("Workspace not found for progress stream (may be stale browser session)", logger.Fields{"workspace_id": workspaceID})
		orihttp.NotFound(w, "workspace not found")
		return
	}

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		orihttp.InternalError(w, "streaming not supported")
		return
	}

	ctx := r.Context()

	if sh.eventBus == nil {
		orihttp.ServiceUnavailable(w, "event bus not available")
		return
	}

	// Create event channel
	eventChan := make(chan workspace.Event, 100)

	// Subscribe to workspace events
	subID := sh.eventBus.SubscribeToWorkspace(workspaceID, func(event workspace.Event) {
		select {
		case eventChan <- event:
		default:
			logger.Warn("Progress event channel full for workspace", logger.Fields{"workspace_id": workspaceID})
		}
	})
	defer sh.eventBus.Unsubscribe(subID)

	// Send initial workspace progress
	sh.sendInitialProgress(w, flusher, workspaceID)
	// Immediately follow with a workspace.progress event so clients don't wait for the ticker
	sh.sendWorkspaceProgressUpdate(w, flusher, workspaceID)

	// Create ticker for periodic workspace progress updates (every 10 seconds)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Create keepalive ticker to prevent connection timeout (every 15 seconds)
	keepaliveTicker := time.NewTicker(15 * time.Second)
	defer keepaliveTicker.Stop()

	// Stream events
	for {
		select {
		case <-ctx.Done():
			// Client disconnected - send close message and return
			logger.Debug("Progress stream closed by client", logger.Fields{"workspace_id": workspaceID})
			_, _ = w.Write([]byte("event: close\ndata: stream closed\n\n"))
			flusher.Flush()
			return

		case event := <-eventChan:
			// Send event to client
			eventData := map[string]any{
				"type":         event.Type,
				"workspace_id": event.WorkspaceID,
				"timestamp":    event.Timestamp,
				"source":       event.Source,
				"data":         event.Data,
			}

			data, err := json.Marshal(eventData)
			if err != nil {
				logger.Error("Failed to marshal progress event", logger.Fields{"err": err})
				continue
			}

			// Send with event type prefix
			_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			if err != nil {
				logger.Debug("Failed to write progress SSE event (client likely disconnected)", logger.Fields{"err": err})
				return
			}

			// Check if flush works (connection still alive)
			flusher.Flush()

			// After any task event, send updated workspace progress
			if strings.HasPrefix(string(event.Type), "task.") || strings.HasPrefix(string(event.Type), "attachment.") {
				sh.sendWorkspaceProgressUpdate(w, flusher, workspaceID)
			}

		case <-ticker.C:
			// Send periodic workspace progress update
			sh.sendWorkspaceProgressUpdate(w, flusher, workspaceID)

		case <-keepaliveTicker.C:
			// Send keepalive comment to prevent timeout
			_, err := w.Write([]byte(": keepalive\n\n"))
			if err != nil {
				logger.Debug("Failed to send keepalive (client disconnected)", logger.Fields{"workspace_id": workspaceID})
				return
			}
			flusher.Flush()
		}
	}
}

// sendInitialProgress sends the initial workspace progress
func (sh *StreamingHandler) sendInitialProgress(w http.ResponseWriter, flusher http.Flusher, workspaceID string) {
	ws, err := sh.workspaceStore.Get(workspaceID)
	if err != nil {
		// Workspace may have been deleted after validation - this is not critical
		logger.Debug("Workspace not found for initial progress (may have been deleted)", logger.Fields{"workspace_id": workspaceID})
		return
	}

	progress := ws.GetWorkspaceProgress()
	agentStats := ws.GetAgentStats()

	// Tasks surfaces begin at Ready (PRD workspace-backlog FR40) — filter out
	// Backlog the same way handleGetTasks does, so this SSE dashboard feed
	// doesn't sweep uncommitted captures into the live task list either.
	tasks := make([]workspace.Task, 0, len(ws.Tasks))
	for _, t := range ws.Tasks {
		if t.Status == workspace.TaskStatusBacklog {
			continue
		}
		tasks = append(tasks, t)
	}

	data := map[string]any{
		"workspace_id":       workspaceID,
		"workspace_progress": progress,
		"agent_stats":        agentStats,
		"tasks":              tasks,
		"attachments":        ws.Attachments,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		logger.Error("Failed to marshal initial progress", logger.Fields{"err": err})
		return
	}

	_, _ = fmt.Fprintf(w, "event: initial\ndata: %s\n\n", jsonData)
	flusher.Flush()
}

// sendWorkspaceProgressUpdate sends a workspace progress update
func (sh *StreamingHandler) sendWorkspaceProgressUpdate(w http.ResponseWriter, flusher http.Flusher, workspaceID string) {
	ws, err := sh.workspaceStore.Get(workspaceID)
	if err != nil {
		return // Workspace might have been deleted
	}

	progress := ws.GetWorkspaceProgress()
	agentStats := ws.GetAgentStats()

	eventData := map[string]any{
		"type":               "workspace.progress",
		"workspace_id":       workspaceID,
		"timestamp":          time.Now(),
		"workspace_progress": progress,
		"agent_stats":        agentStats,
		"attachments":        ws.Attachments,
	}

	data, err := json.Marshal(eventData)
	if err != nil {
		logger.Error("Failed to marshal workspace progress", logger.Fields{"error": err})
		return
	}

	_, _ = fmt.Fprintf(w, "event: workspace.progress\ndata: %s\n\n", data)
	flusher.Flush()
}
