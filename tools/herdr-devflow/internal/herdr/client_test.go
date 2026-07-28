package herdr

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

type fakeRunner struct {
	mu        sync.Mutex
	calls     []Command
	responses map[string]CommandResult
	errors    map[string]error
}

func (f *fakeRunner) Run(_ context.Context, command Command) (CommandResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, command)
	key := strings.Join(command.Args, " ")
	return f.responses[key], f.errors[key]
}

func TestClientUsesStructuredJSONAndMapsAPIErrors(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{responses: map[string]CommandResult{
		"--version":         {Stdout: []byte("herdr 0.7.5\n")},
		"api schema --json": {Stdout: fixture(t, "schema-0.7.5.json")},
		"agent get builder": {Stdout: fixture(t, "agent-pane-not-found-0.7.5.json")},
	}, errors: map[string]error{
		"agent get builder": errors.New("exit status 1"),
	}}
	client := New("fake-herdr", "", runner)
	version, err := client.Version(context.Background())
	if err != nil || version.Raw != "0.7.5" {
		t.Fatalf("Version() = %#v, %v", version, err)
	}
	schema, err := client.Schema(context.Background())
	if err != nil || !schema.Supports("plugin.link") || !schema.Supports("agent.view.set") {
		t.Fatalf("Schema() = %#v, %v", schema, err)
	}
	if missing := MissingRequiredSchemaMethods(schema); len(missing) != 0 {
		t.Fatalf("recorded 0.7.5 schema is missing bridge methods: %v", missing)
	}
	_, err = client.AgentGet(context.Background(), "builder")
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrAgentMissing {
		t.Fatalf("AgentGet() error = %#v, want mapped missing-agent error", err)
	}
}

func TestClientReadsStructuredAPIErrorsFromStderr(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{responses: map[string]CommandResult{
		"agent get missing": {Stderr: []byte(`{"error":{"code":"agent_not_found","message":"agent target missing not found"}}`)},
	}, errors: map[string]error{
		"agent get missing": errors.New("exit status 1"),
	}}
	_, err := New("fake-herdr", "", runner).AgentGetInfo(context.Background(), "missing")
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrAgentMissing {
		t.Fatalf("stderr API error = %#v", err)
	}
}

func TestRecorded075ResponsesDecodeStableBridgeIdentities(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{responses: map[string]CommandResult{
		"agent list": {Stdout: fixture(t, "agent-list-0.7.5.json")},
		"worktree open --cwd /tmp/ori-source --path /tmp/ori-feature --no-focus --json": {Stdout: fixture(t, "worktree-open-0.7.5.json")},
		"workspace close w1": {Stdout: fixture(t, "workspace-close-0.7.5.json")},
	}}
	client := New("fake-herdr", "", runner)
	agents, err := client.AgentListInfo(context.Background())
	if err != nil || len(agents) != 1 {
		t.Fatalf("AgentListInfo() = %#v, %v", agents, err)
	}
	if agents[0].Name != "ori-repo-feature-builder" || agents[0].WorkspaceID != "w1" || agents[0].AgentSession == nil || agents[0].AgentSession.Value != "recorded-session-123" {
		t.Fatalf("recorded agent identity = %#v", agents[0])
	}
	opened, err := client.OpenExistingWorktree(context.Background(), "/tmp/ori-source", "/tmp/ori-feature")
	if err != nil || !opened.AlreadyOpen || opened.RootPane.PaneID != "w1:p1" || opened.Worktree.Branch != "feature/ori-feature" {
		t.Fatalf("OpenExistingWorktree() = %#v, %v", opened, err)
	}
	closed, err := client.WorkspaceClose(context.Background(), "w1")
	if err != nil || !strings.Contains(string(closed), `"workspace_closed"`) {
		t.Fatalf("WorkspaceClose() = %s, %v", closed, err)
	}
}

