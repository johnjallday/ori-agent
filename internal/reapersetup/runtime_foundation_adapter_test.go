package reapersetup

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fixedGrantAgentProvider struct {
	provider string
	isCLI    bool
	err      error
}

func (p fixedGrantAgentProvider) ProviderForAgent(context.Context, string, workspace.AgentInstance) (string, bool, error) {
	return p.provider, p.isCLI, p.err
}

type fixedRunnerRoot struct{ err error }

func (r fixedRunnerRoot) Resolve() (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return "/trusted/runner", nil
}

func grantValidationRequest() runtimecapability.GrantValidationRequest {
	return runtimecapability.GrantValidationRequest{
		WorkspaceID: "ws-1",
		Mode:        workspace.RuntimeOperatingMode{ID: "assisted"},
		Requirement: workspace.RuntimeRequirement{Key: ReaperLiveControlCapability, Adapter: ReaperLiveControlCapability},
		Agent:       workspace.AgentInstance{ID: "agent-1", Name: "Producer"},
	}
}

func TestRuntimeFoundationGrantAcceptsOnlyCompatibleCLIAndUsableTrustedRoot(t *testing.T) {
	for _, provider := range []string{"codex", "claude_code"} {
		adapter := NewRuntimeFoundationAdapter(fixedGrantAgentProvider{provider: provider, isCLI: true}, fixedRunnerRoot{})
		if err := adapter.ValidateGrant(context.Background(), grantValidationRequest()); err != nil {
			t.Errorf("provider %s should be compatible: %v", provider, err)
		}
	}

	cases := []struct {
		name   string
		agents CLIAgentProviderResolver
		roots  RunnerRootResolver
	}{
		{name: "cloud provider", agents: fixedGrantAgentProvider{provider: "openai"}, roots: fixedRunnerRoot{}},
		{name: "not cli", agents: fixedGrantAgentProvider{provider: "codex", isCLI: false}, roots: fixedRunnerRoot{}},
		{name: "agent lookup failed", agents: fixedGrantAgentProvider{err: errors.New("missing")}, roots: fixedRunnerRoot{}},
		{name: "runner unavailable", agents: fixedGrantAgentProvider{provider: "codex", isCLI: true}, roots: fixedRunnerRoot{err: ErrRunnerRootUnavailable}},
		{name: "no agent resolver", roots: fixedRunnerRoot{}},
		{name: "no root resolver", agents: fixedGrantAgentProvider{provider: "codex", isCLI: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := NewRuntimeFoundationAdapter(tc.agents, tc.roots)
			if err := adapter.ValidateGrant(context.Background(), grantValidationRequest()); !errors.Is(err, ErrRuntimeGrantUnavailable) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
