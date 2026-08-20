package reaperhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/johnjallday/ori-agent/internal/reapersetup"
	"github.com/johnjallday/ori-agent/internal/toolapi"
)

var ErrAgentRuntimeGrantRequired = errors.New("the assigned agent does not have reaper_live_control access")

// AgentTools exposes the same catalog and run policy as the HTTP console. The
// workspace and stable agent instance are captured by trusted task execution;
// neither can be replaced by tool arguments from the model.
func (h *Handler) AgentTools(workspaceID, agentInstanceID string) []toolapi.Tool {
	workspaceID = strings.TrimSpace(workspaceID)
	agentInstanceID = strings.TrimSpace(agentInstanceID)
	if h == nil || workspaceID == "" || agentInstanceID == "" {
		return nil
	}
	return []toolapi.Tool{
		&listActionsTool{handler: h, workspaceID: workspaceID, agentInstanceID: agentInstanceID},
		&runActionTool{handler: h, workspaceID: workspaceID, agentInstanceID: agentInstanceID},
	}
}

func (h *Handler) agentHasLiveControlGrant(workspaceID, agentInstanceID string) bool {
	if h == nil || h.store == nil || strings.TrimSpace(agentInstanceID) == "" {
		return false
	}
	ws, err := h.store.GetFolderWorkspace(strings.TrimSpace(workspaceID))
	if err != nil || ws == nil || !runtimeSelectsLiveReaper(ws) {
		return false
	}
	state := ws.GetRuntimeState()
	return state != nil && state.HasActiveRuntimeGrant(reapersetup.ReaperLiveControlCapability, agentInstanceID)
}

type listActionsTool struct {
	handler         *Handler
	workspaceID     string
	agentInstanceID string
}

func (t *listActionsTool) Definition() toolapi.ToolDefinition {
	return toolapi.ToolDefinition{
		Name:        "list_reaper_actions",
		Description: "List the live REAPER action catalog for this workspace. Requires this agent instance's reaper_live_control grant.",
		Parameters: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}
}

func (t *listActionsTool) Call(_ context.Context, _ string) (string, error) {
	if t == nil || t.handler == nil || !t.handler.agentHasLiveControlGrant(t.workspaceID, t.agentInstanceID) {
		return "", ErrAgentRuntimeGrantRequired
	}
	actions, err := t.handler.listActions()
	if err != nil {
		return "", errors.New("REAPER actions could not be read")
	}
	return marshalToolResult(map[string]any{"actions": actions})
}

type runActionTool struct {
	handler         *Handler
	workspaceID     string
	agentInstanceID string
}

func (t *runActionTool) Definition() toolapi.ToolDefinition {
	return toolapi.ToolDefinition{
		Name:        "run_reaper_action",
		Description: "Run one ID from list_reaper_actions, or a validated raw command ID. Project-mutating and raw commands require confirmed=true only after the user explicitly confirms that exact action. Requires this agent instance's reaper_live_control grant.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action_id": map[string]any{"type": "string", "description": "Catalog ID, decimal command ID, or _RS hexadecimal named command ID."},
				"confirmed": map[string]any{"type": "boolean", "description": "True only when the user explicitly confirmed this exact project-changing action."},
			},
			"required":             []string{"action_id"},
			"additionalProperties": false,
		},
	}
}

func (t *runActionTool) Call(ctx context.Context, args string) (string, error) {
	if t == nil || t.handler == nil || !t.handler.agentHasLiveControlGrant(t.workspaceID, t.agentInstanceID) {
		return "", ErrAgentRuntimeGrantRequired
	}
	var request struct {
		ActionID  string `json:"action_id"`
		Confirmed bool   `json:"confirmed"`
	}
	decoder := json.NewDecoder(strings.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || strings.TrimSpace(request.ActionID) == "" {
		return "", errors.New("action_id is required")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", errors.New("invalid REAPER action arguments")
	}
	response, status := t.handler.runAction(ctx, t.workspaceID, request.ActionID, request.Confirmed)
	result, err := marshalToolResult(response)
	if err != nil {
		return "", err
	}
	if status >= 500 {
		return result, fmt.Errorf("REAPER action failed: %s", response.ErrorReason)
	}
	return result, nil
}

func marshalToolResult(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("REAPER tool result could not be encoded")
	}
	return string(encoded), nil
}
