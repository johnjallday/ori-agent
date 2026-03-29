package chathttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestResolveEffectiveAgent_PromotesWorkspaceEntryAgentToWorkspaceManager(t *testing.T) {
	st := newPreflightStore("Espana Manager", &agent.Agent{Type: "general"})
	h := NewHandler(st, nil)
	h.workspaceStore = &preflightWorkspaceStore{
		workspaces: map[string]*workspace.Workspace{
			"workspace-espana": {
				ID:         "workspace-espana",
				Name:       "Espana",
				SharedData: map[string]interface{}{"entry_agent_name": "Espana Manager"},
				AgentInstances: []workspace.AgentInstance{
					{ID: "agent-1", Name: "Espana Manager", EntryPoint: true},
				},
			},
		},
	}

	resolved, err := h.resolveEffectiveAgent("Espana Manager", normalizedChatRouteContext{WorkspaceID: "workspace-espana"})
	if err != nil {
		t.Fatalf("resolveEffectiveAgent returned error: %v", err)
	}
	if resolved == nil || resolved.Agent == nil {
		t.Fatal("expected resolved agent")
	}
	if resolved.Type != "workspace-manager" {
		t.Fatalf("expected workspace entry agent to be promoted to workspace-manager, got %q", resolved.Type)
	}
}

func TestChatHandler_WorkspaceEntryGeneralAgent_UsesPlanningForm(t *testing.T) {
	h := NewHandler(newPreflightStore("Spain Manager", &agent.Agent{Type: "general"}), nil)
	h.workspaceStore = &preflightWorkspaceStore{
		workspaces: map[string]*workspace.Workspace{
			"workspace-spain": {
				ID:         "workspace-spain",
				Name:       "Spain",
				SharedData: map[string]interface{}{"entry_agent_name": "Spain Manager"},
				AgentInstances: []workspace.AgentInstance{
					{ID: "agent-1", Name: "Spain Manager", EntryPoint: true},
				},
			},
		},
	}

	body, _ := json.Marshal(map[string]any{
		"question":   "let's plan a trip to Spain",
		"agent_name": "Spain Manager",
		"route_context": map[string]any{
			"workspace_id": "workspace-spain",
			"surface":      "workspace_detail",
			"page_path":    "/workspaces/workspace-spain",
			"origin":       "ask_ori",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChatHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, ok := resp["planning_form"].(map[string]any); !ok {
		t.Fatalf("expected planning_form object, got %T", resp["planning_form"])
	}
}
