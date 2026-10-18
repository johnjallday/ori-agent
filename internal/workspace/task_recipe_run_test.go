package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/types"
)

// isolatingProvider is a scripted provider that can, or cannot, be held to an
// isolated call.
type isolatingProvider struct {
	scriptedProviderStub
	isolates bool
	chatErr  error
	// hold, when set, is called inside Chat: a test uses it to keep a call open.
	hold func()
}

func (p *isolatingProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsTools: true, SupportsNativeMCP: true, SupportsIsolatedChat: p.isolates}
}

func (p *isolatingProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if p.hold != nil {
		p.hold()
	}
	if p.chatErr != nil {
		p.requests = append(p.requests, req)
		return nil, p.chatErr
	}
	return p.scriptedProviderStub.Chat(ctx, req)
}

// recipeRunnerStub is a host recipe that hands over a fixed brief.
type recipeRunnerStub struct {
	ready    *TaskBlockedError
	briefErr error
	briefs   int
	windows  []int
	finished []string
	// empty makes Finish return no report at all.
	empty bool
}

func (r *recipeRunnerStub) Ready(Task) *TaskBlockedError { return r.ready }

func (r *recipeRunnerStub) Brief(_ context.Context, _ Task, window int) (RecipeBrief, error) {
	r.briefs++
	r.windows = append(r.windows, window)
	if r.briefErr != nil {
		return RecipeBrief{}, r.briefErr
	}
	return RecipeBrief{System: "HOST RULES", User: "HOST EVIDENCE", State: "state"}, nil
}

func (r *recipeRunnerStub) Finish(brief RecipeBrief, reply string) (string, error) {
	r.finished = append(r.finished, reply)
	if r.empty {
		return "  ", nil
	}
	return reply + "\n[host note " + brief.State.(string) + "]", nil
}

// countingRegistry records any attempt to reach an agent's tool servers.
type countingRegistry struct{ starts, lists int }

func (c *countingRegistry) GetToolsForServer(string) ([]toolapi.Tool, error) {
	c.lists++
	return []toolapi.Tool{taskHandlerToolStub{name: "run_shell"}}, nil
}
func (c *countingRegistry) StartServer(string) error        { c.starts++; return nil }
func (c *countingRegistry) ListServers() []mcp.ServerConfig { return nil }

type recipeRunFixture struct {
	handler  *LLMTaskHandler
	provider *isolatingProvider
	runner   *recipeRunnerStub
	registry *countingRegistry
	search   *taskHandlerToolFunc
	factory  int
}

// newRecipeRunFixture builds a handler whose one agent is as privileged as an
// agent gets here: a persona prompt, utility tools, workspace tools and tool
// servers. An ordinary task of its gets all of them.
func newRecipeRunFixture(t *testing.T, settings types.Settings) *recipeRunFixture {
	t.Helper()
	f := &recipeRunFixture{
		provider: &isolatingProvider{isolates: true, scriptedProviderStub: scriptedProviderStub{
			name: "openai", responses: []llm.ChatResponse{{Content: "## What it is\nA service."}},
		}},
		runner: &recipeRunnerStub{}, registry: &countingRegistry{},
		search: &taskHandlerToolFunc{name: "web_search", result: `{}`},
	}
	factory := llm.NewFactory()
	factory.Register("openai", f.provider)
	f.handler = &LLMTaskHandler{
		llmFactory: factory, mcpRegistry: f.registry,
		agentStore: &resolverAgentStoreStub{agents: map[string]*agent.Agent{"Robin": {Settings: settings}}},
	}
	f.handler.SetUtilityToolProvider(taskUtilityProviderStub{tools: map[string]toolapi.Tool{"web_search": f.search}})
	f.handler.SetWorkspaceToolFactory(func(string, string, string) []toolapi.Tool {
		f.factory++
		return []toolapi.Tool{taskHandlerToolStub{name: "workspace_note_write"}}
	})
	f.handler.SetRecipeRunner(FirstTaskRecipeSoftwareAssessment, f.runner)
	return f
}

