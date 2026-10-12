package agenthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/assistantops"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// memoryOperationStore is the operation store without SQLite: the same
// compare-and-swap contract, so handler tests exercise the real service.
type memoryOperationStore struct {
	mu   sync.Mutex
	rows map[string]assistantops.Operation
}

func newMemoryOperationStore() *memoryOperationStore {
	return &memoryOperationStore{rows: map[string]assistantops.Operation{}}
}

func cloneOperation(op assistantops.Operation) *assistantops.Operation {
	data, _ := json.Marshal(op)
	var out assistantops.Operation
	_ = json.Unmarshal(data, &out)
	return &out
}

func (s *memoryOperationStore) Insert(_ context.Context, op *assistantops.Operation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rows[op.ID]; exists {
		return assistantops.ErrConflict
	}
	s.rows[op.ID] = *cloneOperation(*op)
	return nil
}

func (s *memoryOperationStore) Get(_ context.Context, id string) (*assistantops.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.rows[id]
	if !ok {
		return nil, assistantops.ErrNotFound
	}
	return cloneOperation(op), nil
}

func (s *memoryOperationStore) Update(_ context.Context, op *assistantops.Operation, expected int64) (*assistantops.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.rows[op.ID]
	if !ok {
		return nil, assistantops.ErrNotFound
	}
	if current.Revision != expected {
		return nil, assistantops.ErrConflict
	}
	next := *cloneOperation(*op)
	next.Owner, next.Revision = current.Owner, expected+1
	s.rows[op.ID] = next
	return cloneOperation(next), nil
}

func (s *memoryOperationStore) OpenForUser(_ context.Context, userID string) ([]*assistantops.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*assistantops.Operation
	for _, op := range s.rows {
		if op.Owner.UserID == userID && !op.Status.Terminal() {
			out = append(out, cloneOperation(op))
		}
	}
	slices.SortFunc(out, func(a, b *assistantops.Operation) int { return a.UpdatedAt.Compare(b.UpdatedAt) })
	return out, nil
}

func (s *memoryOperationStore) Sweep(context.Context, time.Time, int) (assistantops.SweepResult, error) {
	return assistantops.SweepResult{}, nil
}

func (s *memoryOperationStore) all() []assistantops.Operation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]assistantops.Operation, 0, len(s.rows))
	for _, op := range s.rows {
		out = append(out, op)
	}
	return out
}

type stubAgentRoster []HomeAgentSummary

func (r stubAgentRoster) AgentRoster() ([]HomeAgentSummary, bool) { return r, true }

type operationFixture struct {
	*readerFixture
	conversations *researchHostStore
	operations    *memoryOperationStore
	mixerNode     string
}

