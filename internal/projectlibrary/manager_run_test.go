package projectlibrary

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// --- scripted model and test tools -----------------------------------------

type scriptedChat struct {
	mu       sync.Mutex
	steps    []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error)
	requests []llm.ChatRequest
}

func (c *scriptedChat) chat(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	c.mu.Lock()
	step := len(c.requests)
	c.requests = append(c.requests, request)
	c.mu.Unlock()
	if step >= len(c.steps) {
		return &llm.ChatResponse{Content: "Nothing more to suggest."}, nil
	}
	return c.steps[step](ctx, request)
}

func (c *scriptedChat) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func toolCall(id, name string, args any) llm.ToolCall {
	encoded, _ := json.Marshal(args)
	return llm.ToolCall{ID: id, Name: name, Arguments: string(encoded)}
}

func callTools(calls ...llm.ToolCall) func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{ToolCalls: calls, Usage: llm.Usage{TotalTokens: 100}}, nil
	}
}

type runTestTool struct {
	name string
	call func(context.Context, string) (string, error)
}

func (tool runTestTool) Definition() toolapi.ToolDefinition {
	return toolapi.ToolDefinition{Name: tool.name, Description: tool.name,
		Parameters: map[string]any{"type": "object"}}
}

func (tool runTestTool) Call(ctx context.Context, args string) (string, error) {
	return tool.call(ctx, args)
}

func proposalReply(proposal ManagerProposal, replay bool, err error) (string, error) {
	if err != nil {
		return "", err
	}
	encoded, _ := json.Marshal(map[string]any{"proposal_id": proposal.ID, "replay": replay})
	return string(encoded), nil
}

// runTestTools mirrors the chat adapter: every call goes through the real
// Store method with the authority (and Run context) the runner handed over.
// The extra write tool must never reach the model or run.
func runTestTools(store *Store, forbiddenRan *atomic.Bool) func(ManagerAuthority) []toolapi.Tool {
	return func(authority ManagerAuthority) []toolapi.Tool {
		return []toolapi.Tool{
			runTestTool{"home_library_search", func(context.Context, string) (string, error) {
				page, err := store.SearchForManager(authority, Search{Sort: "name"})
				if err != nil {
					return "", err
				}
				encoded, _ := json.Marshal(page)
				return string(encoded), nil
			}},
			runTestTool{"home_library_propose_next_action", func(_ context.Context, raw string) (string, error) {
				var in struct {
					EntryID        string `json:"entry_id"`
					FieldsRevision int64  `json:"fields_revision"`
					NextAction     string `json:"next_action"`
					Reason         string `json:"reason"`
					RequestKey     string `json:"request_key"`
				}
				if err := json.Unmarshal([]byte(raw), &in); err != nil {
					return "", err
				}
				return proposalReply(store.ProposeNextAction(authority, in.EntryID, in.FieldsRevision, in.NextAction,
					in.Reason, in.RequestKey))
			}},
			runTestTool{"home_library_propose_project_review", func(_ context.Context, raw string) (string, error) {
				var in struct {
					EntryID       string `json:"entry_id"`
					EntryRevision int64  `json:"entry_revision"`
					Reason        string `json:"reason"`
					RequestKey    string `json:"request_key"`
				}
				if err := json.Unmarshal([]byte(raw), &in); err != nil {
					return "", err
				}
				return proposalReply(store.ProposeProjectReview(authority, in.EntryID, in.EntryRevision, in.Reason,
					in.RequestKey))
			}},
			runTestTool{"home_library_propose_root_review", func(_ context.Context, raw string) (string, error) {
				var in struct {
					Reason     string `json:"reason"`
					RequestKey string `json:"request_key"`
				}
				if err := json.Unmarshal([]byte(raw), &in); err != nil {
					return "", err
				}
				return proposalReply(store.ProposeRootReview(authority, in.Reason, in.RequestKey))
			}},
			runTestTool{"workspace_note_write", func(context.Context, string) (string, error) {
				forbiddenRan.Store(true)
				return "wrote a note", nil
			}},
		}
	}
}