func recipeTask() Task {
	return Task{
		ID: "assess", WorkspaceID: "ws-1", To: "Robin", Description: "Assess this project and suggest a first task",
		Context: map[string]any{TaskContextFirstTaskRecipe: FirstTaskRecipeSoftwareAssessment, TaskContextFirstTaskRecipeVersion: 1},
	}
}

var privileged = types.Settings{Provider: "openai", Model: "gpt-test", SystemPrompt: "AGENT PERSONA: you may do anything."}

// A recipe run is exactly one call. It carries the recipe's two messages and
// nothing of the agent's: no tool definitions, no tool servers, no workspace,
// no persona. The same agent's ordinary task gets its tools, which is what
// shows the difference is the recipe's and not an empty agent.
func TestRecipeRunIsOneIsolatedCallWithNothingOfTheAgents(t *testing.T) {
	f := newRecipeRunFixture(t, privileged)
	result, err := f.handler.ExecuteTask(context.Background(), "Robin", recipeTask())
	if err != nil {
		t.Fatalf("recipe run: %v", err)
	}
	if result != "## What it is\nA service.\n[host note state]" {
		t.Fatalf("result = %q", result)
	}
	if len(f.provider.requests) != 1 || f.runner.briefs != 1 || len(f.runner.finished) != 1 {
		t.Fatalf("calls: provider %d, brief %d, finish %d", len(f.provider.requests), f.runner.briefs, len(f.runner.finished))
	}
	req := f.provider.requests[0]
	if !req.Isolated || len(req.Tools) != 0 || len(req.MCPServers) != 0 || req.WorkspaceID != "" || req.WorkspaceDir != "" || req.ExecutionScope != nil {
		t.Fatalf("the call was not a bare isolated one: %+v", req)
	}
	if err := llm.CheckIsolatedRequest(req); err != nil {
		t.Fatalf("the request would be refused by a provider: %v", err)
	}
	// The recipe's rules travel as the system prompt, which every provider
	// sends; a system-role message is dropped by some.
	if req.Model != "gpt-test" || req.SystemPrompt != "HOST RULES" || len(req.Messages) != 1 ||
		req.Messages[0].Role != llm.RoleUser || req.Messages[0].Content != "HOST EVIDENCE" {
		t.Fatalf("system prompt = %q, messages = %+v", req.SystemPrompt, req.Messages)
	}
	if all := req.SystemPrompt + req.Messages[0].Content; strings.Contains(all, "AGENT PERSONA") || strings.Contains(all, "web_search") {
		t.Fatal("the agent's prompt or tools reached a recipe run")
	}
	if f.registry.starts != 0 || f.registry.lists != 0 || f.factory != 0 || f.search.calls != 0 {
		t.Fatalf("the agent's tools were resolved: starts=%d lists=%d workspace=%d search=%d", f.registry.starts, f.registry.lists, f.factory, f.search.calls)
	}
	if len(f.runner.windows) != 1 {
		t.Fatalf("context windows asked = %v", f.runner.windows)
	}

	// The very same agent, on an ordinary task, is given its tools.
	ordinary := recipeTask()
	ordinary.Context = nil
	f.provider.responses = append(f.provider.responses, llm.ChatResponse{Content: "done"})
	if _, err := f.handler.ExecuteTask(context.Background(), "Robin", ordinary); err != nil {
		t.Fatalf("ordinary run: %v", err)
	}
	last := f.provider.requests[len(f.provider.requests)-1]
	if last.Isolated || len(last.Tools) == 0 {
		t.Fatalf("an ordinary task of the same agent had no tools: isolated=%v tools=%d", last.Isolated, len(last.Tools))
	}
}

