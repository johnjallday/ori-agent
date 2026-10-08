package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
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

// What the workspace listing means follows the page. On a Home it is offered as
// "the projects in this Home"; on a project it could only return that project,
// which the overview already carries, so it is not offered; with no workspace
// pinned it is the app-wide listing as before. Discovery says it is for the
// rest of the app.
func TestPanelWorkspaceTools_WorkspaceListingFollowsThePinnedWorkspace(t *testing.T) {
	r, store, home, alpha, _ := workspaceResolverFixture(t)
	relationship := &workspaceRelationshipSnapshot{work: PersonalAssistantWorkContext{State: "active", StateVersion: 3, HQWorkspaceID: home.ID, ConversationAgent: "Atlas"}}
	h := &HomeAssistantAskHandler{WorkspaceContext: r, UserID: "local", PersonalAssistantContext: relationship}
	ctx := context.Background()
	offered := func(refs *HomeAssistantRouteContext) map[string]string {
		turn := h.bindWorkspaceTurn(ctx, "what kind of projects do i have?", refs, &relationship.work)
		registry := &panelToolRegistry{handler: h, turn: turn, home: newHomeToolRegistry(h.scopedPanelSources(ctx, HomeSnapshotSources{Workspaces: store}, turn)), ledger: turn.ledger}
		tools := map[string]string{}
		for _, tool := range registry.Definitions() {
			tools[tool.Name] = tool.Description
		}
		return tools
	}
	onHome := offered(&HomeAssistantRouteContext{WorkspaceID: home.ID, Origin: "personal_assistant_panel"})
	if !strings.Contains(onHome["home_workspaces"], "projects in the current Home") || !strings.Contains(onHome["home_workspaces"], "does not list the rest of the app") ||
		!strings.Contains(onHome["assistant_workspace_discovery"], "across the whole app") || onHome["home_tasks"] == "" || onHome[readerTasks] == "" {
		t.Fatalf("on a Home: %v", onHome)
	}
	onProject := offered(&HomeAssistantRouteContext{WorkspaceID: alpha.ID, Origin: "personal_assistant_panel"})
	if _, listed := onProject["home_workspaces"]; listed || !strings.Contains(onProject["assistant_workspace_discovery"], "across the whole app") || onProject["home_tasks"] == "" {
		t.Fatalf("on a project: %v", onProject)
	}
	appWide := offered(&HomeAssistantRouteContext{PagePath: "/settings", Origin: "personal_assistant_panel"})
	if strings.Contains(appWide["home_workspaces"], "current Home") || appWide["home_workspaces"] == "" || strings.Contains(appWide["assistant_workspace_discovery"], "beyond the current workspace") {
		t.Fatalf("app-wide: %v", appWide)
	}
}

type failingSummaries struct{ workspace.Store }

func (failingSummaries) ListActive() ([]*workspace.Workspace, error) {
	return nil, errors.New("store unavailable at /Users/person/private")
}

