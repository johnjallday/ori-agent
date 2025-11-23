package orchestrationhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentstudio"
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
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if sh.orchestrator == nil {
		http.Error(w, "orchestrator not initialized", http.StatusInternalServerError)
		return
	}

	workspaceID := r.URL.Query().Get("studio_id")
	if workspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}

	// Get workflow status from orchestrator
	status, err := sh.orchestrator.GetWorkflowStatus(workspaceID)
	if err != nil {
		log.Printf("❌ Failed to get workflow status: %v", err)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	_ = json.NewEncoder(w).Encode(status)
}

// WorkflowStatusStreamHandler streams real-time workflow status updates using Server-Sent Events (SSE)
// GET /api/orchestration/workflow/stream?workspace_id=<id>
func (sh *StreamingHandler) WorkflowStatusStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspaceID := r.URL.Query().Get("studio_id")
	if workspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Context with cancellation
	ctx := r.Context()

	log.Printf("🔄 Starting SSE stream for workspace %s", workspaceID)

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
			log.Printf("⚠️  Event channel full for workspace %s", workspaceID)
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
			log.Printf("⏹  SSE stream closed for workspace %s", workspaceID)
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
				log.Printf("❌ Failed to marshal event: %v", err)
				continue
			}

			// Send with event type prefix
			_, err = w.Write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, data)))
			if err != nil {
				log.Printf("❌ Failed to write SSE event: %v", err)
				return
			}
			flusher.Flush()

			// Check for completion events
			if event.Type == agentstudio.EventWorkspaceCompleted || event.Type == agentstudio.EventWorkflowCompleted {
				log.Printf("✅ Workspace %s completed, closing SSE stream", workspaceID)
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
		http.Error(w, "orchestrator not initialized and event bus not available", http.StatusInternalServerError)
		return
	}

	// Create ticker for periodic updates (every 2 seconds)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Send initial status immediately
	status, err := sh.orchestrator.GetWorkflowStatus(workspaceID)
	if err == nil {
		data, _ := json.Marshal(status)
		_, _ = w.Write([]byte(fmt.Sprintf("data: %s\n\n", data)))
		flusher.Flush()
	}

	// Stream updates
	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			log.Printf("⏹  SSE stream closed for workspace %s", workspaceID)
			return

		case <-ticker.C:
			// Send status update
			status, err := sh.orchestrator.GetWorkflowStatus(workspaceID)
			if err != nil {
				// Workspace might have been deleted or completed
				log.Printf("❌ Failed to get status for workspace %s: %v", workspaceID, err)
				_, _ = w.Write([]byte("event: error\ndata: workspace not found\n\n"))
				flusher.Flush()
				return
			}

			// Send status as JSON
			data, err := json.Marshal(status)
			if err != nil {
				log.Printf("❌ Failed to marshal status: %v", err)
				continue
			}

			_, err = w.Write([]byte(fmt.Sprintf("data: %s\n\n", data)))
			if err != nil {
				log.Printf("❌ Failed to write SSE data: %v", err)
				return
			}
			flusher.Flush()

			// If workflow is completed, send completion event and close
			if status.Phase == "completed" {
				_, _ = w.Write([]byte("event: complete\ndata: workflow completed\n\n"))
				flusher.Flush()
				log.Printf("✅ Workflow %s completed, closing SSE stream", workspaceID)
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

	_, err = w.Write([]byte(fmt.Sprintf("event: status\ndata: %s\n\n", data)))
	if err == nil {
		flusher.Flush()
	}
}

// ProgressStreamHandler streams real-time progress updates using Server-Sent Events (SSE)
// GET /api/orchestration/progress/stream?workspace_id=<id>
func (sh *StreamingHandler) ProgressStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Get flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	// If no event bus, return error
	if sh.eventBus == nil {
		http.Error(w, "event bus not available", http.StatusServiceUnavailable)
		return
	}

	// Create event channel
	eventChan := make(chan agentstudio.Event, 100)

	// Subscribe to workspace events
	subID := sh.eventBus.SubscribeToWorkspace(workspaceID, func(event agentstudio.Event) {
		select {
		case eventChan <- event:
		default:
			log.Printf("⚠️  Progress event channel full for workspace %s", workspaceID)
		}
	})
	defer sh.eventBus.Unsubscribe(subID)

	// Send initial workspace progress
	sh.sendInitialProgress(w, flusher, workspaceID)

	// Create ticker for periodic workspace progress updates (every 10 seconds)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Stream events
	for {
		select {
		case <-ctx.Done():
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
				log.Printf("❌ Failed to marshal progress event: %v", err)
				continue
			}

			// Send with event type prefix
			_, err = w.Write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, data)))
			if err != nil {
				log.Printf("❌ Failed to write progress SSE event: %v", err)
				return
			}
			flusher.Flush()

			// After any task event, send updated workspace progress
			if strings.HasPrefix(string(event.Type), "task.") {
				sh.sendWorkspaceProgressUpdate(w, flusher, workspaceID)
			}

		case <-ticker.C:
			// Send periodic workspace progress update
			sh.sendWorkspaceProgressUpdate(w, flusher, workspaceID)
		}
	}
}

// sendInitialProgress sends the initial workspace progress
func (sh *StreamingHandler) sendInitialProgress(w http.ResponseWriter, flusher http.Flusher, workspaceID string) {
	ws, err := sh.workspaceStore.Get(workspaceID)
	if err != nil {
		log.Printf("❌ Failed to get workspace for initial progress: %v", err)
		return
	}

	progress := ws.GetWorkspaceProgress()
	agentStats := ws.GetAgentStats()

	data := map[string]interface{}{
		"workspace_id":       workspaceID,
		"workspace_progress": progress,
		"agent_stats":        agentStats,
		"tasks":              ws.Tasks,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Printf("❌ Failed to marshal initial progress: %v", err)
		return
	}

	_, _ = w.Write([]byte(fmt.Sprintf("event: initial\ndata: %s\n\n", jsonData)))
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
	}

	data, err := json.Marshal(eventData)
	if err != nil {
		log.Printf("❌ Failed to marshal workspace progress: %v", err)
		return
	}

	_, _ = w.Write([]byte(fmt.Sprintf("event: workspace.progress\ndata: %s\n\n", data)))
	flusher.Flush()
}