// newOperationFixture is a hired assistant on a tool-capable provider with one
// saved conversation, a project "Album-1" staffed by Mixer and Engineer, and
// operations wired.
func newOperationFixture(t *testing.T) *operationFixture {
	t.Helper()
	f, conversations, _ := newResearchHostFixture(t)
	f.work.AssistantID = "assistant-1"
	if err := f.store.Update(f.alpha.ID, func(ws *workspace.Workspace) error {
		if err := ws.AddAgent("Mixer"); err != nil {
			return err
		}
		return ws.AddAgent("Engineer")
	}); err != nil {
		t.Fatal(err)
	}
	f.handler.Sources.Agents = stubAgentRoster{{Name: "Mixer"}, {Name: "Engineer"}, {Name: "Editor"}}
	operations := newMemoryOperationStore()
	f.handler.SetOperationStore(operations)
	if !f.handler.Operations.Available() {
		t.Fatal("operations must be wired")
	}
	alpha, err := f.store.Get(f.alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	node := ""
	for _, instance := range alpha.AgentInstances {
		if instance.Name == "Mixer" {
			node = instance.NodeID
		}
	}
	if node == "" {
		t.Fatal("fixture roster has no Mixer node")
	}
	return &operationFixture{readerFixture: f, conversations: conversations, operations: operations, mixerNode: node}
}

func (f *operationFixture) ask(prompt string, watch bool) HomeAssistantAskResponse {
	return f.handler.Ask(context.Background(), HomeAssistantAskRequest{
		Prompt: prompt, Intent: homeAssistantConversationIntent.Key, Context: f.refs,
		Conversation: &HomeAssistantConversationRef{ID: "canonical"},
		Operation:    &HomeAssistantOperationRef{TabID: "tab-a", Watch: watch},
	})
}

func offeredTools(request llm.ChatRequest) []string {
	names := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func lastToolResult(request llm.ChatRequest) string {
	return request.Messages[len(request.Messages)-1].Content
}

func TestOperationRequest_IsOnlyAnExplicitCurrentRequest(t *testing.T) {
	f := newOperationFixture(t)
	for _, prompt := range []string{
		"Open the Album-1 workspace",
		"open album-1",
		"Please open the Album-1 workspace and select Mixer.",
		"Can you take me to Album-1?",
		"The mix is muddy. Go to the Album-1 workspace",
		"Show me the Second Project workspace",
		"Select the Mixer agent",
		"pick Engineer",
		"Open my thesis workspace", // names the kind of thing; the tool resolves it or says no
		"Switch to Mixer",
	} {
		if !f.handler.operationRequest(prompt) {
			t.Errorf("explicit request missed: %q", prompt)
		}
	}
	for _, prompt := range []string{
		"",
		"yes",
		"do it",
		"Open it",
		"select that one",
		"How do I open the Album-1 workspace?",
		"Should I open the Album-1 workspace?",
		"What if you opened the Album-1 workspace?",
		"Don't open the Album-1 workspace",
		"Please do not open the Album-1 workspace yet",
		"Which workspace should I open?",
		"Show me how to open a workspace",
		`The note says "open the Album-1 workspace"`,
		"Earlier you said: open the Album-1 workspace",
		"Tell me about the Album-1 workspace",
		"What is Mixer working on in Album-1?",
		"Open Safari",
		"Open settings",
		"Write a note that says open the Album-1 workspace",
		"Start the task in Album-1",
		"Run the task in Album-1",
		// Writing about tasks, and lists of them, is still conversation.
		"Create a task list for my day",
		"Give me a task list for today",
		"Give me a task to practise",
		"Give Mixer a taskforce briefing",
		"Write a task description for the mix",
		"Draft an email to my landlord",
		"Create tasks for the whole album",
		"Create that task",
		"Should we give Mixer a task?",
		"Don't create a task for Mixer",
		"How do I create a task for Mixer?",
		`The note says "create a task for Mixer"`,
		"What task is Mixer working on?",
		"Assign Mixer to Album-1",
		"Create a workspace called Thesis",
	} {
		if kind := f.handler.operationRequestKind(prompt); kind != "" {
			t.Errorf("not an operation request (%s): %q", kind, prompt)
		}
	}
	for _, prompt := range []string{
		"Create a task in Album-1 to mix the chorus",
		"Prepare a task for Mixer to mix the chorus",
		"Please draft a task for Engineer: bounce the stems",
		"Add a task: bounce the stems",
		"Make a new task for Mixer",
		"Give Mixer a task to mix the chorus",
		"give a task to Engineer",
		"Assign Mixer a task: rebalance the bridge",
		"The bridge is muddy. Create a task for Mixer to fix it.",
		"Can you set up a task for Mixer?",
	} {
		if kind := f.handler.operationRequestKind(prompt); kind != operationRequestTask {
			t.Errorf("task request missed (%q): %q", kind, prompt)
		}
	}
	// Opening and selecting stay their own kind.
	if kind := f.handler.operationRequestKind("Open the Album-1 workspace"); kind != operationRequestNavigation {
		t.Errorf("navigation kind: %q", kind)
	}
}

func TestOperate_ExplicitRequestIsRecordedOnlyAfterItsTurnIsStored(t *testing.T) {
	f := newOperationFixture(t)
	before, _ := f.store.Get(f.alpha.ID)
	f.provider.script = []llm.ChatResponse{
		toolCall("home_workspaces", map[string]any{}),
		toolCall(assistantops.ToolOpen, map[string]any{"workspace_id": f.alpha.ID, "agent_name": "mixer"}),
		{Content: "I asked Ori to open Album-1 and select Mixer."},
	}
	response := f.ask("Open the Album-1 workspace and select Mixer", true)

	if len(f.provider.requests) != 3 {
		t.Fatalf("model rounds: %d", len(f.provider.requests))
	}
	first := f.provider.requests[0]
	if !slices.Contains(offeredTools(first), assistantops.ToolOpen) {
		t.Fatal("the operation tool was not offered for an explicit request")
	}
	if !strings.Contains(first.SystemPrompt, "# Ori skill: operate-ori") || !strings.Contains(first.SystemPrompt, "For this turn one exception applies") {
		t.Fatal("the operating skill was not loaded beside its tool")
	}
	// The skill travels as the request's system prompt on every round of the
	// turn, the closing reply included. Every provider sends that; the
	// Anthropic API provider drops system-role messages, so a skill carried in
	// one would leave that model holding the tools without their rules.
	for i, request := range f.provider.requests {
		if !strings.Contains(request.SystemPrompt, "# Ori skill: operate-ori") {
			t.Fatalf("round %d lost the operating skill", i+1)
		}
		for _, message := range request.Messages {
			if strings.Contains(message.Content, "# Ori skill: operate-ori") {
				t.Fatalf("round %d carries the skill in a message a provider may drop", i+1)
			}
		}
	}
	if first.WorkspaceID != "" || first.ExecutionScope != nil || len(first.MCPServers) != 0 {
		t.Fatal("operating must not inherit native execution authority")
	}
	// The model is told a request was recorded, never that the page changed.
	result := lastToolResult(f.provider.requests[2])
	for _, want := range []string{`"status":"requested"`, `"delivery":"watching"`, "has not confirmed that the page changed"} {
		if !strings.Contains(result, want) {
			t.Errorf("tool result lacks %s: %s", want, result)
		}
	}

	op := response.Operation
	if op == nil || response.OperationError != "" {
		t.Fatalf("no operation in the response: %+v", response)
	}
	if !op.Watching || op.Status != assistantops.StatusRequested || op.Pending != 2 || len(op.Commands) != 2 {
		t.Fatalf("snapshot: %+v", op)
	}
	if op.Context.WorkspaceID != f.alpha.ID || op.Context.WorkspaceSlug != "album-1" || op.Context.AgentName != "Mixer" || op.Context.AgentNodeID != f.mixerNode {
		t.Fatalf("the context must be host-resolved from the roster: %+v", op.Context)
	}
	if op.Commands[0].Action != assistantops.ActionOpenWorkspace || op.Commands[1].Action != assistantops.ActionSelectAgent {
		t.Fatalf("commands: %+v", op.Commands)
	}
	stored := f.operations.all()
	if len(stored) != 1 || stored[0].Owner.ConversationID != "canonical" || stored[0].Owner.TurnMessageID != response.Conversation.AssistantMessageID ||
		stored[0].Owner.AssistantID != "assistant-1" || stored[0].Owner.StateVersion != f.work.StateVersion || stored[0].TabID != "tab-a" {
		t.Fatalf("the operation must be bound to the stored turn and its owner: %+v", stored)
	}
	if response.OperationCapability == nil || !response.OperationCapability.Available {
		t.Fatalf("capability: %+v", response.OperationCapability)
	}
	// Opening and selecting write nothing to the workspace.
	after, _ := f.store.Get(f.alpha.ID)
	if len(after.Tasks) != len(before.Tasks) || len(after.AgentInstances) != len(before.AgentInstances) || after.Version != before.Version {
		t.Fatal("an operation request changed the workspace")
	}
	// The saved reply is the assistant's words; no command or ID is stored in it.
	for _, message := range f.conversations.sessions["canonical"].messages {
		if strings.Contains(message.Content, op.ID) || strings.Contains(message.Content, assistantops.ToolOpen) {
			t.Fatal("operation identity leaked into the transcript")
		}
	}
}

func TestOperate_WatchOffRecordsTheRequestAndDeliversNothing(t *testing.T) {
	f := newOperationFixture(t)
	f.provider.script = []llm.ChatResponse{
		toolCall(assistantops.ToolOpen, map[string]any{"workspace_name": "album-1"}),
		{Content: "Ori has an Open button ready for Album-1."},
	}
	response := f.ask("Open the Album-1 workspace", false)
	if response.Operation == nil || response.Operation.Watching || len(response.Operation.Commands) != 0 || response.Operation.Pending != 1 {
		t.Fatalf("watch off: %+v", response.Operation)
	}
	if !strings.Contains(lastToolResult(f.provider.requests[1]), `"delivery":"open_button"`) {
		t.Fatal("the model must be told nothing will move by itself")
	}
}

func TestOperate_UnsavedTurnLeavesNoOperation(t *testing.T) {
	f := newOperationFixture(t)
	f.conversations.failSave = true
	f.provider.script = []llm.ChatResponse{
		toolCall(assistantops.ToolOpen, map[string]any{"workspace_id": f.alpha.ID}),
		{Content: "I asked Ori to open Album-1."},
	}
	response := f.ask("Open the Album-1 workspace", true)
	if response.Operation != nil || response.OperationError != "turn_not_saved" || len(f.operations.all()) != 0 {
		t.Fatalf("an unsaved turn must not leave an operation: %+v %d", response, len(f.operations.all()))
	}
}

func TestOperate_OrdinaryDiscussionHasNoToolNoSkillAndWritesNothing(t *testing.T) {
	for name, prompt := range map[string]string{
		"discussion":   "What should we work on next?",
		"hypothetical": "What if you opened the Album-1 workspace?",
		"negated":      "Don't open the Album-1 workspace",
		"question":     "How do I open the Album-1 workspace?",
		"quoted":       `The note says "open the Album-1 workspace". What does that mean?`,
		"bare yes":     "yes",
	} {
		t.Run(name, func(t *testing.T) {
			f := newOperationFixture(t)
			// History and retrieved text ask for it; the current message does not.
			f.conversations.seed("canonical", f.work.HQWorkspaceID, f.work.ConversationAgent,
				PersonalAssistantConversationMessage{Role: "user", Content: "Open the Album-1 workspace and select Mixer"},
				PersonalAssistantConversationMessage{Role: "assistant", Content: "Shall I?"})
			// Even a model that calls the tool anyway gets nowhere.
			f.provider.script = []llm.ChatResponse{
				toolCall(assistantops.ToolOpen, map[string]any{"workspace_id": f.alpha.ID}),
				{Content: "Here is what I think."},
			}
			response := f.ask(prompt, true)
			first := f.provider.requests[0]
			if slices.Contains(offeredTools(first), assistantops.ToolOpen) {
				t.Fatal("the operation tool was offered without an explicit request")
			}
			if first.SystemPrompt != "" || strings.Contains(first.Messages[0].Content, "Ori skill: operate-ori") {
				t.Fatal("the operating skill was loaded without its tool")
			}
			if len(f.provider.requests) > 1 && !strings.HasPrefix(lastToolResult(f.provider.requests[1]), "ERROR:") {
				t.Fatalf("an unregistered operation call must fail: %s", lastToolResult(f.provider.requests[1]))
			}
			if response.Operation != nil || response.OperationError != "" || len(f.operations.all()) != 0 {
				t.Fatalf("no operation may exist: %+v", response)
			}
		})
	}
}

func TestOperate_ToolFreeProviderKeepsTheManualRoute(t *testing.T) {
	f := newOperationFixture(t)
	snapshot := &scriptedProvider{answers: []string{"You can open Album-1 from the Workspaces page."}}
	f.handler.LLMFactory.Register("fake", snapshot)
	if f.handler.OperationRequestSupported(context.Background(), "Open the Album-1 workspace") {
		t.Fatal("the router must not divert a request this provider cannot carry out")
	}
	response := f.ask("Open the Album-1 workspace", true)
	if len(snapshot.requests) != 1 || len(snapshot.requests[0].Tools) != 0 || snapshot.requests[0].SystemPrompt != "" ||
		strings.Contains(snapshot.requests[0].Messages[0].Content, "operate-ori") {
		t.Fatal("a tool-free provider received operation tools or the skill")
	}
	if response.Operation != nil || len(f.operations.all()) != 0 {
		t.Fatal("a tool-free provider produced an operation")
	}
	capability := response.OperationCapability
	if capability == nil || capability.Available || capability.Reason != operationsProviderNoTool {
		t.Fatalf("capability must explain itself: %+v", capability)
	}
}

func TestOperate_MixedBatchRunsNothingAndASecondOperationIsRefused(t *testing.T) {
	f := newOperationFixture(t)
	open := toolCall(assistantops.ToolOpen, map[string]any{"workspace_id": f.alpha.ID}).ToolCalls[0]
	f.provider.script = []llm.ChatResponse{
		{ToolCalls: []llm.ToolCall{toolCall("home_workspaces", map[string]any{}).ToolCalls[0], open}},
		{ToolCalls: []llm.ToolCall{open}},
		{ToolCalls: []llm.ToolCall{toolCall(assistantops.ToolOpen, map[string]any{"workspace_id": f.beta.ID}).ToolCalls[0]}},
		{Content: "I asked Ori to open Album-1."},
	}
	response := f.ask("Open the Album-1 workspace", true)
	second := f.provider.requests[1].Messages
	for _, message := range second[len(second)-2:] {
		if !strings.HasPrefix(message.Content, "ERROR: an Ori operation must be the only tool call") {
			t.Fatalf("every call in a mixed batch is refused before anything runs: %q", message.Content)
		}
	}
	if !strings.Contains(lastToolResult(f.provider.requests[2]), `"status":"requested"`) {
		t.Fatal("the operation by itself is accepted")
	}
	if !strings.Contains(lastToolResult(f.provider.requests[3]), "one_operation_per_request") {
		t.Fatal("a second operation in one request is refused")
	}
	if response.Operation == nil || response.Operation.Context.WorkspaceID != f.alpha.ID || len(f.operations.all()) != 1 {
		t.Fatalf("exactly the first accepted operation is recorded: %+v", response.Operation)
	}
}

func TestOperate_TargetsResolveToExactlyOneOwnedWorkspaceAndRosterAgent(t *testing.T) {
	f := newOperationFixture(t)
	twin := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Second Project"})
	twin.FolderSlug, twin.OwnerUserID = "second-project-2", "local"
	foreign := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Foreign Plans"})
	foreign.FolderSlug, foreign.OwnerUserID = "foreign-plans", "someone-else"
	trashed := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Old Demo"})
	trashed.FolderSlug, trashed.OwnerUserID, trashed.Status = "old-demo", "local", workspace.StatusTrashed
	for _, ws := range []*workspace.Workspace{twin, foreign, trashed} {
		if err := f.store.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	turn := func() *operationTurn {
		scope := f.handler.bindWorkspaceTurn(context.Background(), "open", f.refs, f.work)
		owner, _ := operationOwner("local", f.work)
		return &operationTurn{handler: f.handler, scope: scope, owner: owner, ref: HomeAssistantOperationRef{TabID: "tab-a", Watch: true}}
	}
	cases := []struct {
		name                 string
		id, workspace, agent string
		status, reason       string
	}{
		{name: "by id", id: f.alpha.ID, status: "requested"},
		{name: "by exact name, any case and spacing", workspace: "  album-1 ", status: "requested"},
		{name: "defaults to the workspace this turn is about", status: "requested"},
		{name: "several match", workspace: "Second Project", status: "choose", reason: "several_workspaces_match"},
		{name: "none match", workspace: "Album", status: "not_found", reason: "workspace_not_found"},
		{name: "another user's by name", workspace: "Foreign Plans", status: "not_found", reason: "workspace_not_found"},
		{name: "another user's by id", id: foreign.ID, status: "not_found", reason: "workspace_not_found"},
		{name: "trashed", id: trashed.ID, status: "not_found", reason: "workspace_not_found"},
		{name: "an unknown id is not replaced by a name", id: "ws-nope", workspace: "Album-1", status: "not_found", reason: "workspace_not_found"},
		{name: "agent in the roster", id: f.alpha.ID, agent: "ENGINEER", status: "requested"},
		{name: "agent elsewhere in the app", id: f.alpha.ID, agent: "Editor", status: "not_found", reason: "agent_not_in_workspace"},
		{name: "agent in another workspace's roster", id: f.beta.ID, agent: "Mixer", status: "not_found", reason: "agent_not_in_workspace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := turn()
			result := current.open(strings.TrimSpace(tc.id), strings.TrimSpace(tc.workspace), tc.agent)
			if result["status"] != tc.status || (tc.reason != "" && result["reason"] != tc.reason) {
				t.Fatalf("got %v", result)
			}
			if (current.staged != nil) != (tc.status == "requested") {
				t.Fatalf("only an exact match stages a request: staged=%v", current.staged != nil)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "Foreign Plans") || strings.Contains(string(encoded), foreign.ID) {
				t.Fatal("another user's workspace was disclosed")
			}
		})
	}
	// A choice lists the candidates and stages nothing.
	result := turn().open("", "second project", "")
	choices, _ := result["choices"].([]operationChoice)
	if len(choices) != 2 || choices[0].WorkspaceID == choices[1].WorkspaceID {
		t.Fatalf("choices: %+v", result)
	}
	// A near miss offers the closest names so the assistant can ask.
	near := turn().open("", "Album", "")
	if closest, _ := near["closest"].([]operationChoice); len(closest) != 1 || closest[0].Name != "Album-1" {
		t.Fatalf("closest: %+v", near)
	}
	missing := turn().open(f.alpha.ID, "", "Editor")
	if roster, _ := missing["closest"].([]operationChoice); len(roster) != 2 {
		t.Fatalf("a missing agent lists this workspace's roster: %+v", missing)
	}
}

func TestOperationRegistry_AdvertisesExactlyTheSkillsOperations(t *testing.T) {
	f := newOperationFixture(t)
	registry := &operationRegistry{base: newHomeToolRegistry(f.handler.Sources), turn: &operationTurn{handler: f.handler}}
	var advertised []string
	for _, tool := range registry.Definitions() {
		if strings.HasPrefix(tool.Name, "assistant_operate_") {
			advertised = append(advertised, tool.Name)
			if !assistantops.IsTool(tool.Name) || tool.Parameters["additionalProperties"] != false {
				t.Fatalf("%s is not a closed, registered operation", tool.Name)
			}
		}
	}
	skill, err := assistantops.LoadSkill()
	if err != nil {
		t.Fatal(err)
	}
	declared := slices.Clone(skill.Tools)
	slices.Sort(advertised)
	slices.Sort(declared)
	if !slices.Equal(advertised, declared) || !slices.Equal(advertised, assistantops.ToolNames()) {
		t.Fatalf("registry %v, skill %v, vocabulary %v must agree", advertised, declared, assistantops.ToolNames())
	}
	// Unknown arguments are refused, not ignored: the model cannot pass a URL,
	// a selector or a script through this tool.
	turn := &operationTurn{handler: f.handler, scope: f.handler.bindWorkspaceTurn(context.Background(), "open", f.refs, f.work)}
	if _, err := (&operationRegistry{base: registry.base, turn: turn}).Execute(context.Background(), assistantops.ToolOpen,
		`{"workspace_id":"`+f.alpha.ID+`","url":"javascript:alert(1)"}`); err == nil || turn.staged != nil {
		t.Fatal("an argument outside the schema must refuse the call")
	}
}

func (f *operationFixture) server(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/home-assistant/operations/capability", f.handler.OperationCapabilityHandler)
	mux.HandleFunc("GET /api/home-assistant/operations/{id}", f.handler.OperationHandler)
	mux.HandleFunc("POST /api/home-assistant/operations/{id}/ack", f.handler.OperationAckHandler)
	mux.HandleFunc("POST /api/home-assistant/operations/{id}/watch", f.handler.OperationWatchHandler)
	mux.HandleFunc("POST /api/home-assistant/operations/{id}/cancel", f.handler.OperationCancelHandler)
	mux.HandleFunc("POST /api/home-assistant/operations/{id}/start", f.handler.OperationStartHandler)
	return mux
}

func operationCall(t *testing.T, server http.Handler, method, path string, body any) (int, map[string]json.RawMessage) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%s %s must not be cached", method, path)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s %s: invalid JSON %q", method, path, recorder.Body.String())
	}
	return recorder.Code, out
}

