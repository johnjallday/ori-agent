// Package cliagenthttp provides HTTP handlers for managing CLI agent tasks.
package cliagenthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/johnjallday/ori-agent/internal/cliagent"
)

// Handler provides HTTP endpoints for CLI agent task management.
type Handler struct {
	executor    *cliagent.MicroStepExecutor
	registry    *cliagent.CLIAgentRegistry
	eventLogger *cliagent.EventLogger

	mu      sync.RWMutex
	results map[string]*cliagent.TaskResult // taskID -> result (for completed tasks)
	pending map[string]*cliagent.TaskConfig // taskID -> config (for running tasks)
}

// NewHandler creates a new CLI agent HTTP handler.
func NewHandler(executor *cliagent.MicroStepExecutor, registry *cliagent.CLIAgentRegistry, eventLogger *cliagent.EventLogger) *Handler {
	return &Handler{
		executor:    executor,
		registry:    registry,
		eventLogger: eventLogger,
		results:     make(map[string]*cliagent.TaskResult),
		pending:     make(map[string]*cliagent.TaskConfig),
	}
}

// createTaskRequest is the request body for POST /api/cli-agents/tasks.
type createTaskRequest struct {
	CLIBackend    string  `json:"cli_backend"`
	Model         string  `json:"model"`
	Prompt        string  `json:"prompt"`
	WorkingDir    string  `json:"working_dir"`
	TokenBudget   int     `json:"token_budget,omitempty"`
	CostBudgetUSD float64 `json:"cost_budget_usd,omitempty"`
	MaxSteps      int     `json:"max_steps,omitempty"`
}

// createTaskResponse is the response body for POST /api/cli-agents/tasks.
type createTaskResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

// HandleCreateTask handles POST /api/cli-agents/tasks.
func (h *Handler) HandleCreateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	config := cliagent.TaskConfig{
		CLIBackend:    req.CLIBackend,
		Model:         req.Model,
		Prompt:        req.Prompt,
		WorkingDir:    req.WorkingDir,
		TokenBudget:   req.TokenBudget,
		CostBudgetUSD: req.CostBudgetUSD,
		MaxSteps:      req.MaxSteps,
	}

	if err := config.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Launch task in background. The request context is canceled as soon as
	// this handler returns, so the background task must not inherit its
	// cancellation or it dies almost immediately.
	taskCtx := context.WithoutCancel(r.Context())
	go func() {
		result, err := h.executor.Execute(taskCtx, config)
		if err != nil {
			result = &cliagent.TaskResult{
				Status: cliagent.TaskFailed,
				Error:  err.Error(),
			}
		}
		if result != nil && result.TaskID != "" {
			h.mu.Lock()
			h.results[result.TaskID] = result
			delete(h.pending, result.TaskID)
			h.mu.Unlock()
		}
	}()

	// Return immediately with accepted status
	// Note: We don't have the taskID yet since Execute generates it internally.
	// For now we return a placeholder. A production implementation would use channels.
	writeJSON(w, http.StatusAccepted, createTaskResponse{
		Status: "accepted",
	})
}

// HandleGetTask handles GET /api/cli-agents/tasks/{id}.
func (h *Handler) HandleGetTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	taskID := extractTaskID(r.URL.Path)
	if taskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task ID required"})
		return
	}

	h.mu.RLock()
	result, found := h.results[taskID]
	h.mu.RUnlock()

	if !found {
		// Check if it's still running
		if h.executor.RunningCount() > 0 {
			writeJSON(w, http.StatusOK, map[string]any{
				"task_id": taskID,
				"status":  "running",
				"events":  h.eventLogger.GetEvents(taskID),
			})
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}

	resp := map[string]any{
		"task_id":        result.TaskID,
		"status":         result.Status,
		"summary":        result.Summary,
		"steps_executed": result.StepsExecuted,
		"total_usage":    result.TotalUsage,
		"files_changed":  result.FilesChanged,
		"duration":       result.Duration.String(),
		"events":         h.eventLogger.GetEvents(taskID),
	}
	if result.Error != "" {
		resp["error"] = result.Error
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleStopTask handles POST /api/cli-agents/tasks/{id}/stop.
func (h *Handler) HandleStopTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract task ID: path is /api/cli-agents/tasks/{id}/stop
	path := r.URL.Path
	path = strings.TrimSuffix(path, "/stop")
	taskID := extractTaskID(path)
	if taskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task ID required"})
		return
	}

	if h.executor.Stop(taskID) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "stopped", "task_id": taskID})
	} else {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found or not running"})
	}
}

// HandleListAgents handles GET /api/cli-agents.
func (h *Handler) HandleListAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agents := h.registry.List()
	writeJSON(w, http.StatusOK, map[string]any{
		"agents": agents,
	})
}

// extractTaskID gets the task ID from a URL path like /api/cli-agents/tasks/{id}.
func extractTaskID(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// Looking for pattern: api/cli-agents/tasks/{id}
	for i, part := range parts {
		if part == "tasks" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		_, _ = fmt.Fprintf(w, `{"error":"encode: %s"}`, err.Error())
	}
}
