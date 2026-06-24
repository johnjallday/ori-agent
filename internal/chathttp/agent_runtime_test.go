package chathttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/types"
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

func TestResolveEffectiveAgent_UsesRuntimeResolverWhenBaseAgentIsMissing(t *testing.T) {
	st := &preflightStore{agents: map[string]*agent.Agent{}, names: nil}
	h := NewHandler(st, nil)
	resolver := &stubChatRuntimeResolver{
		resolved: &workspace.ResolvedAgentRuntime{
			Agent: &agent.Agent{Type: "workspace-manager"},
		},
	}
	h.SetRuntimeResolver(resolver)

	resolved, err := h.resolveEffectiveAgent("Workspace Manager", normalizedChatRouteContext{WorkspaceID: "workspace-1"})
	if err != nil {
		t.Fatalf("resolveEffectiveAgent returned error: %v", err)
	}
	if resolved == nil || resolved.Agent == nil {
		t.Fatal("expected resolved agent")
	}
	if resolved.Type != "workspace-manager" {
		t.Fatalf("expected workspace-manager type, got %q", resolved.Type)
	}
	if len(resolver.calls) != 1 {
		t.Fatalf("expected runtime resolver to be called once, got %d", len(resolver.calls))
	}
}

func TestResolveEffectiveAgent_WorkspaceEntryAgentKeepsOriginalType(t *testing.T) {
	st := newPreflightStore("Espana Manager", &agent.Agent{Type: "general"})
	h := NewHandler(st, nil)
	h.workspaceStore = &preflightWorkspaceStore{
		workspaces: map[string]*workspace.Workspace{
			"workspace-espana": {
				ID:         "workspace-espana",
				Name:       "Espana",
				SharedData: map[string]any{"entry_agent_name": "Espana Manager"},
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
	if resolved.Type != "general" {
		t.Fatalf("expected workspace entry agent to keep original type 'general', got %q", resolved.Type)
	}
}

func TestResolveEffectiveAgent_RejectsPausedAgent(t *testing.T) {
	st := newPreflightStore("Paused Agent", &agent.Agent{Status: types.AgentStatusDisabled})
	h := NewHandler(st, nil)

	resolved, err := h.resolveEffectiveAgent("Paused Agent", normalizedChatRouteContext{})
	if !errors.Is(err, errAgentPaused) {
		t.Fatalf("expected errAgentPaused, got resolved=%v err=%v", resolved, err)
	}
}

func TestResolveEffectiveAgent_DoesNotFallbackWhenWorkspaceAgentIsPaused(t *testing.T) {
	st := newPreflightStore("Workspace Agent", &agent.Agent{})
	h := NewHandler(st, nil)
	h.SetRuntimeResolver(&stubChatRuntimeResolver{err: workspace.ErrAgentPaused})

	resolved, err := h.resolveEffectiveAgent("Workspace Agent", normalizedChatRouteContext{WorkspaceID: "workspace-1"})
	if !errors.Is(err, errAgentPaused) {
		t.Fatalf("expected errAgentPaused, got resolved=%v err=%v", resolved, err)
	}
}

func TestChatHandler_WorkspaceEntryGeneralAgent_UsesPlanningForm(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	sessionStore := session.NewHybridStoreWithDB(db, 10)
	now := time.Now()
	if err := sessionStore.CreateWorkspace(ctx, &session.Workspace{
		ID:        "workspace-spain",
		Name:      "Spain",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to create session workspace: %v", err)
	}

	h := NewHandler(newPreflightStore("Spain Manager", &agent.Agent{Type: "general"}), nil)
	h.SetSessionStore(sessionStore)
	h.workspaceStore = &preflightWorkspaceStore{
		workspaces: map[string]*workspace.Workspace{
			"workspace-spain": {
				ID:         "workspace-spain",
				Name:       "Spain",
				SharedData: map[string]any{"entry_agent_name": "Spain Manager"},
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
	if workflowStep, ok := resp["workflow_step"].(map[string]any); !ok {
		t.Fatalf("expected workflow_step object, got %T", resp["workflow_step"])
	} else if got, _ := workflowStep["step_type"].(string); got != string(WorkflowStepAskForm) {
		t.Fatalf("expected ask_form workflow step, got %q", got)
	}
}

func TestChatHandler_AutoWorkspaceContextIsAppliedBeforeRuntimeResolution(t *testing.T) {
	st := newPreflightStore("Ori", &agent.Agent{})
	h := NewHandler(st, nil)
	wsStore := workspace.NewInMemoryStore()
	h.SetWorkspaceStore(wsStore)

	resolver := &stubChatRuntimeResolver{
		resolved: &workspace.ResolvedAgentRuntime{
			Agent: &agent.Agent{},
		},
	}
	h.SetRuntimeResolver(resolver)

	body, _ := json.Marshal(map[string]any{
		"question":   "review code in this repository",
		"agent_name": "Ori",
		"route_context": map[string]any{
			"surface":   "dashboard",
			"page_path": "/dashboard",
			"origin":    "ask_ori",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ChatHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	if len(resolver.calls) != 1 {
		t.Fatalf("expected runtime resolver to be called once, got %d body=%s", len(resolver.calls), rr.Body.String())
	}

	parts := strings.Split(resolver.calls[0], "|")
	if len(parts) != 3 {
		t.Fatalf("expected resolver call format agent|workspace|node, got %q", resolver.calls[0])
	}
	if parts[0] != "Ori" {
		t.Fatalf("expected resolver to target agent Ori, got %q", parts[0])
	}
	if strings.TrimSpace(parts[1]) == "" {
		t.Fatalf("expected resolver to receive an auto-selected workspace id, got %q", resolver.calls[0])
	}

	allIDs, err := wsStore.List()
	if err != nil {
		t.Fatalf("failed to list workspaces: %v", err)
	}
	if len(allIDs) != 1 {
		t.Fatalf("expected one auto-created workspace, got %d", len(allIDs))
	}
	if parts[1] != allIDs[0] {
		t.Fatalf("expected resolver workspace id %q to match created workspace %q", parts[1], allIDs[0])
	}
}