func decodeOperation(t *testing.T, payload map[string]json.RawMessage) assistantops.Snapshot {
	t.Helper()
	var snapshot assistantops.Snapshot
	if err := json.Unmarshal(payload["operation"], &snapshot); err != nil {
		t.Fatalf("no operation in %v", payload)
	}
	return snapshot
}

func errorCode(payload map[string]json.RawMessage) string {
	var code string
	_ = json.Unmarshal(payload["code"], &code)
	return code
}

func TestOperationEndpoints_DeliverToOneTabAndRefuseForgedOrStaleCalls(t *testing.T) {
	f := newOperationFixture(t)
	f.provider.script = []llm.ChatResponse{
		toolCall(assistantops.ToolOpen, map[string]any{"workspace_id": f.alpha.ID, "agent_name": "Mixer"}),
		{Content: "I asked Ori to open Album-1 and select Mixer."},
	}
	created := f.ask("Open the Album-1 workspace and select Mixer", true).Operation
	if created == nil {
		t.Fatal("fixture operation missing")
	}
	server, base := f.server(t), "/api/home-assistant/operations/"+created.ID

	status, payload := operationCall(t, server, http.MethodGet, "/api/home-assistant/operations/capability", nil)
	if status != http.StatusOK || string(payload["available"]) != "true" {
		t.Fatalf("capability: %d %v", status, payload)
	}
	// A second tab reads the same facts and is handed no command.
	status, payload = operationCall(t, server, http.MethodGet, base+"?tab=tab-b", nil)
	if other := decodeOperation(t, payload); status != http.StatusOK || other.Watching || len(other.Commands) != 0 || other.Pending != 2 {
		t.Fatalf("second tab: %d %+v", status, other)
	}
	ack := func(tab, command string, revision int64, observed *assistantops.Observation) (int, map[string]json.RawMessage) {
		return operationCall(t, server, http.MethodPost, base+"/ack", map[string]any{
			"command_id": command, "revision": revision, "tab_id": tab, "outcome": "acknowledged", "observed": observed})
	}
	arrived := &assistantops.Observation{Path: "/workspaces/album-1"}
	for name, tc := range map[string]struct {
		tab, command string
		revision     int64
		observed     *assistantops.Observation
		status       int
		code         string
	}{
		"forged command":   {"tab-a", "not-a-command", created.Revision, arrived, http.StatusBadRequest, "operation_invalid"},
		"second tab":       {"tab-b", created.Commands[0].ID, created.Revision, arrived, http.StatusConflict, "operation_tab_mismatch"},
		"stale revision":   {"tab-a", created.Commands[0].ID, created.Revision + 3, arrived, http.StatusConflict, "operation_conflict"},
		"another page":     {"tab-a", created.Commands[0].ID, created.Revision, &assistantops.Observation{Path: "/workspaces/second-project"}, http.StatusBadRequest, "operation_invalid"},
		"malformed tab id": {"tab a<script>", created.Commands[0].ID, created.Revision, arrived, http.StatusBadRequest, "operation_invalid"},
		"out of order":     {"tab-a", created.Commands[1].ID, created.Revision, &assistantops.Observation{AgentKey: "Mixer"}, http.StatusConflict, "operation_conflict"},
	} {
		if status, payload := ack(tc.tab, tc.command, tc.revision, tc.observed); status != tc.status || errorCode(payload) != tc.code {
			t.Errorf("%s: got %d %s", name, status, errorCode(payload))
		}
	}
	if status, payload := operationCall(t, server, http.MethodGet, "/api/home-assistant/operations/not-an-operation?tab=tab-a", nil); status != http.StatusNotFound || errorCode(payload) != "operation_not_found" {
		t.Fatalf("forged operation: %d %v", status, payload)
	}
	if stored := f.operations.all(); stored[0].Revision != created.Revision || len(stored[0].Receipts) != 0 {
		t.Fatal("refused calls must write nothing")
	}

	status, payload = ack("tab-a", created.Commands[0].ID, created.Revision, arrived)
	opened := decodeOperation(t, payload)
	if status != http.StatusOK || opened.Pending != 1 || opened.Receipts[0].Kind != assistantops.ReceiptViewOpened {
		t.Fatalf("arrival: %d %+v", status, opened)
	}
	// A reload after the acknowledgement repeats it with the old revision: harmless.
	if status, payload = ack("tab-a", created.Commands[0].ID, created.Revision, arrived); status != http.StatusOK || decodeOperation(t, payload).Revision != opened.Revision {
		t.Fatalf("repeat after refresh: %d", status)
	}
	status, payload = ack("tab-a", created.Commands[1].ID, opened.Revision, &assistantops.Observation{AgentKey: "mixer"})
	selected := decodeOperation(t, payload)
	if status != http.StatusOK || selected.Pending != 0 || selected.Receipts[1].Kind != assistantops.ReceiptAgentSelected {
		t.Fatalf("selection: %d %+v", status, selected)
	}

	// Watching off, then dismissing, changes no workspace record.
	status, payload = operationCall(t, server, http.MethodPost, base+"/watch", map[string]any{"tab_id": "tab-a", "revision": selected.Revision, "watch": false})
	off := decodeOperation(t, payload)
	if status != http.StatusOK || off.Watching || off.Status != assistantops.StatusRequested {
		t.Fatalf("watch off: %d %+v", status, off)
	}
	status, payload = operationCall(t, server, http.MethodPost, base+"/cancel", map[string]any{"tab_id": "tab-a", "revision": off.Revision})
	if cancelled := decodeOperation(t, payload); status != http.StatusOK || cancelled.Status != assistantops.StatusCancelled {
		t.Fatalf("cancel: %d %+v", status, cancelled)
	}
	if status, payload = ack("tab-a", created.Commands[1].ID, off.Revision+1, &assistantops.Observation{AgentKey: "mixer"}); status != http.StatusConflict || errorCode(payload) != "operation_ended" {
		t.Fatalf("an ended operation accepts nothing: %d %s", status, errorCode(payload))
	}
}