func TestPluginManifestPinsHerdr075Minimum(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile(filepath.Join("..", "..", "herdr-plugin.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `min_herdr_version = "0.7.5"`) {
		t.Fatalf("plugin manifest does not pin the recorded 0.7.5 contract: %q", contents)
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestCallSocketUsesJSONLines(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket fixture")
	}
	t.Parallel()
	socket := filepath.Join(t.TempDir(), "herdr.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close(); _ = os.Remove(socket) })
	requests := make(chan string, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		line, _ := bufio.NewReader(connection).ReadString('\n')
		requests <- line
		_, _ = connection.Write([]byte(`{"id":"ori-devflow-1","result":{"type":"pong"}}` + "\n"))
	}()
	client := New("unused", socket, nil)
	response, err := client.CallSocket(context.Background(), "ping", map[string]any{})
	if err != nil || string(response) != `{"type":"pong"}` {
		t.Fatalf("CallSocket() = %s, %v", response, err)
	}
	request := <-requests
	if !strings.Contains(request, `"method":"ping"`) || !strings.HasSuffix(request, "\n") {
		t.Fatalf("socket request = %q", request)
	}
}

func TestAgentListAndWorkspaceClosePreferTheStructuredSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket fixture")
	}
	t.Parallel()
	runner := &fakeRunner{}
	for _, test := range []struct {
		name     string
		response string
		call     func(*Client) error
		method   string
	}{
		{
			name:     "agent list",
			response: `{"id":"ori-devflow-1","result":{"type":"agent_list","agents":[{"name":"ori-bridge-builder","workspace_id":"w1","pane_id":"w1:p1","terminal_id":"term-1"}]}}` + "\n",
			call: func(client *Client) error {
				agents, err := client.AgentListInfo(context.Background())
				if err != nil || len(agents) != 1 || agents[0].Name != "ori-bridge-builder" {
					return fmt.Errorf("AgentListInfo() = %#v, %v", agents, err)
				}
				return nil
			},
			method: "agent.list",
		},
		{
			name:     "workspace close",
			response: `{"id":"ori-devflow-1","result":{"type":"workspace_closed","workspace_id":"w1"}}` + "\n",
			call: func(client *Client) error {
				_, err := client.WorkspaceClose(context.Background(), "w1")
				return err
			},
			method: "workspace.close",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			pathFile, err := os.CreateTemp("", "herdr-socket-")
			if err != nil {
				t.Fatal(err)
			}
			socket := pathFile.Name()
			if err := pathFile.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(socket); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close(); _ = os.Remove(socket) })
			requests := make(chan string, 1)
			go func() {
				connection, acceptErr := listener.Accept()
				if acceptErr != nil {
					return
				}
				defer connection.Close()
				line, _ := bufio.NewReader(connection).ReadString('\n')
				requests <- line
				_, _ = connection.Write([]byte(test.response))
			}()
			if err := test.call(New("unused", socket, runner)); err != nil {
				t.Fatal(err)
			}
			if request := <-requests; !strings.Contains(request, `"method":"`+test.method+`"`) {
				t.Fatalf("socket request = %q, want %s", request, test.method)
			}
		})
	}
	if len(runner.calls) != 0 {
		t.Fatalf("socket-enabled methods unexpectedly ran the CLI: %#v", runner.calls)
	}
}

func TestIntegrationStatusIsOpaqueAndRedacted(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{responses: map[string]CommandResult{
		"integration status": {Stdout: []byte("claude: current (v7)\n\x1b[31mcodex: current (v6)\x1b[0m\n")},
	}}
	status, err := New("fake-herdr", "", runner).IntegrationStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(status, '\x1b') || !strings.Contains(status, "claude: current") || !strings.Contains(status, "codex: current") {
		t.Fatalf("IntegrationStatus() = %q", status)
	}
}

