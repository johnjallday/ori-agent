package agenthttp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestPanelWorkspaceTools_PinnedFreshOwnedAndRevocable(t *testing.T) {
	r, store, home, alpha, beta := workspaceResolverFixture(t)
	relationship := &workspaceRelationshipSnapshot{work: PersonalAssistantWorkContext{State: "active", StateVersion: 3, HQWorkspaceID: home.ID, ConversationAgent: "Atlas"}}
	h := &HomeAssistantAskHandler{WorkspaceContext: r, UserID: "local", PersonalAssistantContext: relationship}
	ctx := context.Background()
	turn := h.bindWorkspaceTurn(ctx, "hello", &HomeAssistantRouteContext{WorkspaceID: alpha.ID, Origin: "personal_assistant_panel"}, &relationship.work)
	if err := store.Update(alpha.ID, func(ws *workspace.Workspace) error {
		ws.Tasks = []workspace.Task{{ID: "owned-task", WorkspaceID: alpha.ID, Description: "Current owned fact", Status: workspace.TaskStatusPending}, {ID: "foreign-task", WorkspaceID: beta.ID, Description: "FOREIGN_TASK_SENTINEL", Status: workspace.TaskStatusPending}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sources := h.scopedPanelSources(ctx, HomeSnapshotSources{Workspaces: store}, turn)
	registry := &panelToolRegistry{handler: h, turn: turn, home: newHomeToolRegistry(sources), ledger: turn.ledger}
	data, err := registry.Execute(ctx, "home_tasks", `{"workspace_id":"`+alpha.ID+`"}`)
	if err != nil || !strings.Contains(data, "Current owned fact") || strings.Contains(data, "FOREIGN_TASK_SENTINEL") {
		t.Fatalf("owned read: %s %v", data, err)
	}
	if _, err := registry.Execute(ctx, "home_tasks", `{"workspace_id":"`+beta.ID+`"}`); err == nil {
		t.Fatal("tool retargeted accepted turn")
	}
	data, err = registry.Execute(ctx, "assistant_workspace_discovery", `{}`)
	if err != nil || !strings.Contains(data, home.Name) || turn.projection.Subject.ID != alpha.ID {
		t.Fatal("discovery omitted group or changed subject", data, err)
	}
	if !turn.ledger.charge(turn.ledger.remaining()) || turn.ledger.remaining() != 0 || assistantcontext.EvidenceLimit != 64000 {
		t.Fatal("fixture could not exhaust the turn's evidence budget")
	}
	data, err = registry.Execute(ctx, "home_tasks", `{}`)
	if err != nil || !strings.Contains(data, "evidence_budget_exhausted") {
		t.Fatal("aggregate budget", data, err)
	}
	relationship.work.StateVersion++
	if _, err := registry.Execute(ctx, "home_tasks", `{}`); err == nil {
		t.Fatal("replacement relationship retained reader")
	}
}

// With a workspace pinned, the app-wide workspace listing can only answer with
// that one workspace, and with nothing on a Home or group. It is not offered
// there, so a model is not sent to a listing that contradicts discovery; with
// no workspace pinned it is offered as before.
func TestPanelWorkspaceTools_PinnedTurnDoesNotOfferTheAppWideWorkspaceListing(t *testing.T) {
	r, store, home, alpha, _ := workspaceResolverFixture(t)
	relationship := &workspaceRelationshipSnapshot{work: PersonalAssistantWorkContext{State: "active", StateVersion: 3, HQWorkspaceID: home.ID, ConversationAgent: "Atlas"}}
	h := &HomeAssistantAskHandler{WorkspaceContext: r, UserID: "local", PersonalAssistantContext: relationship}
	ctx := context.Background()
	offered := func(refs *HomeAssistantRouteContext) map[string]bool {
		turn := h.bindWorkspaceTurn(ctx, "what kind of projects do i have?", refs, &relationship.work)
		registry := &panelToolRegistry{handler: h, turn: turn, home: newHomeToolRegistry(h.scopedPanelSources(ctx, HomeSnapshotSources{Workspaces: store}, turn)), ledger: turn.ledger}
		names := map[string]bool{}
		for _, tool := range registry.Definitions() {
			names[tool.Name] = true
		}
		return names
	}
	for name, id := range map[string]string{"a Home": home.ID, "a project": alpha.ID} {
		tools := offered(&HomeAssistantRouteContext{WorkspaceID: id, Origin: "personal_assistant_panel"})
		if tools["home_workspaces"] || !tools["assistant_workspace_discovery"] || !tools["home_tasks"] || !tools[readerTasks] {
			t.Fatalf("on %s: %v", name, tools)
		}
	}
	if tools := offered(&HomeAssistantRouteContext{PagePath: "/settings", Origin: "personal_assistant_panel"}); !tools["home_workspaces"] || !tools["assistant_workspace_discovery"] {
		t.Fatalf("app-wide: %v", tools)
	}
}

type inspectingPanelProvider struct {
	fakeProvider
	tools    bool
	requests []llm.ChatRequest
}

func (p *inspectingPanelProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsTools: p.tools}
}
func (p *inspectingPanelProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.requests = append(p.requests, req)
	return &llm.ChatResponse{Content: "Hello"}, nil
}

func TestPanelWorkspaceModel_CapabilitiesAndNoNativeOptIn(t *testing.T) {
	for _, supportsTools := range []bool{false, true} {
		t.Run(map[bool]string{true: "brokered", false: "snapshot"}[supportsTools], func(t *testing.T) {
			r, store, home, alpha, _ := workspaceResolverFixture(t)
			relationship := &workspaceRelationshipSnapshot{work: PersonalAssistantWorkContext{State: "active", StateVersion: 3, HQWorkspaceID: home.ID, ConversationAgent: "Atlas"}}
			provider := &inspectingPanelProvider{tools: supportsTools}
			factory := llm.NewFactory()
			factory.Register("fake", provider)
			h := NewHomeAssistantAskHandler(HomeSnapshotSources{Workspaces: store}, factory, stubSystemModel{provider: "fake", model: "fake-model"})
			h.WorkspaceContext, h.PersonalAssistantContext = r, relationship
			for _, intent := range []string{"assistant_conversation", "app_introspection", "app_navigation"} {
				provider.requests = nil
				reply := h.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "Hello", Intent: intent, Context: &HomeAssistantRouteContext{WorkspaceID: alpha.ID, Origin: "personal_assistant_panel"}})
				if reply.Response != "Hello" || len(provider.requests) != 1 || reply.WorkspaceContext.Subject.ID != alpha.ID {
					t.Fatalf("%s reply: %+v", intent, reply)
				}
				req := provider.requests[0]
				if req.WorkspaceID != "" || req.WorkspaceDir != "" || req.ExecutionScope != nil || len(req.MCPServers) > 0 {
					t.Fatal("display context opted into native execution")
				}
				if (len(req.Tools) > 0) != supportsTools {
					t.Fatal("ignored provider capability")
				}
				encoded, _ := json.Marshal(req.Messages)
				if !strings.Contains(string(encoded), alpha.Name) {
					t.Fatalf("%s omitted context", intent)
				}
				if !supportsTools && !strings.Contains(string(encoded), "cannot execute Ori-brokered readers") {
					t.Fatal("snapshot limitation missing")
				}
			}
		})
	}
}