func TestOperationEndpoints_StaleContextEndsContinuation(t *testing.T) {
	for name, change := range map[string]func(*operationFixture){
		"relationship changed": func(f *operationFixture) { f.work.StateVersion++ },
		"assistant replaced":   func(f *operationFixture) { f.work.AssistantID = "assistant-2" },
		"assistant not ready":  func(f *operationFixture) { f.work.State = "repair_needed" },
		"conversation deleted": func(f *operationFixture) { delete(f.conversations.sessions, "canonical") },
		"conversation no longer this assistant's": func(f *operationFixture) {
			f.conversations.getErr = ErrPersonalAssistantConversationOwnerChanged
		},
		"workspace trashed": func(f *operationFixture) {
			_ = f.store.Update(f.alpha.ID, func(ws *workspace.Workspace) error { ws.Status = workspace.StatusTrashed; return nil })
		},
		"workspace handed to another user": func(f *operationFixture) {
			_ = f.store.Update(f.alpha.ID, func(ws *workspace.Workspace) error { ws.OwnerUserID = "someone-else"; return nil })
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newOperationFixture(t)
			f.provider.script = []llm.ChatResponse{toolCall(assistantops.ToolOpen, map[string]any{"workspace_id": f.alpha.ID}), {Content: "Asked."}}
			created := f.ask("Open the Album-1 workspace", true).Operation
			if created == nil {
				t.Fatal("fixture operation missing")
			}
			change(f)
			server, base := f.server(t), "/api/home-assistant/operations/"+created.ID
			status, payload := operationCall(t, server, http.MethodPost, base+"/ack", map[string]any{
				"command_id": created.Commands[0].ID, "revision": created.Revision, "tab_id": "tab-a", "outcome": "acknowledged",
				"observed": map[string]any{"path": "/workspaces/album-1"}})
			if status != http.StatusGone || errorCode(payload) != "operation_invalidated" {
				t.Fatalf("ack after the change: %d %s", status, errorCode(payload))
			}
			status, payload = operationCall(t, server, http.MethodGet, base+"?tab=tab-a", nil)
			if ended := decodeOperation(t, payload); status != http.StatusOK || ended.Status != assistantops.StatusInvalidated || ended.Pending != 0 {
				t.Fatalf("the ended operation explains itself and hands out nothing: %d %+v", status, ended)
			}
		})
	}
}