// What the run bridge prepares before a run is the recipe's too: preparing a
// recipe task does not resolve the agent's tools, which would start its tool
// servers before the recipe ever ran.
func TestPrepareTaskContextForARecipeTaskTouchesNoneOfTheAgentsTools(t *testing.T) {
	f := newRecipeRunFixture(t, privileged)
	prepared, err := f.handler.PrepareTaskContext(context.Background(), "Robin", recipeTask())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Strategy != "host_recipe" || len(prepared.AvailableTools) != 0 || len(prepared.Items) != 1 || prepared.Items[0].Kind != "host_recipe" {
		t.Fatalf("prepared = %+v", prepared)
	}
	if f.registry.starts != 0 || f.registry.lists != 0 || f.factory != 0 {
		t.Fatalf("preparing a recipe task resolved tools: starts=%d lists=%d workspace=%d", f.registry.starts, f.registry.lists, f.factory)
	}
	// An ordinary task's preparation does list the agent's tools.
	ordinary := recipeTask()
	ordinary.Context = nil
	other, err := f.handler.PrepareTaskContext(context.Background(), "Robin", ordinary)
	if err != nil || len(other.AvailableTools) == 0 {
		t.Fatalf("ordinary preparation = %+v, %v", other, err)
	}
}

// A model that asks for a tool is not obliged: nothing is dispatched, the
// answer is discarded, and the run fails with that as its reason.
func TestRecipeRunRefusesAToolCallInTheReply(t *testing.T) {
	f := newRecipeRunFixture(t, privileged)
	f.provider.responses = []llm.ChatResponse{{
		Content:   "Let me look around first.",
		ToolCalls: []llm.ToolCall{{ID: "1", Name: "web_search", Arguments: `{"query":"x"}`}},
	}}
	result, err := f.handler.ExecuteTask(context.Background(), "Robin", recipeTask())
	if failure, ok := AsRecipeRunError(err); !ok || failure.Code != RecipeToolCallRefused || result != "" {
		t.Fatalf("a tool call in the reply => %q, %v", result, err)
	}
	if f.search.calls != 0 || len(f.provider.requests) != 1 || len(f.runner.finished) != 0 {
		t.Fatalf("the tool ran, the model was asked again, or the reply was kept: search=%d requests=%d finished=%d",
			f.search.calls, len(f.provider.requests), len(f.runner.finished))
	}
	// The tool loop itself refuses a recipe task, whatever reaches it.
	refused := f.handler.executeToolCall(context.Background(), &resolvedTaskAgent{Agent: &agent.Agent{}}, "Robin", recipeTask(),
		llm.ToolCall{Name: "web_search", Arguments: `{}`})
	if refused.Error == nil || f.search.calls != 0 {
		t.Fatalf("the tool loop dispatched a tool for a recipe task: %+v", refused)
	}
}

// Everything that can be known before the run is checked before anything is
// read or sent, and the readiness answer is the same check.
func TestRecipeRunRefusesBeforeAnythingIsReadOrSent(t *testing.T) {
	for name, tc := range map[string]struct {
		settings types.Settings
		arrange  func(*recipeRunFixture)
		assignee string
		want     string
	}{
		"the assistant has no model": {settings: types.Settings{Provider: "openai"}, want: RecipeNoModel},
		"no provider for its model is set up": {settings: types.Settings{Provider: "elsewhere", Model: "their-model"},
			arrange: func(f *recipeRunFixture) { f.handler.llmFactory = llm.NewFactory() }, want: RecipeModelUnavailable},
		// The assistant's own provider is not set up here, and another provider
		// that could answer for a model of that name is. An ordinary task would
		// be moved to it; a recipe run is not.
		"its provider is not set up, though another could take the model": {
			settings: types.Settings{Provider: "codex", Model: "gpt-test"}, want: RecipeModelUnavailable},
		"its provider cannot isolate a call":    {settings: privileged, arrange: func(f *recipeRunFixture) { f.provider.isolates = false }, want: RecipeModelUnsupported},
		"the assistant is gone":                 {settings: privileged, assignee: "Nobody", want: RecipeAssistantMissing},
		"the recipe is not one this server has": {settings: privileged, arrange: func(f *recipeRunFixture) { f.handler.recipes = nil }, want: RecipeUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRecipeRunFixture(t, tc.settings)
			if tc.arrange != nil {
				tc.arrange(f)
			}
			task := recipeTask()
			if tc.assignee != "" {
				task.To = tc.assignee
			}
			if got := f.handler.RecipeReadiness(task); got != tc.want {
				t.Errorf("readiness = %q, want %q", got, tc.want)
			}
			result, err := f.handler.ExecuteTask(context.Background(), task.To, task)
			failure, ok := AsRecipeRunError(err)
			if !ok || failure.Code != tc.want || result != "" {
				t.Fatalf("run => %q, %v; want %s", result, err, tc.want)
			}
			if !strings.Contains(failure.Sentence, "nothing was sent to a model") && !strings.Contains(failure.Sentence, "Nothing was sent to it") {
				t.Errorf("the reason does not say nothing was sent: %q", failure.Sentence)
			}
			if len(f.provider.requests) != 0 || f.runner.briefs != 0 {
				t.Fatalf("something was read or sent: requests=%d briefs=%d", len(f.provider.requests), f.runner.briefs)
			}
		})
	}

	// Ready is the recipe's own cheap check, and readiness reports it.
	f := newRecipeRunFixture(t, privileged)
	if got := f.handler.RecipeReadiness(recipeTask()); got != "" {
		t.Fatalf("a ready task reports %q", got)
	}
	f.runner.ready = &TaskBlockedError{ReasonCode: "project_folder_unavailable"}
	if got := f.handler.RecipeReadiness(recipeTask()); got != "project_folder_unavailable" {
		t.Fatalf("readiness = %q", got)
	}
	if got := f.handler.RecipeReadiness(Task{ID: "plain", To: "Robin"}); got != "" {
		t.Fatalf("an ordinary task has a recipe readiness: %q", got)
	}
}