// --- fixture ---------------------------------------------------------------

type runFixture struct {
	runner    *ManagerRunner
	library   *Store
	scope     Scope
	file      *workspace.FileStore
	tree      musicTree
	scanID    string
	chat      *scriptedChat
	available *atomic.Bool
	forbidden *atomic.Bool
	installed activationPlugins
	roots     *Roots
}

func bindTestManager(t *testing.T, file *workspace.FileStore, homeID string, roles []workspace.AssistantProgramRoleSpec,
	bindings []workspace.AssistantRoleBinding) {
	t.Helper()
	if err := file.Update(homeID, func(home *workspace.Workspace) error {
		home.AgentInstances = nil
		for _, binding := range bindings {
			home.AgentInstances = append(home.AgentInstances, workspace.AgentInstance{
				ID: binding.AgentInstanceID, Name: binding.AgentName, RoleID: binding.RoleID})
		}
		state := home.GetAssistantProgramState()
		state.Declaration = &workspace.AssistantProgramDeclaration{Roles: roles}
		state.HomeBindings = workspace.AssistantRoleBindingSet{StateRevision: state.HomeBindings.StateRevision + 1,
			Bindings: bindings}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if err := file.SaveWorkspaceAgent(homeID, binding.AgentName, &agent.Agent{}); err != nil {
			t.Fatal(err)
		}
	}
}

var (
	managerRole = workspace.AssistantProgramRoleSpec{ID: "manager", Scope: workspace.AssistantRoleScopeHome,
		Required: true, Primary: true}
	managerBinding = workspace.AssistantRoleBinding{RoleID: "manager", AgentInstanceID: "local-manager",
		AgentName: "Manager"}
)

func newRunFixture(t *testing.T) *runFixture {
	t.Helper()
	_, scope, roots, file, tree, installed := activationFixture(t)
	bindTestManager(t, file, scope.HomeID, []workspace.AssistantProgramRoleSpec{managerRole},
		[]workspace.AssistantRoleBinding{managerBinding})
	f := &runFixture{scope: scope, file: file, tree: tree, chat: &scriptedChat{}, available: &atomic.Bool{},
		forbidden: &atomic.Bool{}, installed: installed, roots: roots}
	f.available.Store(true)
	f.library = NewStore(file).WithProviderEvidence(func(Scope, *workspace.Workspace) bool {
		return f.available.Load()
	}).WithInstalledPlugins(installed)
	roots.library = f.library
	doc, err := f.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	f.scanID = scanRoot(t, roots, scope, doc.Roots[0].ID, "run-fixture-scan").ID
	f.runner = f.newRunner(f.library)
	return f
}

func (f *runFixture) newRunner(library *Store) *ManagerRunner {
	return NewManagerRunner(library, ManagerRunHost{
		Tools: runTestTools(library, f.forbidden),
		Model: func(context.Context, string, string) (ManagerChat, string, error) {
			return f.chat.chat, "test-model", nil
		},
	})
}

func (f *runFixture) doc(t *testing.T) Document {
	t.Helper()
	doc, err := f.library.Read(f.scope)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func nextActionArgs(t *testing.T, f *runFixture, entryID, key string) map[string]any {
	t.Helper()
	entry := sessionEntry(f.doc(t), entryID)
	if entry == nil {
		t.Fatalf("no entry %s", entryID)
	}
	return map[string]any{"entry_id": entryID, "fields_revision": entry.Fields.Revision,
		"next_action": "Listen through " + entryID, "reason": "<b>untrusted</b> note", "request_key": key}
}

func runProposals(doc Document, scanID string) []ManagerProposal {
	var out []ManagerProposal
	for _, proposal := range doc.Proposals {
		if proposal.ScanID == scanID {
			out = append(out, proposal)
		}
	}
	return out
}

// --- tests -----------------------------------------------------------------

func TestManagerRun_FilesProvenanceCarryingProposalsOncePerScan(t *testing.T) {
	f := newRunFixture(t)
	f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){
		func(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
			return callTools(
				toolCall("1", "home_library_search", map[string]any{}),
				toolCall("2", "workspace_note_write", map[string]any{"text": "not allowed"}),
				toolCall("3", "home_library_propose_next_action", nextActionArgs(t, f, "single", "scan-review-a")),
			)(ctx, request)
		},
		func(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
			entry := sessionEntry(f.doc(t), "alternates")
			return callTools(toolCall("4", "home_library_propose_project_review", map[string]any{
				"entry_id": "alternates", "entry_revision": entry.Revision, "reason": "Pick a take",
				"request_key": "scan-review-b"}))(ctx, request)
		},
	}
	before := fileDigest(t, filepath.Join(f.tree.single, "Song.rpp"))
	workspacesBefore, _ := f.file.List()
	run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
	if err != nil || run.Status != runFinished || run.Reason != "" || run.Proposals != 2 ||
		run.Model != "test-model" || run.AgentName != "Manager" || run.Tokens != 200 || run.FinishedAt == nil {
		t.Fatalf("run receipt: %+v %v", run, err)
	}
	if f.forbidden.Load() {
		t.Fatal("a tool outside the allowlist ran")
	}
	first := f.chat.requests[0]
	for _, tool := range first.Tools {
		if !managerRunReadTools[tool.Name] && !managerRunProposalTools[tool.Name] {
			t.Fatalf("model was offered %q", tool.Name)
		}
	}
	prompt := first.SystemPrompt + first.Messages[0].Content
	for _, leak := range []string{f.tree.root, "Song.rpp", "Single", "Alternates"} {
		if strings.Contains(prompt, leak) {
			t.Fatalf("prompt leaks %q: %s", leak, prompt)
		}
	}
	if !strings.Contains(first.Messages[0].Content, "observed 4 projects") {
		t.Fatalf("prompt lacks digest counts: %s", first.Messages[0].Content)
	}
	refused := f.chat.requests[1].Messages[2] // assistant, then the tool results in call order
	if refused.Role != llm.RoleTool || refused.Name != "home_library_search" {
		t.Fatalf("unexpected message order: %+v", f.chat.requests[1].Messages)
	}
	if got := f.chat.requests[1].Messages[3].Content; !strings.Contains(got, "not available in a scan review") {
		t.Fatalf("forbidden tool was not refused to the model: %s", got)
	}
	doc := f.doc(t)
	saved := runProposals(doc, f.scanID)
	if len(saved) != 2 {
		t.Fatalf("want 2 run proposals, got %+v", saved)
	}
	for _, proposal := range saved {
		if proposal.Source != sourceManagerModel || proposal.Model != "test-model" || proposal.ScanID != f.scanID {
			t.Fatalf("proposal lacks provenance: %+v", proposal)
		}
	}
	if saved[0].Reason != "<b>untrusted</b> note" {
		t.Fatalf("reason was not stored verbatim as inert text: %q", saved[0].Reason)
	}
	page, err := f.library.ListManagerProposals(f.scope)
	if err != nil || page.Total != 2 {
		t.Fatalf("shelf: %+v %v", page, err)
	}
	for _, row := range page.Rows {
		if row.Status != "ready" {
			t.Fatalf("run proposal is not reviewable: %+v", row)
		}
	}
	summary, err := f.library.Summary(f.scope)
	if err != nil || summary.ProposalRun == nil || summary.ProposalRun.Status != runFinished || summary.ReadyProposals != 2 {
		t.Fatalf("summary: %+v %v", summary, err)
	}
	calls := f.chat.calls()
	if again, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID); err != nil || again.Status != runFinished ||
		f.chat.calls() != calls {
		t.Fatalf("replayed event ran a second turn: %+v %v (calls %d -> %d)", again, err, calls, f.chat.calls())
	}
	path, _ := f.file.GetFolderPath(f.scope.HomeID)
	reopened, err := workspace.NewFileStore(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	other := NewStore(reopened).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return true })
	if again, err := f.newRunner(other).Run(context.Background(), f.scope.HomeID, f.scanID); err != nil ||
		again.Status != runFinished || f.chat.calls() != calls {
		t.Fatalf("a second process ran a second turn: %+v %v", again, err)
	}
	workspacesAfter, _ := f.file.List()
	if len(workspacesAfter) != len(workspacesBefore) || fileDigest(t, filepath.Join(f.tree.single, "Song.rpp")) != before {
		t.Fatal("the review turn created a workspace or touched a project file")
	}
	for _, proposal := range doc.Proposals {
		if proposal.Kind == "" && proposal.EntryID == "single" && sessionEntry(doc, "single").Fields.NextAction != "" {
			t.Fatal("a suggestion edited the project's notes")
		}
	}
}