func TestOpenExistingWorktreeUsesTheSourceCheckoutAndOnlyTheDocumentedOpenOperation(t *testing.T) {
	t.Parallel()
	const source = "/tmp/ori-source"
	const path = "/tmp/ori-feature"
	runner := &fakeRunner{responses: map[string]CommandResult{
		"worktree open --cwd /tmp/ori-source --path /tmp/ori-feature --no-focus --json": {
			Stdout: []byte(`{"result":{"type":"worktree_opened","already_open":true,"workspace":{"workspace_id":"w1","cwd":"/tmp/ori-feature"},"tab":{"tab_id":"w1:t1","workspace_id":"w1"},"root_pane":{"pane_id":"w1:p1","terminal_id":"term-1","workspace_id":"w1","tab_id":"w1:t1","cwd":"/tmp/ori-feature","foreground_cwd":"/tmp/ori-feature"},"worktree":{"path":"/tmp/ori-feature","branch":"feature/bridge"}}}`),
		},
	}}
	opened, err := New("fake-herdr", "", runner).OpenExistingWorktree(context.Background(), source, path)
	if err != nil || !opened.AlreadyOpen || opened.RootPane.PaneID != "w1:p1" || opened.Worktree.Path != path {
		t.Fatalf("OpenExistingWorktree() = %#v, %v", opened, err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("Herdr calls = %#v, want exactly one open", runner.calls)
	}
	if got := strings.Join(runner.calls[0].Args, " "); got != "worktree open --cwd /tmp/ori-source --path /tmp/ori-feature --no-focus --json" {
		t.Fatalf("worktree open arguments = %q", got)
	}
	for _, argument := range runner.calls[0].Args {
		if argument == "create" || argument == "remove" {
			t.Fatalf("forbidden Herdr worktree mutation in %#v", runner.calls[0])
		}
	}
}

func TestAgentPromptUsesImmediateStructuredAcknowledgementAndRedactsFailures(t *testing.T) {
	t.Parallel()
	const target = "ori-repo-feature-builder"
	const secretPrompt = "OPENAI_API_KEY=sk-not-a-real-secret"
	runner := &fakeRunner{responses: map[string]CommandResult{
		"agent prompt " + target + " " + secretPrompt: {Stdout: []byte(`{"result":{"type":"agent_prompted","agent":{"name":"ori-repo-feature-builder","workspace_id":"w1","pane_id":"w1:p1","terminal_id":"term-1"}}}`)},
	}}
	ack, err := New("fake-herdr", "", runner).AgentPromptInfo(context.Background(), target, secretPrompt, time.Second)
	if err != nil || ack.Name != target {
		t.Fatalf("AgentPromptInfo() = %#v, %v", ack, err)
	}
	if got := strings.Join(runner.calls[0].Args, " "); strings.Contains(got, "--wait") || strings.Contains(got, "--timeout") {
		t.Fatalf("prompt command unexpectedly waits for a later state transition: %q", got)
	}

	failing := &fakeRunner{responses: map[string]CommandResult{
		"agent prompt " + target + " " + secretPrompt: {Stderr: []byte(`{"error":{"code":"agent_not_found","message":"` + secretPrompt + `"}}`)},
	}, errors: map[string]error{
		"agent prompt " + target + " " + secretPrompt: errors.New("exit status 1"),
	}}
	_, err = New("fake-herdr", "", failing).AgentPromptInfo(context.Background(), target, secretPrompt, time.Second)
	if err == nil || strings.Contains(err.Error(), secretPrompt) || strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("prompt error leaked sensitive body: %v", err)
	}
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Stage != "agent prompt" {
		t.Fatalf("prompt error = %#v", err)
	}
}

func TestMetadataCommandUsesStableTokenOrder(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{responses: map[string]CommandResult{
		"workspace report-metadata w1 --source ori.devflow --token branch=feature/bridge --token feature=bridge --token repository=repo-1": {Stdout: []byte(`{"result":{"type":"workspace_metadata"}}`)},
	}}
	_, err := New("fake-herdr", "", runner).ReportWorkspaceMetadata(context.Background(), "w1", "ori.devflow", map[string]string{
		"repository": "repo-1",
		"feature":    "bridge",
		"branch":     "feature/bridge",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(runner.calls[0].Args, " "); got != "workspace report-metadata w1 --source ori.devflow --token branch=feature/bridge --token feature=bridge --token repository=repo-1" {
		t.Fatalf("metadata command = %q", got)
	}
}

func TestCallSocketReportsAnActionableUnavailableError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket fixture")
	}
	t.Parallel()
	_, err := New("unused", filepath.Join(t.TempDir(), "missing.sock"), nil).Ping(context.Background())
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrHerdrUnavailable || stage.Recovery == "" {
		t.Fatalf("Ping() error = %#v, want actionable unavailable error", err)
	}
}

func TestPaneSplitAndRenameDecodeStructuredAgentIdentity(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{responses: map[string]CommandResult{
		"pane split w1:p1 --direction right --cwd /tmp/bridge --no-focus": {Stdout: []byte(`{"result":{"pane":{"pane_id":"w1:p2","terminal_id":"term-2","workspace_id":"w1","tab_id":"w1:t1","cwd":"/tmp/bridge","foreground_cwd":"/tmp/bridge"}}}`)},
		"agent rename old-name ori-bridge-reviewer":                       {Stdout: []byte(`{"result":{"agent":{"name":"ori-bridge-reviewer","agent":"claude","pane_id":"w1:p2","terminal_id":"term-2","workspace_id":"w1","tab_id":"w1:t1"}}}`)},
	}}
	client := New("fake-herdr", "", runner)
	pane, err := client.PaneSplitInfo(context.Background(), "w1:p1", "right", "/tmp/bridge")
	if err != nil || pane.PaneID != "w1:p2" || pane.TerminalID != "term-2" {
		t.Fatalf("PaneSplitInfo() = %#v, %v", pane, err)
	}
	agent, err := client.AgentRenameInfo(context.Background(), "old-name", "ori-bridge-reviewer")
	if err != nil || agent.Name != "ori-bridge-reviewer" || agent.PaneID != "w1:p2" {
		t.Fatalf("AgentRenameInfo() = %#v, %v", agent, err)
	}
}

