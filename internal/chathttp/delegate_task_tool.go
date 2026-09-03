package chathttp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// isWorkspaceCoordinator reports whether the executing agent is the workspace
// coordinator (entry agent). It is the gate that structurally enforces
// single-level delegation — specialists never receive delegate_task, so a
// delegated specialist cannot delegate again.
func (p *WorkspaceToolProvider) isWorkspaceCoordinator() bool {
	_, ok := p.coordinatorRoster()
	return ok
}

// coordinatorRoster returns the workspace roster when the executing agent is
// the coordinator. The specialist list is what the delegate_task schema is
// built from, so it is resolved here rather than left for the model to guess.
func (p *WorkspaceToolProvider) coordinatorRoster() (workspace.CoordinatorRoster, bool) {
	if p.workspaceStore == nil || p.executingAgent == "" {
		return workspace.CoordinatorRoster{}, false
	}
	ws, err := p.workspaceStore.Get(p.workspaceID)
	if err != nil || ws == nil {
		return workspace.CoordinatorRoster{}, false
	}
	roster := ws.BuildCoordinatorRoster()
	if roster.CoordinatorSource == workspace.CoordinatorSourceMissing {
		return workspace.CoordinatorRoster{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(roster.Coordinator), p.executingAgent) {
		return workspace.CoordinatorRoster{}, false
	}
	return roster, true
}

// delegationSpecialists returns the agents delegate_task may target, and
// whether the tool should be exposed at all.
//
// A coordinator with no specialists does not get the tool: every call it could
// make would be rejected by the membership check in agentcomm.DelegateTask, so
// offering it only costs schema tokens and invites a failed call plus a retry
// turn. This is the solo-workspace case, where the coordinator is resolved by
// CoordinatorSourceSingleAgentDefault.
func (p *WorkspaceToolProvider) delegationSpecialists() ([]string, bool) {
	roster, ok := p.coordinatorRoster()
	if !ok || len(roster.Specialists) == 0 {
		return nil, false
	}
	return roster.Specialists, true
}

// delegateTaskTool lets the coordinator hand a subtask to a workspace specialist.
// The subtask is persisted with dynamic_delegation provenance and a parent link
// (via agentcomm.DelegateTask) before it is executed by the delegation loop.
//
// specialists is the workspace roster, and is bound into the schema as the
// enum of the "agent" argument: the model picks from the agents that actually
// exist instead of guessing a name and burning a turn on the rejection.
func (p *WorkspaceToolProvider) delegateTaskTool(specialists []string) toolapi.Tool {
	return &nativeUtilityTool{
		definition: toolapi.ToolDefinition{
			Name: "delegate_task",
			Description: fmt.Sprintf(
				"Delegate a subtask to a workspace specialist agent. Only the workspace coordinator can call this. "+
					"The subtask is created under the current task and assigned to the chosen specialist; use it when a step needs an agent other than yourself. "+
					"Specialists available in this workspace: %s.",
				strings.Join(specialists, ", "),
			),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{
						"type":        "string",
						"description": "Name of the workspace specialist agent to assign the subtask to. Must be one of the workspace's current specialists.",
						"enum":        specialists,
					},
					"instructions": map[string]any{
						"type":        "string",
						"description": "Clear, self-contained instructions for the delegated subtask.",
					},
					"inputs": map[string]any{
						"type":        "object",
						"description": "Optional structured inputs (e.g. prior task outputs) to pass to the specialist.",
					},
					"parent_task_id": map[string]any{
						"type":        "string",
						"description": "Task that triggered the delegation. Defaults to the current task when omitted.",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Short explanation of why this specialist was chosen.",
					},
				},
				"required": []any{"agent", "instructions"},
			},
		},
		call: func(ctx context.Context, args string) (string, error) {
			var in struct {
				Agent        string         `json:"agent"`
				Instructions string         `json:"instructions"`
				Inputs       map[string]any `json:"inputs"`
				ParentTaskID string         `json:"parent_task_id"`
				Reason       string         `json:"reason"`
			}
			if strings.TrimSpace(args) != "" {
				if err := json.Unmarshal([]byte(args), &in); err != nil {
					return "", fmt.Errorf("invalid delegate_task arguments: %w", err)
				}
			}

			agentName := strings.TrimSpace(in.Agent)
			instructions := strings.TrimSpace(in.Instructions)
			if agentName == "" || instructions == "" {
				return marshalToolResponse(map[string]any{
					"error": "both 'agent' and 'instructions' are required",
				})
			}

			ws, err := p.workspaceStore.Get(p.workspaceID)
			if err != nil {
				return "", fmt.Errorf("workspace not found: %w", err)
			}
			coordinator, source := ws.ResolveCoordinator()
			if source == workspace.CoordinatorSourceMissing {
				return marshalToolResponse(map[string]any{
					"error": "workspace has no coordinator (entry agent); cannot delegate",
				})
			}

			parentID := strings.TrimSpace(in.ParentTaskID)
			if parentID == "" {
				parentID = p.taskID
			}
			reason := strings.TrimSpace(in.Reason)
			if reason == "" {
				reason = fmt.Sprintf("delegated by %s", coordinator)
			}

			comm := agentcomm.NewCommunicator(p.workspaceStore)
			task, err := comm.DelegateTask(agentcomm.DelegationRequest{
				WorkspaceID:  p.workspaceID,
				From:         coordinator,
				To:           agentName,
				Description:  instructions,
				Context:      in.Inputs,
				ParentTaskID: parentID,
				Reason:       reason,
			})
			if err != nil {
				return marshalToolResponse(map[string]any{
					"error": err.Error(),
				})
			}

			// Status is "assigned": the subtask is persisted and ready. Synchronous
			// execution and the result / needs_input / cap_hit carriers are added by
			// the delegation loop (task 4.5).
			return marshalToolResponse(map[string]any{
				"delegated_task_id": task.ID,
				"status":            string(task.Status),
				"assigned_to":       task.To,
				"assigned_by":       task.AssignedBy,
				"parent_task_id":    task.ParentTaskID,
				"assignment_mode":   string(task.AssignmentMode),
			})
		},
	}
}