// On a Home's page, "what do I have" is about that Home. Its listings cover the
// Home and its own projects and nothing else in the app: not a project outside
// it, another Home's project, or another user's. A project that is moved away
// stops being listed, and a project that is pinned never reaches its siblings.
func TestPanelWorkspaceTools_PinnedHomeListsItsOwnProjectsAndNothingElse(t *testing.T) {
	r, store, home, alpha, beta := workspaceResolverFixture(t)
	create := func(name, kind, parent, owner string) *workspace.Workspace {
		t.Helper()
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
		ws.FolderSlug, ws.Kind, ws.ParentID, ws.OwnerUserID = strings.ToLower(strings.ReplaceAll(name, " ", "-")), kind, parent, owner
		if err := store.Save(ws); err != nil {
			t.Fatal(err)
		}
		return ws
	}
	sibling := create("Album-2", "", home.ID, "local")
	otherHome := create("Other Home", "group", "", "local")
	create("Other Home project", "", otherHome.ID, "local")
	create("Someone elses project", "", home.ID, "another-user")
	for id, title := range map[string]string{alpha.ID: "ALPHA_TASK master the single", sibling.ID: "SIBLING_TASK book the studio", beta.ID: "OUTSIDE_TASK unrelated"} {
		if err := store.Update(id, func(ws *workspace.Workspace) error {
			ws.Tasks = []workspace.Task{{ID: "task-" + ws.FolderSlug, WorkspaceID: ws.ID, Description: title, Status: workspace.TaskStatusPending}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	relationship := &workspaceRelationshipSnapshot{work: PersonalAssistantWorkContext{State: "active", StateVersion: 3, HQWorkspaceID: home.ID, ConversationAgent: "Atlas"}}
	h := &HomeAssistantAskHandler{WorkspaceContext: r, UserID: "local", PersonalAssistantContext: relationship}
	ctx := context.Background()
	registryOn := func(id string, source workspace.Store) (*panelToolRegistry, *assistantWorkspaceTurn) {
		turn := h.bindWorkspaceTurn(ctx, "review workspaces what do i have?", &HomeAssistantRouteContext{WorkspaceID: id, Origin: "personal_assistant_panel"}, &relationship.work)
		return &panelToolRegistry{handler: h, turn: turn, home: newHomeToolRegistry(h.scopedPanelSources(ctx, HomeSnapshotSources{Workspaces: source}, turn)), ledger: turn.ledger}, turn
	}

	onHome, turn := registryOn(home.ID, store)
	listed, err := onHome.Execute(ctx, "home_workspaces", `{}`)
	if err != nil || !strings.Contains(listed, `"Album-1"`) || !strings.Contains(listed, `"Album-2"`) || !strings.Contains(listed, `"total":2`) || !strings.Contains(listed, `"open_tasks":1`) {
		t.Fatalf("the Home's projects: %s %v", listed, err)
	}
	for _, outside := range []string{"Second Project", "Other Home", "Someone elses"} {
		if strings.Contains(listed, outside) {
			t.Fatalf("the Home's listing named %q: %s", outside, listed)
		}
	}
	tasks, err := onHome.Execute(ctx, "home_tasks", `{}`)
	if err != nil || !strings.Contains(tasks, "ALPHA_TASK") || !strings.Contains(tasks, "SIBLING_TASK") || strings.Contains(tasks, "OUTSIDE_TASK") {
		t.Fatalf("the Home's task listing: %s %v", tasks, err)
	}
	// The turn's facts carry the Home's children, not the app's totals, and say
	// what a question asked here is about.
	prompt := workspaceTurnPrompt(turn, true, false)
	if !strings.Contains(prompt, "This turn is about the subject workspace") || !strings.Contains(prompt, `"children"`) ||
		strings.Contains(prompt, "project_count") || strings.Contains(prompt, "group_count") || strings.Contains(prompt, "Second Project") {
		t.Fatalf("a pinned turn carried app-wide totals or lacked its scope: %s", prompt)
	}
	// Reading stays on the Home itself: a project's task detail is not read from here.
	if detail := readerResult(t, onHome, readerTask, map[string]any{"task_id": "task-album-1"}); detail["content_read"] != false || detail["reason"] != "task_not_found_in_this_workspace" {
		t.Fatalf("a project's task was read from its Home: %v", detail)
	}

	// A project moved out of the Home is no longer the Home's.
	if err := store.Update(sibling.ID, func(ws *workspace.Workspace) error { ws.ParentID = otherHome.ID; return nil }); err != nil {
		t.Fatal(err)
	}
	if after, err := onHome.Execute(ctx, "home_workspaces", `{}`); err != nil || strings.Contains(after, "Album-2") || !strings.Contains(after, `"total":1`) {
		t.Fatalf("a project moved away was still listed: %s %v", after, err)
	}
	// A pinned project is only itself: no sibling, no parent's listing.
	onProject, _ := registryOn(alpha.ID, store)
	if own, err := onProject.Execute(ctx, "home_tasks", `{}`); err != nil || !strings.Contains(own, "ALPHA_TASK") || strings.Contains(own, "SIBLING_TASK") {
		t.Fatalf("a pinned project's listing: %s %v", own, err)
	}
	// Projects that could not be listed are not "no projects".
	broken, _ := registryOn(home.ID, failingSummaries{store})
	if out, err := broken.Execute(ctx, "home_workspaces", `{}`); err == nil || strings.Contains(err.Error(), "/Users/") {
		t.Fatalf("a failed listing was reported as %q, %v", out, err)
	}
	// With no workspace pinned the listing is the whole app, as before.
	appTurn := h.bindWorkspaceTurn(ctx, "what do i have?", &HomeAssistantRouteContext{PagePath: "/settings", Origin: "personal_assistant_panel"}, &relationship.work)
	if app := workspaceTurnPrompt(appTurn, true, false); !strings.Contains(app, "project_count") || strings.Contains(app, "This turn is about the subject workspace") {
		t.Fatalf("an app-wide turn lost its totals: %s", app)
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