func TestManagerRun_FourthProposalIsRefusedAndEndsTheTurn(t *testing.T) {
	f := newRunFixture(t)
	f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){
		func(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
			entry := sessionEntry(f.doc(t), "alternates")
			return callTools(
				toolCall("1", "home_library_propose_next_action", nextActionArgs(t, f, "single", "scan-review-1")),
				toolCall("2", "home_library_propose_next_action", nextActionArgs(t, f, "unsupported", "scan-review-2")),
				toolCall("3", "home_library_propose_project_review", map[string]any{"entry_id": "alternates",
					"entry_revision": entry.Revision, "request_key": "scan-review-3"}),
				toolCall("4", "home_library_propose_root_review", map[string]any{"request_key": "scan-review-4"}),
			)(ctx, request)
		},
	}
	run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
	if err != nil || run.Status != runFinished || run.Reason != "proposal_limit" || run.Proposals != 3 {
		t.Fatalf("cap: %+v %v", run, err)
	}
	if got := len(runProposals(f.doc(t), f.scanID)); got != 3 || f.chat.calls() != 1 {
		t.Fatalf("want 3 proposals in one model call, got %d proposals and %d calls", got, f.chat.calls())
	}
	// The cap is the store's, not only the loop's: a fourth write under the
	// same scan is refused even when it bypasses the loop.
	authority := ManagerAuthority{HomeID: f.scope.HomeID, AgentInstanceID: "local-manager", AgentName: "Manager",
		Run: ManagerRunContext{ScanID: f.scanID, Model: "test-model"}}
	if _, _, err := f.library.ProposeRootReview(authority, "", "scan-review-late"); !errors.Is(err, ErrConflict) {
		t.Fatalf("a finished turn accepted another proposal: %v", err)
	}
}

