package workspacesurfacehttp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// AgentRuntimeService is Ori's host-owned mode/readiness/grant service. Plugin
// code cannot implement or select it through a workspace record.
type AgentRuntimeService interface {
	Status(context.Context, string) (runtimecapability.Status, error)
}

func (h *Handler) SetAgentRuntimeService(service AgentRuntimeService) {
	if h != nil {
		h.runtime = service
	}
}

type AgentOperationRequest struct {
	WorkspaceID       string
	AgentInstanceID   string
	PluginID          string
	CapabilityID      string
	OperationID       string
	Input             json.RawMessage
	ConfirmationToken string
}

type AgentOperationResult struct {
	Output         json.RawMessage
	ConfirmationID string
}

type AgentOperationError struct {
	Code    string
	Message string
}

func (e *AgentOperationError) Error() string { return e.Code + ": " + e.Message }

type agentOperationTool struct {
	handler    *Handler
	request    AgentOperationRequest
	definition toolapi.ToolDefinition
}

func (t *agentOperationTool) Definition() toolapi.ToolDefinition { return t.definition }
func (t *agentOperationTool) Call(ctx context.Context, arguments string) (string, error) {
	input := json.RawMessage(strings.TrimSpace(arguments))
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	request := t.request
	request.Input = input
	result, err := t.handler.InvokeAgentOperation(ctx, request)
	if err != nil {
		return "", err
	}
	return string(result.Output), nil
}

// AgentTools exposes only declared capability operations for this exact
// workspace agent. Calls still repeat every authorization check, so a grant
// revoked after tool-list construction fails closed.
func (h *Handler) AgentTools(ctx context.Context, workspaceID, agentInstanceID string) []toolapi.Tool {
	if h == nil || h.registry == nil || h.workspaces == nil {
		return nil
	}
	_, owned := h.ownedWorkspace(ctx, workspaceID)
	ws, err := h.workspaces.Get(workspaceID)
	if !owned || err != nil || ws == nil || !workspaceHasAgent(ws, agentInstanceID) {
		return nil
	}
	seen := make(map[string]struct{})
	var tools []toolapi.Tool
	for _, surface := range h.registry.Surfaces() {
		if surface.Owner.Kind != workspacesurface.OwnerPlugin || !surface.Available || len(surface.Capability.AgentOperationIDs) == 0 ||
			h.attachments == nil || !h.attachments.Attached(ctx, workspaceID, surface) {
			continue
		}
		if key := surface.Capability.RuntimeRequirementKey; key != "" {
			state := ws.GetRuntimeState()
			if state == nil || !state.HasActiveRuntimeGrant(key, agentInstanceID) || h.runtime == nil {
				continue
			}
			status, statusErr := h.runtime.Status(ctx, workspaceID)
			if statusErr != nil || !runtimeRequirementHealthy(status, key) {
				continue
			}
		}
		binding, ok := h.registry.Binding(surface.Key)
		if !ok {
			continue
		}
		for _, operationID := range surface.Capability.AgentOperationIDs {
			operation, declared := binding.Operations[operationID]
			toolName := agentToolName(surface.Owner.ID, surface.Capability.ID, operationID)
			if !declared || toolName == "" {
				continue
			}
			if _, duplicate := seen[toolName]; duplicate {
				continue
			}
			seen[toolName] = struct{}{}
			parameters := map[string]any{}
			if err := json.Unmarshal(operation.InputSchema, &parameters); err != nil {
				continue
			}
			description := "Run " + operationID + " from " + surface.Capability.Display.Name + "."
			if operation.Policy == workspacesurface.PolicyConfirmationRequired {
				description += " Ori requires host review before this operation runs."
			}
			tools = append(tools, &agentOperationTool{
				handler: h,
				request: AgentOperationRequest{
					WorkspaceID: workspaceID, AgentInstanceID: agentInstanceID,
					PluginID: surface.Owner.ID, CapabilityID: surface.Capability.ID, OperationID: operationID,
				},
				definition: toolapi.ToolDefinition{Name: toolName, Description: description, Parameters: parameters},
			})
		}
	}
	return tools
}

