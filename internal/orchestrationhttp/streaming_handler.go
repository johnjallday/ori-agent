package orchestrationhttp

import (
	"context"
	"encoding/json"
	"fmt"

	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/orchestration"
)

// StreamingHandler manages SSE streaming endpoints
type StreamingHandler struct {
	workspaceStore agentstudio.Store
	orchestrator   *orchestration.Orchestrator
	eventBus       *agentstudio.EventBus
}

// NewStreamingHandler creates a new streaming handler
func NewStreamingHandler(workspaceStore agentstudio.Store,
	orchestrator *orchestration.Orchestrator,
	eventBus *agentstudio.EventBus) *StreamingHandler {
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if sh.orchestrator == nil {
		if err := orihttp.RespondInternalError(w, "orchestrator not initialized"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	workspaceID := r.URL.Query().Get("studio_id")
	if workspaceID == "" {
		if err := orihttp.RespondBadRequest(w, "workspace_id is required"); err != nil {
			logger.

				// Get workflow status from orchestrator
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	status, err := sh.orchestrator.GetWorkflowStatus(workspaceID)
	if err != nil {
		logger.Error("Failed to get workflow status", logger.Fields{"status": err})
		if err := orihttp.RespondNotFound(w, err.Error()); err != nil {
			logger.Error("Failed to write response", logger.

				// WorkflowStatusStreamHandler streams real-time workflow status updates using Server-Sent Events (SSE)
				// GET /api/orchestration/workflow/stream?workspace_id=<id>
				Fields{"error": err})
		}
		return
	}

	orihttp.WriteJSON(w, status)
}

func (sh *StreamingHandler) WorkflowStatusStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	workspaceID := r.URL.Query().Get("studio_id")
	if workspaceID == "" {
		if err := orihttp.RespondBadRequest(w, "workspace_id is required"); err != nil {
			logger.

				// Set headers for SSE
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		if err := orihttp.RespondInternalError(w, "streaming not supported"); err != nil {
			logger.

				// Context with cancellation
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

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
	eventChan := make(chan agentstudio.Event, 50)

	// Subscribe to workspace events
	subID := sh.eventBus.SubscribeToWorkspace(workspaceID, func(event agentstudio.Event) {
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
			eventData := map[string]interface{}{
				"type":      event.Type,
				"studio_id": event.WorkspaceID,
				"timestamp": event.Timestamp,
				"source":    event.Source,
				"data":      event.Data,
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
			if event.Type == agentstudio.EventWorkspaceCompleted || event.Type == agentstudio.EventWorkflowCompleted {
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
		if err := orihttp.RespondInternalError(w, "orchestrator not initialized and event bus not available"); err != nil {
			logger.

				// Create ticker for periodic updates (every 2 seconds)
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

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
				logger.Error("Failed to marshal status", logger.Fields{"status": err})
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

	statusData := map[string]interface{}{
		"studio_id":  ws.ID,
		"status":     ws.Status,
		"updated_at": ws.UpdatedAt,
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		if err := orihttp.RespondBadRequest(w, "workspace_id is required"); err != nil {
			logger.

				// Set headers for SSE
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		if err := orihttp.RespondInternalError(w, "streaming not supported"); err != nil {
			logger.Error("Failed to write response",

				// If no event bus, return error
				logger.Fields{"error": err})
		}
		return
	}

	ctx := r.Context()

	if sh.eventBus == nil {
		if err := orihttp.RespondServiceUnavailable(w, "event bus not available"); err != nil {
			logger.Error("Failed to write service unavailable response", logger.Fields{"error": err})
		}
		return
	}

	// Create event channel
	eventChan := make(chan agentstudio.Event, 100)

	// Subscribe to workspace events
	subID := sh.eventBus.SubscribeToWorkspace(workspaceID, func(event agentstudio.Event) {
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
			eventData := map[string]interface{}{
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
		logger.Error("Failed to get workspace for initial progress", logger.Fields{"error": err})
		return
	}

	progress := ws.GetWorkspaceProgress()
	agentStats := ws.GetAgentStats()

	data := map[string]interface{}{
		"workspace_id":       workspaceID,
		"workspace_progress": progress,
		"agent_stats":        agentStats,
		"tasks":              ws.Tasks,
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

	eventData := map[string]interface{}{
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