func TestManagerRun_StoreCapAndDedupeInsideTheFencedWrite(t *testing.T) {
	f := newRunFixture(t)
	if _, claimed, err := f.library.claimProposalRun(f.scope, ProposalRun{ScanID: f.scanID, Status: runStarted,
		AgentName: "Manager", Model: "test-model", StartedAt: time.Now().UTC()}); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	authority := ManagerAuthority{HomeID: f.scope.HomeID, AgentInstanceID: "local-manager", AgentName: "Manager",
		Run: ManagerRunContext{ScanID: f.scanID, Model: "test-model"}}
	propose := func(entryID, key string) error {
		entry := sessionEntry(f.doc(t), entryID)
		_, _, err := f.library.ProposeNextAction(authority, entryID, entry.Fields.Revision, "Next", "", key)
		return err
	}
	if err := propose("single", "k1"); err != nil {
		t.Fatal(err)
	}
	if err := propose("single", "k2"); !errors.Is(err, ErrDuplicateProposal) {
		t.Fatalf("duplicate (entry, kind) was saved: %v", err)
	}
	if err := propose("alternates", "k3"); err != nil {
		t.Fatal(err)
	}
	if err := propose("unsupported", "k4"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.library.ProposeRootReview(authority, "", "k5"); !errors.Is(err, ErrLimit) {
		t.Fatalf("fourth proposal under one scan was saved: %v", err)
	}
	// Chat (no Run) keeps its old behavior and carries no provenance.
	chat := authority
	chat.Run = ManagerRunContext{}
	proposal, _, err := f.library.ProposeRootReview(chat, "", "chat-root")
	if err != nil || proposal.Source != "" || proposal.ScanID != "" {
		t.Fatalf("chat proposal: %+v %v", proposal, err)
	}
}

func TestManagerRun_TimeLimitKeepsSavedProposals(t *testing.T) {
	f := newRunFixture(t)
	f.runner.timeout = 300 * time.Millisecond
	f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){
		func(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
			return callTools(toolCall("1", "home_library_propose_next_action",
				nextActionArgs(t, f, "single", "scan-review-t")))(ctx, request)
		},
		func(ctx context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
	if err != nil || run.Status != runFinished || run.Reason != "time_limit" || run.Proposals != 1 {
		t.Fatalf("time limit: %+v %v", run, err)
	}
}

func TestManagerRun_TokenBudgetFromSettingsEndsTheTurn(t *testing.T) {
	f := newRunFixture(t)
	f.runner.host.TokenBudget = func() int { return 150 }
	f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){
		func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{Usage: llm.Usage{PromptTokens: 120, CompletionTokens: 80},
				ToolCalls: []llm.ToolCall{toolCall("1", "home_library_search", map[string]any{})}}, nil
		},
	}
	run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
	if err != nil || run.Status != runFinished || run.Reason != "token_limit" || run.Tokens != 200 || f.chat.calls() != 1 {
		t.Fatalf("token budget: %+v %v", run, err)
	}
	f.runner.host.TokenBudget = func() int { return 10_000_000 }
	if got := f.runner.tokenBudget(); got != DefaultManagerTokenBudget {
		t.Fatalf("settings raised the budget above the default: %d", got)
	}
	f.runner.host.TokenBudget = func() int { return 0 }
	if got := f.runner.tokenBudget(); got != DefaultManagerTokenBudget {
		t.Fatalf("zero must mean the default: %d", got)
	}
}