func agentToolName(pluginID, capabilityID, operationID string) string {
	value := "plugin_" + pluginID + "_" + capabilityID + "_" + operationID
	value = strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(value))
	if len(value) > 120 {
		return ""
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return ""
		}
	}
	return value
}

var _ toolapi.Tool = (*agentOperationTool)(nil)

type resolvedAgentOperation struct {
	userID       string
	workspace    *workspace.Workspace
	surface      workspacesurface.RegisteredSurface
	binding      workspacesurface.Binding
	operation    workspacesurface.Operation
	context      workspacesurface.WorkspaceContext
	confirmation confirmationBinding
}

// InvokeAgentOperation is the generic agent-operation adapter. Callers expose
// individual declared operations as tools; the private plugin MCP service is
// never attached wholesale to an agent or workspace.
func (h *Handler) InvokeAgentOperation(ctx context.Context, request AgentOperationRequest) (AgentOperationResult, error) {
	resolved, err := h.resolveAgentOperation(ctx, request)
	if err != nil {
		return AgentOperationResult{}, err
	}
	token := strings.TrimSpace(request.ConfirmationToken)
	if resolved.operation.Policy != workspacesurface.PolicyConfirmationRequired && token != "" {
		return AgentOperationResult{}, agentError("confirmation_invalid", "That plugin confirmation is not valid.")
	}
	if resolved.operation.Policy == workspacesurface.PolicyConfirmationRequired {
		if token == "" {
			id, issueErr := h.confirmations.issue(resolved.confirmation, request.Input)
			if issueErr != nil {
				return AgentOperationResult{}, agentError("confirmation_unavailable", "This plugin action could not be prepared for confirmation.")
			}
			return AgentOperationResult{ConfirmationID: id}, agentError("confirmation_required", "Review and confirm this plugin action before it runs.")
		}
		if consumeErr := h.confirmations.consume(token, resolved.confirmation, request.Input); consumeErr != nil {
			return AgentOperationResult{}, agentError("confirmation_invalid", "That plugin confirmation is not valid.")
		}
	}
	callContext, cancel := context.WithTimeout(ctx, h.operationTimeout(resolved.operation.Timeout))
	defer cancel()
	result, err := resolved.binding.Runtime.Invoke(callContext, workspacesurface.Invocation{
		Workspace: resolved.context, Operation: request.OperationID, Input: append(json.RawMessage(nil), request.Input...),
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(callContext.Err(), context.DeadlineExceeded) {
			return AgentOperationResult{}, agentError("service_timeout", "The plugin service did not answer in time.")
		}
		return AgentOperationResult{}, agentError("service_unavailable", "The plugin service could not complete that operation.")
	}
	if len(result.Output) == 0 || len(result.Output) > resolved.operation.MaxOutputBytes || !json.Valid(result.Output) ||
		workspacesurface.ValidateOperationOutput(resolved.operation, result.Output) != nil {
		return AgentOperationResult{}, agentError("output_invalid", "The plugin service returned an invalid result.")
	}
	return AgentOperationResult{Output: append(json.RawMessage(nil), result.Output...)}, nil
}

// ApproveAgentOperationConfirmation is called by host review UI/orchestration,
// never by model-authored tool input. The opaque token stays in the adapter and
// is used only to retry the exact normalized request.
func (h *Handler) ApproveAgentOperationConfirmation(ctx context.Context, request AgentOperationRequest, confirmationID string) (string, error) {
	resolved, err := h.resolveAgentOperation(ctx, request)
	if err != nil {
		return "", err
	}
	token, err := h.confirmations.approve(strings.TrimSpace(confirmationID), resolved.confirmation)
	if err != nil {
		return "", agentError("confirmation_invalid", "That plugin confirmation is not valid.")
	}
	return token, nil
}

func (h *Handler) resolveAgentOperation(ctx context.Context, request AgentOperationRequest) (resolvedAgentOperation, error) {
	if h == nil || h.registry == nil || h.workspaces == nil {
		return resolvedAgentOperation{}, agentError("provider_unavailable", runtimecapability.ProviderUnavailableMessage)
	}
	workspaceID := strings.TrimSpace(request.WorkspaceID)
	userID, owned := h.ownedWorkspace(ctx, workspaceID)
	if !owned {
		return resolvedAgentOperation{}, agentError("workspace_not_found", "Workspace not found.")
	}
	ws, err := h.workspaces.Get(workspaceID)
	if err != nil || ws == nil || !workspaceHasAgent(ws, request.AgentInstanceID) {
		return resolvedAgentOperation{}, agentError("agent_unavailable", "That workspace agent is not available.")
	}
	pluginID := strings.ToLower(strings.TrimSpace(request.PluginID))
	capabilityID := strings.ToLower(strings.TrimSpace(request.CapabilityID))
	operationID := strings.TrimSpace(request.OperationID)
	var selected workspacesurface.RegisteredSurface
	var binding workspacesurface.Binding
	for _, surface := range h.registry.SurfacesForOwner(workspacesurface.OwnerPlugin, pluginID) {
		if surface.Capability.ID != capabilityID || !contains(surface.Capability.AgentOperationIDs, operationID) || !surface.Available {
			continue
		}
		if h.attachments == nil || !h.attachments.Attached(ctx, workspaceID, surface) {
			continue
		}
		candidate, ok := h.registry.Binding(surface.Key)
		if !ok {
			continue
		}
		operation, ok := candidate.Operations[operationID]
		if !ok || operation.ID != operationID {
			continue
		}
		selected, binding = surface, candidate
		break
	}
	if selected.Key == "" {
		return resolvedAgentOperation{}, agentError("operation_unknown", "That plugin agent operation is not available.")
	}
	operation := binding.Operations[operationID]
	if err := workspacesurface.ValidateOperationInput(operation, request.Input); err != nil {
		return resolvedAgentOperation{}, agentError("input_invalid", "The plugin operation input is invalid.")
	}
	if requirementKey := selected.Capability.RuntimeRequirementKey; requirementKey != "" {
		state := ws.GetRuntimeState()
		if state == nil || !state.HasActiveRuntimeGrant(requirementKey, strings.TrimSpace(request.AgentInstanceID)) {
			return resolvedAgentOperation{}, agentError("runtime_grant_required", "This operation requires an authorized runtime grant for this agent.")
		}
		if h.runtime == nil {
			return resolvedAgentOperation{}, agentError("provider_unavailable", runtimecapability.ProviderUnavailableMessage)
		}
		status, statusErr := h.runtime.Status(ctx, workspaceID)
		if statusErr != nil || !runtimeRequirementHealthy(status, requirementKey) {
			return resolvedAgentOperation{}, agentError("provider_unavailable", runtimecapability.ProviderUnavailableMessage)
		}
	}
	workspaceContext, err := h.resolveContext(ctx, workspaceID, selected)
	if err != nil {
		return resolvedAgentOperation{}, agentError("provider_unavailable", runtimecapability.ProviderUnavailableMessage)
	}
	workspaceContext.WorkspaceID = workspaceID
	confirmation := confirmationBinding{
		UserID: userID, WorkspaceID: workspaceID, PluginID: selected.Owner.ID,
		Generation: selected.Owner.Generation, CapabilityID: capabilityID,
		CallerID: "agent:" + strings.TrimSpace(request.AgentInstanceID), OperationID: operationID,
	}
	return resolvedAgentOperation{
		userID: userID, workspace: ws, surface: selected, binding: binding,
		operation: operation, context: workspaceContext, confirmation: confirmation,
	}, nil
}

func workspaceHasAgent(ws *workspace.Workspace, agentInstanceID string) bool {
	want := strings.TrimSpace(agentInstanceID)
	if want == "" {
		return false
	}
	for _, agent := range ws.AgentInstances {
		if strings.TrimSpace(agent.ID) == want {
			return true
		}
	}
	return false
}

func runtimeRequirementHealthy(status runtimecapability.Status, key string) bool {
	for _, requirement := range status.Requirements {
		if requirement.Key != key || requirement.DurableState != runtimecapability.DurableConfigured {
			continue
		}
		return requirement.LiveState == runtimecapability.LiveAvailable || requirement.LiveState == runtimecapability.LiveNotChecked || requirement.LiveState == runtimecapability.LiveNotApplicable
	}
	return false
}

func agentError(code, message string) error {
	return &AgentOperationError{Code: code, Message: message}
}
