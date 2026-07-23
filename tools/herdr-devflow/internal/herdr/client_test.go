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