func TestManagerRun_ConcurrentEventsAndTwoProcessesProduceOneTurn(t *testing.T) {
	f := newRunFixture(t)
	path, _ := f.file.GetFolderPath(f.scope.HomeID)
	reopened, err := workspace.NewFileStore(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	other := f.newRunner(NewStore(reopened).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return true }))
	var wg sync.WaitGroup
	results := make([]ProposalRun, 4)
	errs := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			runner := f.runner
			if i%2 == 1 {
				runner = other
			}
			results[i], errs[i] = runner.Run(context.Background(), f.scope.HomeID, f.scanID)
		}(i)
	}
	wg.Wait()
	if calls := f.chat.calls(); calls > 1 {
		t.Fatalf("more than one turn ran: %d model calls", calls)
	}
	doc := f.doc(t)
	count := 0
	for _, run := range doc.ProposalRuns {
		if run.ScanID == f.scanID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want exactly one receipt, got %d: %+v", count, doc.ProposalRuns)
	}
	for i, err := range errs {
		if err != nil && !errors.Is(err, ErrConflict) {
			t.Fatalf("event %d: %v", i, err)
		}
	}
}

func TestManagerRun_AuthorityRefusalsRecordASkipWithoutAModelCall(t *testing.T) {
	cases := map[string]struct {
		setup  func(*testing.T, *runFixture)
		reason string
	}{
		"unbound Manager": {func(t *testing.T, f *runFixture) {
			bindTestManager(t, f.file, f.scope.HomeID, []workspace.AssistantProgramRoleSpec{managerRole}, nil)
		}, "no_manager"},
		"optional Sample Manager only": {func(t *testing.T, f *runFixture) {
			optional := workspace.AssistantProgramRoleSpec{ID: "sample_manager", Scope: workspace.AssistantRoleScopeHome}
			bindTestManager(t, f.file, f.scope.HomeID, []workspace.AssistantProgramRoleSpec{managerRole, optional},
				[]workspace.AssistantRoleBinding{{RoleID: "sample_manager", AgentInstanceID: "local-sample", AgentName: "Sampler"}})
		}, "no_manager"},
		"no model": {func(_ *testing.T, f *runFixture) {
			f.runner.host.Model = func(context.Context, string, string) (ManagerChat, string, error) {
				return nil, "", ErrNoManagerModel
			}
		}, "no_model"},
		"provider unavailable": {func(_ *testing.T, f *runFixture) {
			f.available.Store(false)
		}, "provider_unavailable"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRunFixture(t)
			tc.setup(t, f)
			run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
			if err != nil || run.Status != runSkipped || run.Reason != tc.reason || f.chat.calls() != 0 {
				t.Fatalf("%s: %+v %v (model calls %d)", name, run, err, f.chat.calls())
			}
			f.available.Store(true)
			summary, err := f.library.Summary(f.scope)
			if err != nil || summary.ProposalRun == nil || summary.ProposalRun.Reason != tc.reason {
				t.Fatalf("skip reason is not visible on the summary: %+v %v", summary.ProposalRun, err)
			}
		})
	}
}