func TestAgentReadReturnsTerminalTextWithoutJSONParsing(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{responses: map[string]CommandResult{
		"agent read ori-bridge-builder --source recent-unwrapped --lines 120": {Stdout: []byte("working on 3.2\n$ ")},
	}}
	text, err := New("fake-herdr", "", runner).AgentReadText(context.Background(), "ori-bridge-builder", 120)
	if err != nil || text != "working on 3.2\n$ " {
		t.Fatalf("AgentReadText() = %q, %v", text, err)
	}
}

func TestWorkspaceListInfoDecodesWorktreeBinding(t *testing.T) {
	payload := `{"result":{"type":"workspace_list","workspaces":[
		{"workspace_id":"wE","label":"downloads-janitor","worktree":{
			"checkout_path":"/repo/worktrees/downloads-janitor",
			"is_linked_worktree":true,
			"repo_key":"/repo/ori-agent/.git",
			"repo_name":"ori-agent",
			"repo_root":"/repo/ori-agent"}},
		{"workspace_id":"wF","label":"ori"}
	]}}`
	runner := &fakeRunner{responses: map[string]CommandResult{
		"workspace list": {Stdout: []byte(payload)},
	}, errors: map[string]error{}}
	client := New("fake-herdr", "", runner)

	workspaces, err := client.WorkspaceListInfo(context.Background())
	if err != nil {
		t.Fatalf("WorkspaceListInfo: %v", err)
	}
	if len(workspaces) != 2 {
		t.Fatalf("decoded %d workspaces, want 2", len(workspaces))
	}

	bound := workspaces[0]
	if bound.Worktree == nil {
		t.Fatal("a workspace with a worktree binding decoded as unbound")
	}
	if bound.Worktree.CheckoutPath != "/repo/worktrees/downloads-janitor" {
		t.Fatalf("checkout path = %q", bound.Worktree.CheckoutPath)
	}
	if !bound.Worktree.IsLinkedWorktree || bound.Worktree.RepoRoot != "/repo/ori-agent" {
		t.Fatalf("worktree binding = %+v", bound.Worktree)
	}

	// A workspace created by hand carries no binding at all. That absence is
	// the signal, not an error — it is why pane cwd is the primary evidence.
	if workspaces[1].Worktree != nil {
		t.Fatalf("an unbound workspace decoded a binding: %+v", workspaces[1].Worktree)
	}
}

func TestTabCallsDecodeRecordedResponsesAndValidateIdentity(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{responses: map[string]CommandResult{
		"tab create --workspace w1 --cwd /tmp/ori-feature --label ori-feature --no-focus": {Stdout: fixture(t, "tab-create-0.7.5.json")},
		"tab list --workspace w1": {Stdout: fixture(t, "tab-list-0.7.5.json")},
		"tab get w1:t2":           {Stdout: []byte(`{"result":{"type":"tab_info","tab":{"tab_id":"w1:t2","workspace_id":"w1","label":"ori-feature","number":2,"pane_count":1,"agent_status":"working"}}}`)},
		"tab close w1:t2":         {Stdout: []byte(`{"result":{"type":"ok"}}`)},
	}}
	client := New("fake-herdr", "", runner)

	created, err := client.TabCreateInfo(context.Background(), "w1", "/tmp/ori-feature", "ori-feature")
	if err != nil {
		t.Fatalf("TabCreateInfo() error = %v", err)
	}
	if created.Tab.TabID != "w1:t2" || created.Tab.Label != "ori-feature" {
		t.Fatalf("created tab = %#v", created.Tab)
	}
	// The root pane is the only pane identity any tab call returns, so it must
	// survive decoding complete enough for validateRootPane to accept it.
	if created.RootPane.PaneID != "w1:p2" || created.RootPane.TerminalID != "term-recorded-2" || created.RootPane.Cwd != "/tmp/ori-feature" {
		t.Fatalf("created root pane = %#v", created.RootPane)
	}

	tabs, err := client.TabListInfo(context.Background(), "w1")
	if err != nil || len(tabs) != 2 || tabs[1].TabID != "w1:t2" {
		t.Fatalf("TabListInfo() = %#v, %v", tabs, err)
	}

	tab, err := client.TabGetInfo(context.Background(), "w1:t2")
	if err != nil || tab.TabID != "w1:t2" || tab.AgentStatus != model.AgentWorking {
		t.Fatalf("TabGetInfo() = %#v, %v", tab, err)
	}

	closed, err := client.TabClose(context.Background(), "w1:t2")
	if err != nil || !strings.Contains(string(closed), `"ok"`) {
		t.Fatalf("TabClose() = %s, %v", closed, err)
	}
}