// What the recipe finds when it comes to read is the run's answer, and a
// provider that discovers it cannot isolate the call after all is not retried
// some other way. There is no fallback provider and no second call.
func TestRecipeRunHasNoSecondCallAndNoFallback(t *testing.T) {
	gone := &TaskBlockedError{ReasonCode: "project_folder_unavailable", Reason: "The folder is missing."}
	f := newRecipeRunFixture(t, privileged)
	f.runner.briefErr = gone
	_, err := f.handler.ExecuteTask(context.Background(), "Robin", recipeTask())
	failure, ok := AsRecipeRunError(err)
	if !ok || failure.Code != "project_folder_unavailable" || failure.Sentence != "The folder is missing." || len(f.provider.requests) != 0 {
		t.Fatalf("a brief that could not be made => %v with %d requests", err, len(f.provider.requests))
	}

	// A fallback is configured for the agent, and a second provider exists.
	settings := privileged
	settings.FallbackProvider, settings.FallbackModel = "backup", "backup-model"
	f = newRecipeRunFixture(t, settings)
	backup := &isolatingProvider{isolates: true, scriptedProviderStub: scriptedProviderStub{name: "backup", responses: []llm.ChatResponse{{Content: "from the fallback"}}}}
	f.handler.llmFactory.Register("backup", backup)

	f.provider.chatErr = llm.ErrIsolationUnsupported
	_, err = f.handler.ExecuteTask(context.Background(), "Robin", recipeTask())
	if failure, ok := AsRecipeRunError(err); !ok || failure.Code != RecipeModelUnsupported {
		t.Fatalf("a provider that refused isolation => %v", err)
	}
	f.provider.chatErr = errors.New("connection refused: the local server is offline")
	if _, err = f.handler.ExecuteTask(context.Background(), "Robin", recipeTask()); err == nil {
		t.Fatal("a failed call was reported as a result")
	}
	if len(f.provider.requests) != 2 || len(backup.requests) != 0 {
		t.Fatalf("calls: primary %d (want one per run), fallback %d (want none)", len(f.provider.requests), len(backup.requests))
	}
	for _, req := range f.provider.requests {
		if !req.Isolated || len(req.Tools) != 0 {
			t.Fatalf("a failed run's call was not isolated: %+v", req)
		}
	}
}

