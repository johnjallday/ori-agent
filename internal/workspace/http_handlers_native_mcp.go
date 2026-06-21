package workspace

import (
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// isNativeMCPCLIProvider reports whether a provider runs MCP tools natively via
// its CLI (the only providers the native-MCP opt-in is meaningful for).
func isNativeMCPCLIProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex", "claude_code":
		return true
	default:
		return false
	}
}

type nativeMCPAgentStatus struct {
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	IsCLIProvider bool   `json:"is_cli_provider"`
	Enabled       bool   `json:"enabled"`
}

type nativeMCPSettingsResponse struct {
	WorkspaceEnabled bool                   `json:"workspace_enabled"`
	Agents           []nativeMCPAgentStatus `json:"agents"`
}

type nativeMCPToggleRequest struct {
	Enabled bool `json:"enabled"`
}

// GetNativeMCPSettings handles GET /api/workspaces/{workspaceID}/native-mcp and
// returns the workspace opt-in plus per-agent opt-in state (flagging which
// agents are CLI providers the toggle actually affects).
func (h *HTTPHandler) GetNativeMCPSettings(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}
	ws, err := h.store.Get(workspaceID)
	if err != nil {
		orihttp.NotFound(w, "Workspace not found")
		return
	}

	resp := nativeMCPSettingsResponse{WorkspaceEnabled: ws.AllowNativeMCPCLI}
	seen := make(map[string]bool)
	for _, inst := range ws.AgentInstances {
		name := strings.TrimSpace(inst.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		status := nativeMCPAgentStatus{Name: name}
		if ag, ok, aerr := h.store.GetWorkspaceAgent(workspaceID, name); aerr == nil && ok && ag != nil {
			status.Provider = ag.Settings.Provider
			status.IsCLIProvider = isNativeMCPCLIProvider(ag.Settings.Provider)
			status.Enabled = ag.Settings.IsNativeMCPToolsAllowed()
		}
		resp.Agents = append(resp.Agents, status)
	}
	orihttp.WriteJSON(w, resp)
}

// UpdateNativeMCPWorkspace handles PATCH /api/workspaces/{workspaceID}/native-mcp
// and toggles the workspace-level opt-in.
func (h *HTTPHandler) UpdateNativeMCPWorkspace(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	if workspaceID == "" {
		orihttp.BadRequest(w, "workspace ID is required")
		return
	}
	var req nativeMCPToggleRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if err := h.store.Update(workspaceID, func(ws *Workspace) error {
		ws.AllowNativeMCPCLI = req.Enabled
		return nil
	}); err != nil {
		orihttp.InternalError(w, "Failed to update workspace: "+err.Error())
		return
	}
	logger.Info("Updated workspace native-MCP opt-in", logger.Fields{
		"workspace_id": workspaceID,
		"enabled":      req.Enabled,
	})
	orihttp.WriteJSON(w, map[string]any{"workspace_enabled": req.Enabled})
}

// UpdateNativeMCPAgent handles PATCH
// /api/workspaces/{workspaceID}/agents/{name}/native-mcp and toggles a
// workspace-local agent's opt-in.
func (h *HTTPHandler) UpdateNativeMCPAgent(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.PathValue("workspaceID"))
	agentName := strings.TrimSpace(r.PathValue("name"))
	if workspaceID == "" || agentName == "" {
		orihttp.BadRequest(w, "workspace ID and agent name are required")
		return
	}
	// Opt-in is per agent name (slug), shared across instances; drop any suffix.
	if idx := strings.Index(agentName, ":"); idx >= 0 {
		agentName = strings.TrimSpace(agentName[:idx])
	}

	var req nativeMCPToggleRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if _, err := h.store.Get(workspaceID); err != nil {
		orihttp.NotFound(w, "Workspace not found")
		return
	}
	ag, ok, err := h.store.GetWorkspaceAgent(workspaceID, agentName)
	if err != nil {
		orihttp.InternalError(w, "Failed to read workspace agent: "+err.Error())
		return
	}
	if !ok || ag == nil {
		orihttp.NotFound(w, "Workspace agent not found")
		return
	}

	enabled := req.Enabled
	ag.Settings.AllowNativeMCPTools = &enabled
	if err := h.store.SaveWorkspaceAgent(workspaceID, agentName, ag); err != nil {
		orihttp.InternalError(w, "Failed to save workspace agent: "+err.Error())
		return
	}
	logger.Info("Updated agent native-MCP opt-in", logger.Fields{
		"workspace_id": workspaceID,
		"agent":        agentName,
		"enabled":      enabled,
	})
	orihttp.WriteJSON(w, map[string]any{"agent": agentName, "enabled": enabled})
}