func TestManagerRun_MidTurnAuthorityLossAndForeignArgumentsSaveNothing(t *testing.T) {
	cases := map[string]func(*testing.T, *runFixture) []llm.ToolCall{
		"binding removed mid-run": func(t *testing.T, f *runFixture) []llm.ToolCall {
			args := nextActionArgs(t, f, "single", "scan-review-x")
			bindTestManager(t, f.file, f.scope.HomeID, []workspace.AssistantProgramRoleSpec{managerRole}, nil)
			return []llm.ToolCall{toolCall("1", "home_library_propose_next_action", args)}
		},
		"provider lost mid-run": func(t *testing.T, f *runFixture) []llm.ToolCall {
			args := nextActionArgs(t, f, "single", "scan-review-y")
			f.available.Store(false)
			return []llm.ToolCall{toolCall("1", "home_library_propose_next_action", args)}
		},
		"foreign entry id": func(_ *testing.T, _ *runFixture) []llm.ToolCall {
			return []llm.ToolCall{toolCall("1", "home_library_propose_next_action", map[string]any{
				"entry_id": "entry-from-another-home", "fields_revision": 0, "next_action": "x",
				"request_key": "scan-review-z"})}
		},
	}
	for name, mid := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRunFixture(t)
			f.chat.steps = []func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error){
				func(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
					return callTools(mid(t, f)...)(ctx, request)
				},
			}
			run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
			f.available.Store(true)
			doc, readErr := f.library.Read(f.scope)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(runProposals(doc, f.scanID)) != 0 || len(doc.Proposals) != 0 {
				t.Fatalf("%s saved a proposal: %+v", name, doc.Proposals)
			}
			if err != nil || run.Proposals != 0 {
				t.Fatalf("%s receipt: %+v %v", name, run, err)
			}
		})
	}
}