// Whatever a provider says when it fails, a caller is given one plain sentence
// and a code. A provider's error can carry a path, a command's output or an
// account detail, and every executor stores what it is handed on the task.
func TestRecipeRunFailureNeverCarriesTheProvidersOwnWords(t *testing.T) {
	raw := "exit status 1: /Users/someone/.codex/auth.json: token sk-live-ABC123 rejected\nstderr: Traceback …"
	for name, tc := range map[string]struct {
		err  error
		ctx  func() (context.Context, context.CancelFunc)
		code string
	}{
		"an unclassified failure": {err: errors.New(raw), code: RecipeRunFailed},
		"a wrapped one":           {err: fmt.Errorf("codex: %w", errors.New(raw)), code: RecipeRunFailed},
		"out of time": {err: fmt.Errorf("%s: %w", raw, context.DeadlineExceeded), code: RecipeTimedOut,
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRecipeRunFixture(t, privileged)
			f.provider.chatErr = tc.err
			ctx, cancel := context.Background(), context.CancelFunc(func() {})
			if tc.ctx != nil {
				ctx, cancel = tc.ctx()
			}
			defer cancel()
			result, err := f.handler.ExecuteTask(ctx, "Robin", recipeTask())
			failure, ok := AsRecipeRunError(err)
			if !ok || failure.Code != tc.code || result != "" {
				t.Fatalf("run => %q, %v; want a %s failure", result, err, tc.code)
			}
			for _, leaked := range []string{"/Users/", "sk-live", "auth.json", "stderr", "exit status", "Traceback"} {
				if strings.Contains(err.Error(), leaked) {
					t.Errorf("the failure carries %q: %q", leaked, err.Error())
				}
			}
			if !strings.HasSuffix(err.Error(), "Nothing in the project was run or changed.") {
				t.Errorf("the sentence does not say nothing was changed: %q", err.Error())
			}
		})
	}

	// A reply with nothing in it is a failure with its own sentence, not a
	// finished report.
	f := newRecipeRunFixture(t, privileged)
	f.runner.empty = true
	if _, err := f.handler.ExecuteTask(context.Background(), "Robin", recipeTask()); err == nil {
		t.Fatal("an empty report was returned as a result")
	} else if failure, ok := AsRecipeRunError(err); !ok || failure.Code != RecipeEmptyResult {
		t.Fatalf("an empty report => %v", err)
	}

	// A run the user cancelled stays a cancellation for the executor to handle.
	f = newRecipeRunFixture(t, privileged)
	f.provider.chatErr = context.Canceled
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.handler.ExecuteTask(cancelled, "Robin", recipeTask()); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled run => %v", err)
	}
}

// One task is run once at a time, whichever executor asks. The second start is
// refused here, before anything is read or sent, so two clicks, two tabs or two
// executors never spend two calls.
func TestRecipeRunOfOneTaskIsNeverStartedTwiceAtOnce(t *testing.T) {
	f := newRecipeRunFixture(t, privileged)
	entered, release := make(chan struct{}), make(chan struct{})
	f.provider.hold = func() {
		close(entered)
		<-release
	}
	first := make(chan error, 1)
	go func() {
		_, err := f.handler.ExecuteTask(context.Background(), "Robin", recipeTask())
		first <- err
	}()
	<-entered

	result, err := f.handler.ExecuteTask(context.Background(), "Robin", recipeTask())
	if failure, ok := AsRecipeRunError(err); !ok || failure.Code != RecipeAlreadyRunning || result != "" {
		t.Fatalf("a second start while the first runs => %q, %v", result, err)
	}
	// Another task is not held up by this one.
	other := recipeTask()
	other.ID = "another"
	f.provider.hold = nil
	f.provider.responses = append(f.provider.responses, llm.ChatResponse{Content: "## What it is\nAnother."})
	if _, err := f.handler.ExecuteTask(context.Background(), "Robin", other); err != nil {
		t.Fatalf("a different task: %v", err)
	}

	close(release)
	if err := <-first; err != nil {
		t.Fatalf("the first run: %v", err)
	}
	if f.runner.briefs != 2 {
		t.Fatalf("the project was read %d times; want once per run that started", f.runner.briefs)
	}
	// Finished, the task can be run again.
	f.provider.responses = append(f.provider.responses, llm.ChatResponse{Content: "## What it is\nAgain."})
	if _, err := f.handler.ExecuteTask(context.Background(), "Robin", recipeTask()); err != nil {
		t.Fatalf("a run after the first finished: %v", err)
	}
}
