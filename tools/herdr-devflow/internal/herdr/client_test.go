package herdr

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

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
	_, err = client.AgentGet(context.Background(), "builder")
	var stage *model.StageError
	if !errors.As(err, &stage) || stage.Code != model.ErrAgentMissing {
		t.Fatalf("AgentGet() error = %#v, want mapped missing-agent error", err)
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

func TestOpenExistingWorktreeUsesOnlyTheDocumentedOpenOperation(t *testing.T) {
	t.Parallel()
	const path = "/tmp/ori-feature"
	runner := &fakeRunner{responses: map[string]CommandResult{
		"worktree open --path /tmp/ori-feature --no-focus --json": {
			Stdout: []byte(`{"result":{"type":"worktree_opened","already_open":true,"workspace":{"workspace_id":"w1","cwd":"/tmp/ori-feature"},"tab":{"tab_id":"w1:t1","workspace_id":"w1"},"root_pane":{"pane_id":"w1:p1","terminal_id":"term-1","workspace_id":"w1","tab_id":"w1:t1","cwd":"/tmp/ori-feature","foreground_cwd":"/tmp/ori-feature"},"worktree":{"path":"/tmp/ori-feature","branch":"feature/bridge"}}}`),
		},
	}}
	opened, err := New("fake-herdr", "", runner).OpenExistingWorktree(context.Background(), path)
	if err != nil || !opened.AlreadyOpen || opened.RootPane.PaneID != "w1:p1" || opened.Worktree.Path != path {
		t.Fatalf("OpenExistingWorktree() = %#v, %v", opened, err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("Herdr calls = %#v, want exactly one open", runner.calls)
	}
	for _, argument := range runner.calls[0].Args {
		if argument == "create" || argument == "remove" {
			t.Fatalf("forbidden Herdr worktree mutation in %#v", runner.calls[0])
		}
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