func TestManagerRun_RestartClosesStartedRunsAsInterruptedWithoutRerunning(t *testing.T) {
	f := newRunFixture(t)
	stale := time.Now().UTC().Add(-10 * time.Minute)
	if _, claimed, err := f.library.claimProposalRun(f.scope, ProposalRun{ScanID: f.scanID, Status: runStarted,
		AgentName: "Manager", Model: "test-model", StartedAt: stale}); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	// Before the sweep the stored receipt is still "started", but no reader
	// presents it as live.
	if summary, err := f.library.Summary(f.scope); err != nil || summary.ProposalRun == nil ||
		summary.ProposalRun.Status != runSkipped || summary.ProposalRun.Reason != "interrupted" {
		t.Fatalf("stale started run presented as live: %+v %v", summary.ProposalRun, err)
	}
	path, _ := f.file.GetFolderPath(f.scope.HomeID)
	reopened, err := workspace.NewFileStore(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	restarted := f.newRunner(NewStore(reopened).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return true }))
	if closed := restarted.SweepInterrupted(); closed != 1 {
		t.Fatalf("sweep closed %d runs", closed)
	}
	if closed := restarted.SweepInterrupted(); closed != 0 {
		t.Fatalf("a second sweep changed %d runs", closed)
	}
	run, found, err := restarted.library.proposalRun(f.scope, f.scanID)
	if err != nil || !found || run.Status != runSkipped || run.Reason != "interrupted" || run.FinishedAt == nil {
		t.Fatalf("interrupted receipt: %+v %v", run, err)
	}
	if again, err := restarted.Run(context.Background(), f.scope.HomeID, f.scanID); err != nil ||
		again.Reason != "interrupted" || f.chat.calls() != 0 {
		t.Fatalf("an interrupted turn ran again: %+v %v", again, err)
	}
	// A fresh "started" receipt inside its deadline is left alone.
	fresh := newRunFixture(t)
	if _, _, err := fresh.library.claimProposalRun(fresh.scope, ProposalRun{ScanID: fresh.scanID, Status: runStarted,
		AgentName: "Manager", Model: "m", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if closed, err := fresh.library.CloseInterruptedProposalRuns(fresh.scope); err != nil || closed != 0 {
		t.Fatalf("a live run was swept: %d %v", closed, err)
	}
}

func TestManagerRun_OlderScanIsSupersededAndFinishReplaysUnchanged(t *testing.T) {
	f := newRunFixture(t)
	doc := f.doc(t)
	newer := scanRoot(t, f.roots, f.scope, doc.Roots[0].ID, "run-fixture-rescan")
	run, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID)
	if err != nil || run.Status != runSkipped || run.Reason != "superseded" || f.chat.calls() != 0 {
		t.Fatalf("older scan ran a turn: %+v %v", run, err)
	}
	current, err := f.runner.Run(context.Background(), f.scope.HomeID, newer.ID)
	if err != nil || current.Status != runFinished {
		t.Fatalf("current scan: %+v %v", current, err)
	}
	again, err := f.library.finishProposalRun(f.scope, newer.ID, runSkipped, "model_error", 999)
	if err != nil || again.Status != runFinished || again.Tokens != current.Tokens {
		t.Fatalf("finishing twice changed the receipt: %+v %v", again, err)
	}
}

func TestManagerRun_DocumentRejectsForgedReceiptsAndProvenance(t *testing.T) {
	f := newRunFixture(t)
	if _, err := f.runner.Run(context.Background(), f.scope.HomeID, f.scanID); err != nil {
		t.Fatal(err)
	}
	doc := f.doc(t)
	for name, forge := range map[string]func(*Document){
		"unknown scan":          func(d *Document) { d.ProposalRuns[0].ScanID = "nope" },
		"bad status":            func(d *Document) { d.ProposalRuns[0].Status = "running" },
		"skip reason on finish": func(d *Document) { d.ProposalRuns[0].Reason = "no_manager" },
		"too many proposals":    func(d *Document) { d.ProposalRuns[0].Proposals = 4 },
		"started with finish":   func(d *Document) { d.ProposalRuns[0].Status, d.ProposalRuns[0].Reason = runStarted, "" },
		"duplicate receipt":     func(d *Document) { d.ProposalRuns = append(d.ProposalRuns, d.ProposalRuns[0]) },
		"provenance without scan": func(d *Document) {
			d.Proposals = append(d.Proposals, ManagerProposal{Source: sourceManagerModel})
		},
	} {
		forged := doc
		forged.ProposalRuns = append([]ProposalRun(nil), doc.ProposalRuns...)
		forged.Proposals = append([]ManagerProposal(nil), doc.Proposals...)
		forge(&forged)
		if forged.valid(f.scope) {
			t.Fatalf("%s: forged document accepted", name)
		}
	}
	proposal := ManagerProposal{Source: "chat", ScanID: "x"}
	if proposal.provenanceValid() {
		t.Fatal("unknown provenance source accepted")
	}
}
