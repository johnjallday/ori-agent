package agenthttp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantops"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// hookedPanelProvider runs a hook before a numbered model call, to change the
// host's state while a turn is in flight.
type hookedPanelProvider struct {
	scriptedPanelProvider
	before map[int]func()
}

func (p *hookedPanelProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if hook := p.before[len(p.requests)+1]; hook != nil {
		hook()
	}
	return p.scriptedPanelProvider.Chat(ctx, req)
}

func (f *operationFixture) taskTurn() *operationTurn {
	scope := f.handler.bindWorkspaceTurn(context.Background(), "create a task", f.refs, f.work)
	owner, _ := operationOwner("local", f.work)
	return &operationTurn{handler: f.handler, scope: scope, owner: owner, request: operationRequestTask,
		ref: HomeAssistantOperationRef{TabID: "tab-a", Watch: true}}
}

func TestPrepareTask_StagesADraftAndRecordsItOnlyAfterTheTurnIsStored(t *testing.T) {
	f := newOperationFixture(t)
	before, _ := f.store.Get(f.alpha.ID)
	f.provider.script = []llm.ChatResponse{
		toolCall(assistantops.ToolPrepareTask, map[string]any{
			"workspace_id": f.alpha.ID, "agent_name": "engineer", "title": "Bounce the stems", "brief": "Export stems for mastering."}),
		{Content: "A draft for Engineer is ready to review. Nothing is created until you save it."},
	}
	response := f.ask("Create a task for Engineer to bounce the stems", true)

	first := f.provider.requests[0]
	for _, tool := range assistantops.ToolNames() {
		if !slices.Contains(offeredTools(first), tool) {
			t.Fatalf("%s was not offered for an explicit request", tool)
		}
	}
	if !strings.Contains(first.Messages[0].Content, "# Ori skill: operate-ori (v3)") {
		t.Fatal("the operating skill was not loaded beside its tools")
	}
	// The skill tells the model that saving and starting are the user's own.
	for _, want := range []string{"Saving does not start it", "You cannot save\n  or start it for them", "**Start task**"} {
		if !strings.Contains(first.Messages[0].Content, want) {
			t.Errorf("the operating skill lacks %q", want)
		}
	}
	result := lastToolResult(f.provider.requests[1])
	for _, want := range []string{`"status":"prepared"`, `"delivery":"watching"`, "No task was created and nothing was started"} {
		if !strings.Contains(result, want) {
			t.Errorf("tool result lacks %s: %s", want, result)
		}
	}
	// The request is the operation's alone: no one-click create beside it.
	if response.RequiresConfirmation || response.Confirmation != nil {
		t.Fatalf("the legacy create confirmation also fired: %+v", response.Confirmation)
	}

	op := response.Operation
	if op == nil || op.Kind != assistantops.KindTask || op.Status != assistantops.StatusPrepared || op.Task != nil {
		t.Fatalf("a prepared draft is not a task: %+v %q", op, response.OperationError)
	}
	engineer := rosterInstances(before, "Engineer", "")[0]
	if op.Draft == nil || op.Draft.Revision != 1 || op.Draft.Title != "Bounce the stems" || op.Draft.Brief != "Export stems for mastering." ||
		op.Draft.AgentName != "Engineer" || op.Draft.AgentNodeID != engineer.NodeID {
		t.Fatalf("the draft must carry the roster's own instance: %+v", op.Draft)
	}
	if len(op.Commands) != 2 || op.Commands[0].Action != assistantops.ActionOpenWorkspace ||
		op.Commands[1].Action != assistantops.ActionOpenTaskDraft || op.Commands[1].DraftRevision != 1 {
		t.Fatalf("commands: %+v", op.Commands)
	}
	if len(f.operations.all()) != 1 {
		t.Fatal("exactly one operation is recorded")
	}
	// Preparing wrote no task, assigned nothing and ran nothing.
	after, _ := f.store.Get(f.alpha.ID)
	if len(after.Tasks) != len(before.Tasks) || after.Version != before.Version || len(after.AgentInstances) != len(before.AgentInstances) {
		t.Fatal("preparing a draft changed the workspace")
	}
	// The draft's text is not copied into the transcript beyond the reply.
	for _, message := range f.conversations.sessions["canonical"].messages {
		if strings.Contains(message.Content, op.ID) || strings.Contains(message.Content, assistantops.ToolPrepareTask) {
			t.Fatal("operation identity leaked into the transcript")
		}
	}
}