func TestTabCreateRejectsMisplacedOrIncompleteResponses(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"no root pane":       `{"result":{"type":"tab_created","tab":{"tab_id":"w1:t2","workspace_id":"w1"}}}`,
		"wrong workspace":    `{"result":{"type":"tab_created","tab":{"tab_id":"w9:t1","workspace_id":"w9"},"root_pane":{"pane_id":"w9:p1","tab_id":"w9:t1"}}}`,
		"pane from othertab": `{"result":{"type":"tab_created","tab":{"tab_id":"w1:t2","workspace_id":"w1"},"root_pane":{"pane_id":"w1:p1","tab_id":"w1:t1"}}}`,
		"unexpected type":    `{"result":{"type":"workspace_created","tab":{"tab_id":"w1:t2","workspace_id":"w1"},"root_pane":{"pane_id":"w1:p2","tab_id":"w1:t2"}}}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{responses: map[string]CommandResult{
				"tab create --workspace w1 --cwd /tmp/ori-feature --label ori-feature --no-focus": {Stdout: []byte(payload)},
			}}
			_, err := New("fake-herdr", "", runner).TabCreateInfo(context.Background(), "w1", "/tmp/ori-feature", "ori-feature")
			var stage *model.StageError
			if !errors.As(err, &stage) || stage.Code != model.ErrHerdrUnavailable {
				t.Fatalf("TabCreateInfo() error = %#v, want a rejected placement", err)
			}
		})
	}
}

func TestFocusedWorkspaceResolvesTheSessionTargetOrReportsNoFocus(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{responses: map[string]CommandResult{
		"workspace list": {Stdout: fixture(t, "workspace-list-0.7.5.json")},
	}}
	client := New("fake-herdr", "", runner)

	workspaces, err := client.WorkspaceListInfo(context.Background())
	if err != nil || len(workspaces) != 2 {
		t.Fatalf("WorkspaceListInfo() = %#v, %v", workspaces, err)
	}
	// The live API has always returned these; the struct simply dropped them.
	if !workspaces[0].Focused || workspaces[0].ActiveTabID != "w1:t1" {
		t.Fatalf("focus fields did not decode: %#v", workspaces[0])
	}
	if workspaces[1].Focused {
		t.Fatalf("unfocused workspace decoded as focused: %#v", workspaces[1])
	}

	focused, err := client.FocusedWorkspace(context.Background())
	if err != nil || focused.WorkspaceID != "w1" {
		t.Fatalf("FocusedWorkspace() = %#v, %v", focused, err)
	}

	// No focused workspace is a distinct, recoverable condition: the handoff
	// degrades on it rather than falling back to minting a workspace.
	unfocused := &fakeRunner{responses: map[string]CommandResult{
		"workspace list": {Stdout: []byte(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","focused":false}]}}`)},
	}}
	_, err = New("fake-herdr", "", unfocused).FocusedWorkspace(context.Background())
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrNoFocusedWorkspace {
		t.Fatalf("FocusedWorkspace() with no focus = %#v, want ErrNoFocusedWorkspace", err)
	}
}

// Herdr not being installed and HERDR_BIN_PATH pointing somewhere stale are the
// same problem to the user, but they surface as two different exec errors: a
// bare name misses in LookPath, an absolute path misses at exec with ENOENT.
// Both must read as "install or fix the path", not "check the Herdr server".
func TestMissingHerdrBinaryIsClassifiedForBothLookupAndPathMisses(t *testing.T) {
	t.Parallel()
	cases := map[string]error{
		"not on PATH":         exec.ErrNotFound,
		"configured but gone": &fs.PathError{Op: "fork/exec", Path: "/nowhere/herdr", Err: syscall.ENOENT},
	}
	for name, cause := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{errors: map[string]error{"--version": cause}}
			_, err := New("/nowhere/herdr", "", runner).Version(context.Background())
			var stage *model.StageError
			if !errors.As(err, &stage) || stage.Code != model.ErrHerdrMissing {
				t.Fatalf("Version() error = %#v, want ErrHerdrMissing", err)
			}
			if stage.Recovery == "" {
				t.Fatal("a missing-binary failure must carry a recovery command")
			}
		})
	}
}
