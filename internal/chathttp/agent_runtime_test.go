package chathttp

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type stubChatRuntimeResolver struct {
	resolved *workspace.ResolvedAgentRuntime
	err      error
	calls    []string
}

func (r *stubChatRuntimeResolver) ResolveAgentForWorkspace(agentName, workspaceID, nodeID string) (*workspace.ResolvedAgentRuntime, error) {
	r.calls = append(r.calls, agentName+"|"+workspaceID+"|"+nodeID)
	return r.resolved, r.err
}

func TestPersistAgent_DoesNotPersistRuntimeMCPServerNames(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	h := NewHandler(st, nil)

	runtimeAgent := &agent.Agent{}

	if err := h.persistAgent("Ori", runtimeAgent); err != nil {
		t.Fatalf("persistAgent returned error: %v", err)
	}

	persisted, ok := st.GetAgent("Ori")
	if !ok || persisted == nil {
		t.Fatalf("expected persisted agent")
	}
}

func TestResolveEffectiveAgent_UsesWorkspaceRuntimeResolver(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	h := NewHandler(st, nil)
	resolver := &stubChatRuntimeResolver{
		resolved: &workspace.ResolvedAgentRuntime{
			Agent:      &agent.Agent{},
			MCPServers: []string{"ws:workspace-1:mcp:filesystem:workspace-filesystem"},
		},
	}
	h.SetRuntimeResolver(resolver)

	resolved, err := h.resolveEffectiveAgent("Ori", normalizedChatRouteContext{WorkspaceID: "workspace-1"})
	if err != nil {
		t.Fatalf("resolveEffectiveAgent returned error: %v", err)
	}

	if len(resolver.calls) != 1 {
		t.Fatalf("expected runtime resolver to be called once, got %d", len(resolver.calls))
	}
	if len(resolved.MCPServers) != 1 || resolved.MCPServers[0] != "ws:workspace-1:mcp:filesystem:workspace-filesystem" {
		t.Fatalf("expected workspace runtime MCP server, got %v", resolved.MCPServers)
	}
}
