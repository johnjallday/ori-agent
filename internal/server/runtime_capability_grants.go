package server

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/reapersetup"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type runtimeGrantAgentResolver struct {
	resolver *workspace.AgentRuntimeResolver
}

func (r runtimeGrantAgentResolver) ProviderForAgent(_ context.Context, workspaceID string, instance workspace.AgentInstance) (string, bool, error) {
	if r.resolver == nil {
		return "", false, errors.New("agent resolver unavailable")
	}
	resolved, err := r.resolver.ResolveAgentForWorkspace(instance.Name, workspaceID, instance.NodeID)
	if err != nil || resolved == nil || resolved.Agent == nil {
		return "", false, err
	}
	provider := strings.ToLower(strings.TrimSpace(resolved.Agent.Settings.Provider))
	return provider, provider == "codex" || provider == "claude_code", nil
}

func (b *ServerBuilder) wireRuntimeGrantFoundation() {
	if b == nil || b.runtimeCapabilityRegistry == nil || b.runtimeResolver == nil {
		return
	}
	adapter := reapersetup.NewRuntimeFoundationAdapter(
		runtimeGrantAgentResolver{resolver: b.runtimeResolver},
		reapersetup.NewRunnerRootResolver(),
	)
	if err := b.runtimeCapabilityRegistry.Replace(adapter); err != nil {
		// The key was reserved during registry construction; failure means a
		// wiring bug, not user or filesystem detail.
		return
	}
}
