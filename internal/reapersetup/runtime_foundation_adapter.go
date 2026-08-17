package reapersetup

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const ReaperLiveControlCapability = "reaper_live_control"

var ErrRuntimeGrantUnavailable = errors.New("REAPER runtime grant prerequisites are unavailable")

// CLIAgentProviderResolver resolves provider identity from trusted agent stores,
// never from the grant request body or task prompt.
type CLIAgentProviderResolver interface {
	ProviderForAgent(context.Context, string, workspace.AgentInstance) (provider string, isCLI bool, err error)
}

// RuntimeFoundationAdapter reserves the REAPER runtime adapter ID in Delivery
// PR 1 and owns its narrow grant policy. Durable/live REAPER evaluation replaces
// this foundation in Delivery PR 2 without changing the manifest key.
type RuntimeFoundationAdapter struct {
	agents CLIAgentProviderResolver
	roots  RunnerRootResolver
}

func NewRuntimeFoundationAdapter(agents CLIAgentProviderResolver, roots RunnerRootResolver) *RuntimeFoundationAdapter {
	return &RuntimeFoundationAdapter{agents: agents, roots: roots}
}

func (a *RuntimeFoundationAdapter) ID() string { return ReaperLiveControlCapability }

func (a *RuntimeFoundationAdapter) EvaluateDurable(context.Context, runtimecapability.EvaluationRequest) (runtimecapability.DurableResult, error) {
	return runtimecapability.DurableResult{
		State:      runtimecapability.DurableInProgress,
		ReasonCode: runtimecapability.ReasonAdapterUnavailable,
		Summary:    "Guided REAPER runtime checks are unavailable in this build.",
	}, nil
}

func (a *RuntimeFoundationAdapter) ValidateGrant(ctx context.Context, request runtimecapability.GrantValidationRequest) error {
	if a == nil || a.agents == nil || a.roots == nil || request.Requirement.Key != ReaperLiveControlCapability || request.Requirement.Adapter != ReaperLiveControlCapability {
		return ErrRuntimeGrantUnavailable
	}
	provider, isCLI, err := a.agents.ProviderForAgent(ctx, request.WorkspaceID, request.Agent)
	if err != nil || !isCLI {
		return ErrRuntimeGrantUnavailable
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex", "claude_code":
	default:
		return ErrRuntimeGrantUnavailable
	}
	if _, err := a.roots.Resolve(); err != nil {
		return ErrRuntimeGrantUnavailable
	}
	return nil
}

func (a *RuntimeFoundationAdapter) ResolveExecutionScope(ctx context.Context, request runtimecapability.ExecutionScopeRequest) (runtimecapability.CapabilityExecutionScope, error) {
	validation := runtimecapability.GrantValidationRequest(request)
	if err := a.ValidateGrant(ctx, validation); err != nil {
		return runtimecapability.CapabilityExecutionScope{}, ErrRuntimeGrantUnavailable
	}
	root, err := a.roots.Resolve()
	if err != nil {
		return runtimecapability.CapabilityExecutionScope{}, ErrRuntimeGrantUnavailable
	}
	return runtimecapability.CapabilityExecutionScope{
		AdditionalWritableRoots: []string{root},
		NetworkPosture:          runtimecapability.CapabilityNetworkLocal,
		// Delivery PR 2 registers the exact trusted REAPER helper/MCP server.
		// Empty in the foundation means no prompt-selected MCP is exposed.
		AllowedMCPServers: nil,
	}, nil
}
