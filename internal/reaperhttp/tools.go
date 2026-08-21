package reaperhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/johnjallday/ori-agent/internal/reaper"
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
		&proposeScriptTool{handler: h, workspaceID: workspaceID, agentInstanceID: agentInstanceID},
		&proposeTrackEditsTool{handler: h, workspaceID: workspaceID, agentInstanceID: agentInstanceID},
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

type proposeScriptTool struct {
	handler         *Handler
	workspaceID     string
	agentInstanceID string
}

func (t *proposeScriptTool) Definition() toolapi.ToolDefinition {
	return toolapi.ToolDefinition{
		Name:        "propose_reaper_script",
		Description: "Propose readable Lua and metadata for user review. This creates a workspace proposal only; it cannot save to the global script library or add anything to the action catalog.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filename":           map[string]any{"type": "string", "description": "Safe .lua filename."},
				"name":               map[string]any{"type": "string", "description": "Human-readable script name."},
				"description":        map[string]any{"type": "string", "description": "What the script changes."},
				"needs_confirmation": map[string]any{"type": "boolean", "description": "Whether a draft or saved run needs user confirmation."},
				"code":               map[string]any{"type": "string", "description": "Complete zero-argument REAPER Lua source."},
			},
			"required":             []string{"filename", "name", "description", "needs_confirmation", "code"},
			"additionalProperties": false,
		},
	}
}

func (t *proposeScriptTool) Call(_ context.Context, args string) (string, error) {
	if t == nil || t.handler == nil || !t.handler.agentHasLiveControlGrant(t.workspaceID, t.agentInstanceID) {
		return "", ErrAgentRuntimeGrantRequired
	}
	var input reaper.ScriptInput
	decoder := json.NewDecoder(strings.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return "", errors.New("invalid REAPER script proposal")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", errors.New("invalid REAPER script proposal")
	}
	proposal, err := t.handler.proposeScript(t.workspaceID, t.agentInstanceID, input)
	if err != nil {
		return "", err
	}
	return marshalToolResult(map[string]any{
		"outcome": "proposed", "proposal": proposal,
		"next": "The user can review, run this as a draft, then explicitly save or discard it.",
	})
}

func marshalToolResult(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("REAPER tool result could not be encoded")
	}
	return string(encoded), nil
}

type proposeTrackEditsTool struct {
	handler         *Handler
	workspaceID     string
	agentInstanceID string
}

func (t *proposeTrackEditsTool) Definition() toolapi.ToolDefinition {
	return toolapi.ToolDefinition{
		Name: "propose_reaper_track_edits",
		Description: "Propose a set of track edits (rename, color, mute, solo, arm, or move) as a single reviewable plan. " +
			"This creates a pending plan for the workspace only — it never applies anything. The user reviews the whole " +
			"plan as one card and applies or cancels it themselves; this tool cannot apply a plan. Every edit is guarded " +
			"on the track's expected current name, exactly like a direct edit: if a track's name no longer matches when " +
			"the user applies the plan, the whole plan is refused rather than risking the wrong track.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"edits": map[string]any{
					"type":        "array",
					"description": "One to 64 edits. Renames and colors execute before moves within the plan.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"index":         map[string]any{"type": "integer", "description": "1-based track position, matching the console."},
							"expected_name": map[string]any{"type": "string", "description": "The name Ori currently believes this track has. The empty string means unnamed."},
							"operation":     map[string]any{"type": "string", "enum": []string{"rename", "color", "mute", "solo", "arm", "move"}},
							"new_value": map[string]any{
								"description": "rename: new name (string). color: one of " + strings.Join(reaper.NamedColorNames(), ", ") +
									" (string). mute/solo/arm: true or false (boolean). move: target 1-based position (integer).",
							},
						},
						"required":             []string{"index", "expected_name", "operation", "new_value"},
						"additionalProperties": false,
					},
				},
			},
			"required":             []string{"edits"},
			"additionalProperties": false,
		},
	}
}

type proposedTrackEdit struct {
	Index        int             `json:"index"`
	ExpectedName string          `json:"expected_name"`
	Operation    string          `json:"operation"`
	NewValue     json.RawMessage `json:"new_value"`
}

func (e proposedTrackEdit) toTrackEdit() (reaper.TrackEdit, error) {
	switch e.Operation {
	case reaper.TrackEditRename:
		var name string
		if err := json.Unmarshal(e.NewValue, &name); err != nil {
			return reaper.TrackEdit{}, errors.New("rename new_value must be a string")
		}
		return reaper.RenameEdit(e.Index, e.ExpectedName, name), nil
	case reaper.TrackEditColor:
		var name string
		if err := json.Unmarshal(e.NewValue, &name); err != nil {
			return reaper.TrackEdit{}, errors.New("color new_value must be a string")
		}
		color, ok := reaper.NamedColor(name)
		if !ok {
			return reaper.TrackEdit{}, fmt.Errorf("color must be one of: %s", strings.Join(reaper.NamedColorNames(), ", "))
		}
		return reaper.ColorEdit(e.Index, e.ExpectedName, color), nil
	case reaper.TrackEditMute, reaper.TrackEditSolo, reaper.TrackEditArm:
		var value bool
		if err := json.Unmarshal(e.NewValue, &value); err != nil {
			return reaper.TrackEdit{}, errors.New("mute/solo/arm new_value must be a boolean")
		}
		switch e.Operation {
		case reaper.TrackEditMute:
			return reaper.MuteEdit(e.Index, e.ExpectedName, value), nil
		case reaper.TrackEditSolo:
			return reaper.SoloEdit(e.Index, e.ExpectedName, value), nil
		default:
			return reaper.ArmEdit(e.Index, e.ExpectedName, value), nil
		}
	case reaper.TrackEditMove:
		var target int
		if err := json.Unmarshal(e.NewValue, &target); err != nil {
			return reaper.TrackEdit{}, errors.New("move new_value must be an integer position")
		}
		return reaper.MoveEdit(e.Index, e.ExpectedName, target), nil
	default:
		return reaper.TrackEdit{}, errors.New("operation must be one of: rename, color, mute, solo, arm, move")
	}
}

func (t *proposeTrackEditsTool) Call(_ context.Context, args string) (string, error) {
	if t == nil || t.handler == nil || !t.handler.agentHasLiveControlGrant(t.workspaceID, t.agentInstanceID) {
		return "", ErrAgentRuntimeGrantRequired
	}
	var request struct {
		Edits []proposedTrackEdit `json:"edits"`
	}
	decoder := json.NewDecoder(strings.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || len(request.Edits) == 0 {
		return "", errors.New("edits is required and must not be empty")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", errors.New("invalid track-edit plan arguments")
	}
	edits := make([]reaper.TrackEdit, 0, len(request.Edits))
	for i, proposed := range request.Edits {
		edit, err := proposed.toTrackEdit()
		if err != nil {
			return "", fmt.Errorf("edit %d: %w", i+1, err)
		}
		edits = append(edits, edit)
	}
	plan, err := t.handler.proposeEdits(t.workspaceID, t.agentInstanceID, edits)
	if err != nil {
		return "", err
	}
	return marshalToolResult(map[string]any{
		"outcome": "proposed", "plan_id": plan.ID, "edit_count": len(plan.Edits),
		"next": "The user reviews the plan card and applies or cancels it themselves. Nothing has been applied.",
	})
}