func TestPrepareTask_ReplacesOnlyTheOneClickCreateAndOnlyWhereOriCanOperate(t *testing.T) {
	const create = "Create a task in Album-1 to mix the chorus"
	// Where Ori can operate, the request is the conversation's: the model is
	// asked, and no one-click create is offered.
	f := newOperationFixture(t)
	f.provider.script = []llm.ChatResponse{{Content: "Who should do it?"}}
	if response := f.ask(create, true); response.Confirmation != nil || len(f.provider.requests) != 1 {
		t.Fatalf("operate path: %+v, %d model calls", response.Confirmation, len(f.provider.requests))
	}
	// A provider that cannot run Ori's tools keeps today's confirmation.
	limited := newOperationFixture(t)
	snapshot := &scriptedProvider{}
	limited.handler.LLMFactory.Register("fake", snapshot)
	if response := limited.ask(create, true); response.Confirmation == nil || response.Confirmation.ActionType != HomeActionCreateTask || len(snapshot.requests) != 0 {
		t.Fatalf("tool-free provider must keep the legacy owner: %+v", response)
	}
	// So does an installation without operations.
	unwired := newOperationFixture(t)
	unwired.handler.SetOperationStore(nil)
	if response := unwired.ask(create, true); response.Confirmation == nil || response.Confirmation.ActionType != HomeActionCreateTask {
		t.Fatalf("unwired installation must keep the legacy owner: %+v", response)
	}
	// Starting work and changing a team keep their own confirmations everywhere.
	kept := newOperationFixture(t)
	if err := kept.store.Update(kept.alpha.ID, func(ws *workspace.Workspace) error {
		ws.Tasks = append(ws.Tasks, workspace.Task{ID: "task-pending", WorkspaceID: ws.ID, Description: "Master the single", Status: workspace.TaskStatusPending})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for prompt, action := range map[string]string{
		"Start the task in Album-1":    HomeActionStartTask,
		"Add agent Editor to Album-1":  HomeActionAssignAgent,
		"Create a workspace named Mix": "",
	} {
		response := kept.ask(prompt, true)
		if response.Confirmation == nil || (action != "" && response.Confirmation.ActionType != action) {
			t.Errorf("%q lost its own confirmation: %+v", prompt, response.Confirmation)
		}
		if response.Operation != nil {
			t.Errorf("%q produced an operation", prompt)
		}
	}
	if len(kept.provider.requests) != 0 {
		t.Fatal("a legacy confirmation must not call the model")
	}
}

func TestPrepareTask_ResolvesTheExactRosterInstanceOrAsks(t *testing.T) {
	f := newOperationFixture(t)
	// A second Mixer instance, an agent without a node, and another user's workspace.
	if err := f.store.Update(f.alpha.ID, func(ws *workspace.Workspace) error {
		ws.AgentInstances = append(ws.AgentInstances,
			workspace.AgentInstance{ID: "instance-mixer-2", Name: "Mixer", InstanceNumber: 2, NodeID: "Mixer-node-2", Role: "Second engineer"},
			workspace.AgentInstance{ID: "instance-legacy", Name: "Legacy", InstanceNumber: 1})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	foreign := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Foreign Plans"})
	foreign.FolderSlug, foreign.OwnerUserID = "foreign-plans", "someone-else"
	if err := f.store.Save(foreign); err != nil {
		t.Fatal(err)
	}
	secret := "use api_key=sk-test-123456789abcdefghijklmnop for the upload"
	cases := []struct {
		name           string
		args           operationArguments
		status, reason string
	}{
		{name: "one instance by name", args: operationArguments{WorkspaceID: f.alpha.ID, AgentName: "ENGINEER", Title: "Bounce"}, status: "prepared"},
		{name: "defaults to the workspace this turn is about", args: operationArguments{AgentName: "Engineer", Title: "Bounce"}, status: "prepared"},
		{name: "no agent named", args: operationArguments{WorkspaceID: f.alpha.ID, Title: "Bounce"}, status: "choose", reason: "agent_not_named"},
		{name: "agent elsewhere in the app", args: operationArguments{WorkspaceID: f.alpha.ID, AgentName: "Editor", Title: "Bounce"}, status: "not_found", reason: "agent_not_in_workspace"},
		{name: "agent of another workspace", args: operationArguments{WorkspaceID: f.beta.ID, AgentName: "Engineer", Title: "Bounce"}, status: "not_found", reason: "agent_not_in_workspace"},
		{name: "several instances share the name", args: operationArguments{WorkspaceID: f.alpha.ID, AgentName: "Mixer", Title: "Mix"}, status: "choose", reason: "several_agents_match"},
		{name: "the named instance", args: operationArguments{WorkspaceID: f.alpha.ID, AgentName: "Mixer", AgentNodeID: "Mixer-node-2", Title: "Mix"}, status: "prepared"},
		{name: "a node that is not that agent's", args: operationArguments{WorkspaceID: f.alpha.ID, AgentName: "Engineer", AgentNodeID: "Mixer-node-2", Title: "Mix"}, status: "not_found", reason: "agent_not_in_workspace"},
		{name: "an agent with no node", args: operationArguments{WorkspaceID: f.alpha.ID, AgentName: "Legacy", Title: "Mix"}, status: "unavailable", reason: "agent_cannot_be_assigned"},
		{name: "another user's workspace", args: operationArguments{WorkspaceID: foreign.ID, AgentName: "Engineer", Title: "Mix"}, status: "not_found", reason: "workspace_not_found"},
		{name: "no title", args: operationArguments{WorkspaceID: f.alpha.ID, AgentName: "Engineer"}, status: "unavailable", reason: "draft_out_of_bounds"},
		{name: "title too long", args: operationArguments{WorkspaceID: f.alpha.ID, AgentName: "Engineer", Title: strings.Repeat("t", assistantops.MaxDraftTitleRunes+1)}, status: "unavailable", reason: "draft_out_of_bounds"},
		{name: "brief too long", args: operationArguments{WorkspaceID: f.alpha.ID, AgentName: "Engineer", Title: "Mix", Brief: strings.Repeat("b", assistantops.MaxDraftBriefRunes+1)}, status: "unavailable", reason: "draft_out_of_bounds"},
		{name: "a credential in the brief", args: operationArguments{WorkspaceID: f.alpha.ID, AgentName: "Engineer", Title: "Upload", Brief: secret}, status: "unavailable", reason: "secret_like_text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			turn := f.taskTurn()
			result := turn.prepareTask(tc.args)
			if result["status"] != tc.status || (tc.reason != "" && result["reason"] != tc.reason) {
				t.Fatalf("got %v", result)
			}
			if (turn.staged != nil) != (tc.status == "prepared") {
				t.Fatalf("only a resolved, bounded draft is staged: staged=%v", turn.staged != nil)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "sk-test-") || strings.Contains(string(encoded), "Foreign Plans") {
				t.Fatal("a refusal disclosed a credential or another user's workspace")
			}
		})
	}
	// The instance that was asked for is the one staged, never the first of its name.
	turn := f.taskTurn()
	turn.prepareTask(operationArguments{WorkspaceID: f.alpha.ID, AgentName: "Mixer", AgentNodeID: "Mixer-node-2", Title: "Mix"})
	if turn.staged == nil || turn.staged.Context.AgentNodeID != "Mixer-node-2" || turn.staged.Kind != assistantops.KindTask {
		t.Fatalf("staged: %+v", turn.staged)
	}
	// Several instances are offered with their nodes, so the user can choose.
	choice := f.taskTurn().prepareTask(operationArguments{WorkspaceID: f.alpha.ID, AgentName: "Mixer", Title: "Mix"})
	if choices, _ := choice["choices"].([]operationChoice); len(choices) != 2 || choices[0].NodeID == choices[1].NodeID || choices[1].NodeID != "Mixer-node-2" {
		t.Fatalf("choices: %+v", choice)
	}
	// One request is one operation, whichever tool asked first.
	both := f.taskTurn()
	both.open(f.alpha.ID, "", "")
	if again := both.prepareTask(operationArguments{WorkspaceID: f.alpha.ID, AgentName: "Engineer", Title: "Mix"}); again["reason"] != "one_operation_per_request" {
		t.Fatalf("second operation: %v", again)
	}
	// Task fields on a request to open something are refused, not ignored.
	registry := &operationRegistry{base: newHomeToolRegistry(f.handler.Sources), turn: f.taskTurn()}
	if _, err := registry.Execute(context.Background(), assistantops.ToolOpen, `{"workspace_id":"`+f.alpha.ID+`","title":"Mix"}`); err == nil || registry.turn.staged != nil {
		t.Fatal("open must not accept a task's fields")
	}
	if _, err := registry.Execute(context.Background(), assistantops.ToolPrepareTask, `{"title":"Mix","agent_name":"Engineer","schedule":"daily"}`); err == nil || registry.turn.staged != nil {
		t.Fatal("a field outside the v1 draft must refuse the call")
	}
}

func TestPrepareTask_AnAgentThatLeavesBeforeTheTurnIsStoredGetsNoDraft(t *testing.T) {
	f := newOperationFixture(t)
	provider := &hookedPanelProvider{before: map[int]func(){}}
	f.handler.LLMFactory.Register("fake", provider)
	provider.script = []llm.ChatResponse{
		toolCall(assistantops.ToolPrepareTask, map[string]any{"workspace_id": f.alpha.ID, "agent_name": "Engineer", "title": "Bounce the stems"}),
		{Content: "A draft is ready to review."},
	}
	// The roster changes after the draft is staged and before the reply is saved.
	provider.before[2] = func() {
		if err := f.store.Update(f.alpha.ID, func(ws *workspace.Workspace) error { return ws.RemoveAgent("Engineer") }); err != nil {
			t.Fatal(err)
		}
	}
	response := f.ask("Create a task for Engineer to bounce the stems", true)
	if response.Operation != nil || response.OperationError != "operation_invalidated" || len(f.operations.all()) != 0 {
		t.Fatalf("a draft for an agent who left must not be recorded or reassigned: %+v", response)
	}
	if after, _ := f.store.Get(f.alpha.ID); len(after.Tasks) != 1 {
		t.Fatal("no task may appear")
	}
}