func TestOperationEndpoints_UnwiredInstallationReportsUnavailable(t *testing.T) {
	f := newOperationFixture(t)
	f.handler.SetOperationStore(nil)
	server := f.server(t)
	status, payload := operationCall(t, server, http.MethodGet, "/api/home-assistant/operations/capability", nil)
	if status != http.StatusOK || string(payload["available"]) != "false" || !strings.Contains(string(payload["reason"]), operationsNotWired) {
		t.Fatalf("capability: %d %v", status, payload)
	}
	if status, payload = operationCall(t, server, http.MethodGet, "/api/home-assistant/operations/anything?tab=tab-a", nil); status != http.StatusServiceUnavailable || errorCode(payload) != "operation_unavailable" {
		t.Fatalf("read: %d %v", status, payload)
	}
	f.provider.script = []llm.ChatResponse{{Content: "It is under Workspaces."}}
	response := f.ask("Open the Album-1 workspace", true)
	if response.Operation != nil || slices.Contains(offeredTools(f.provider.requests[0]), assistantops.ToolOpen) {
		t.Fatal("an unwired installation offered an operation")
	}
}

func TestRoute_AnOperationRequestHasOneOwner(t *testing.T) {
	handler, _ := newHiredRouteHandler(t, "active")
	for _, prompt := range []string{"Open the Email Ops workspace", "open my thesis workspace", "Select the Mixer agent"} {
		supported := false
		handler.OperationRequest = func(context.Context, string) bool { return supported }
		before, err := handler.RoutePrompt(context.Background(), prompt, homePanelRouteContext())
		if err != nil {
			t.Fatal(err)
		}
		supported = true
		after, err := handler.RoutePrompt(context.Background(), prompt, homePanelRouteContext())
		if err != nil {
			t.Fatal(err)
		}
		requireConversationRoute(t, after)
		// Where Ori cannot operate, the existing owner keeps the request.
		handler.OperationRequest = nil
		unwired, err := handler.RoutePrompt(context.Background(), prompt, homePanelRouteContext())
		if err != nil || unwired.Intent != before.Intent || unwired.RouteMode != before.RouteMode {
			t.Fatalf("%q: an unsupported request changed owner: %+v vs %+v (%v)", prompt, unwired, before, err)
		}
	}
	// The check is the panel's only: a prompt from outside it is not diverted.
	outsidePanel := &HomeAssistantRouteContext{Surface: "home", PagePath: "/"}
	baseline, err := handler.RoutePrompt(context.Background(), "Open the Email Ops workspace", outsidePanel)
	if err != nil {
		t.Fatal(err)
	}
	handler.OperationRequest = func(context.Context, string) bool { return true }
	outside, err := handler.RoutePrompt(context.Background(), "Open the Email Ops workspace", outsidePanel)
	if err != nil || outside.Intent != baseline.Intent || outside.RouteMode != baseline.RouteMode {
		t.Fatalf("outside the panel: %+v vs %+v (%v)", outside, baseline, err)
	}
}
