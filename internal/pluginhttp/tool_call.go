package pluginhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ToolCallRequest represents a direct tool call request
type ToolCallRequest struct {
	PluginName string                 `json:"plugin_name"`
	Operation  string                 `json:"operation"`
	Args       map[string]interface{} `json:"args"`
}

// ToolCallResponse represents a direct tool call response
type ToolCallResponse struct {
	Success bool   `json:"success"`
	Result  string `json:"result"`
	Error   string `json:"error,omitempty"`
}

// DirectToolCallHandler handles direct plugin tool calls without going through OpenAI
func (h *Handler) DirectToolCallHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	var req ToolCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(ToolCallResponse{
			Success: false,
			Error:   fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	// Get current agent
	names, current := h.State.ListAgents()
	if current == "" && len(names) > 0 {
		current = names[0]
	}

	agent, ok := h.State.GetAgent(current)
	if !ok {
		json.NewEncoder(w).Encode(ToolCallResponse{
			Success: false,
			Error:   "Current agent not found",
		})
		return
	}

	// Normalize plugin name: OpenAI function names use underscores, but plugin names use hyphens
	// Convert ori_reaper -> ori-reaper
	pluginName := strings.ReplaceAll(req.PluginName, "_", "-")

	// Find the plugin using normalized name
	plugin, exists := agent.Plugins[pluginName]
	if !exists || plugin.Tool == nil {
		json.NewEncoder(w).Encode(ToolCallResponse{
			Success: false,
			Error:   fmt.Sprintf("Plugin %q not found or not loaded", pluginName),
		})
		return
	}

	// Build arguments JSON
	req.Args["operation"] = req.Operation
	argsJSON, err := json.Marshal(req.Args)
	if err != nil {
		json.NewEncoder(w).Encode(ToolCallResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to marshal arguments: %v", err),
		})
		return
	}

	// Call the plugin tool directly with timeout
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Track plugin call stats
	startTime := time.Now()
	result, err := plugin.Tool.Call(ctx, string(argsJSON))
	duration := time.Since(startTime)

	// Record call stats in health manager
	if h.HealthManager != nil {
		if err != nil {
			h.HealthManager.RecordCallFailure(pluginName, duration, err)
		} else {
			h.HealthManager.RecordCallSuccess(pluginName, duration)
		}
	}

	if err != nil {
		json.NewEncoder(w).Encode(ToolCallResponse{
			Success: false,
			Error:   fmt.Sprintf("Tool call failed: %v", err),
		})
		return
	}

	// Success
	json.NewEncoder(w).Encode(ToolCallResponse{
		Success: true,
		Result:  result,
	})
}
