package downloadsjanitor

import (
	"context"
	"errors"
	"strings"
	"testing"

	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// scriptedCaller returns a prepared error per call, so a start-then-retry
// sequence can be driven deterministically.
type scriptedCaller struct {
	errs     []error
	toolErrs []bool
	calls    int
	lastArgs map[string]any
	lastTool string
}

func (s *scriptedCaller) CallTool(_ context.Context, _, toolName string, arguments map[string]any) (bool, error) {
	index := s.calls
	s.calls++
	s.lastArgs = arguments
	s.lastTool = toolName
	var err error
	if index < len(s.errs) {
		err = s.errs[index]
	}
	toolErr := false
	if index < len(s.toolErrs) {
		toolErr = s.toolErrs[index]
	}
	return toolErr, err
}

type scriptedStarter struct {
	err   error
	calls int
}

func (s *scriptedStarter) StartServer(string) error {
	s.calls++
	return s.err
}

type staticResolver string

func (s staticResolver) MaterializeRuntimeBinding(string, workspace.MCPBinding) (string, error) {
	return string(s), nil
}

// moverFixture builds a mover over a workspace that already holds a Janitor
// binding, which is what Move looks up before calling the connector.
func moverFixture(t *testing.T, caller ToolCaller, starter ToolStarter) *MCPMover {
	t.Helper()
	_, workspaces := newTestService(t)
	ws := workspaces.workspaces["ws-1"]
	if err := ws.UpsertMCPBinding(workspace.MCPBinding{
		ID:         "janitor-binding",
		ServerName: "filesystem",
		Alias:      JanitorBindingAlias,
		Enabled:    true,
		Config:     map[string]any{"roots": []string{t.TempDir()}},
	}); err != nil {
		t.Fatal(err)
	}
	mover := NewMCPMover(workspaces, staticResolver("ws:x"), caller)
	mover.SetStarter(starter)
	return mover
}

// Two moves in one batch race for the same connector: the first starts it, the
// second's own start attempt comes back "already running". That is the state
// the caller wanted, not a failure — treating it as one failed the second file
// of every multi-file batch while the first succeeded, a partial failure that
// single-file testing cannot produce.
func TestMCPMover_AlreadyRunningIsNotAFailedStart(t *testing.T) {
	caller := &scriptedCaller{errs: []error{errors.New("server ws:x is not running"), nil}}
	starter := &scriptedStarter{err: errors.New(`failed to start MCP server "ws:x": server already running`)}
	mover := moverFixture(t, caller, starter)

	if err := mover.Move(context.Background(), "ws-1", "/src/a.pdf", "/dst/a.pdf"); err != nil {
		t.Fatalf("an already-running connector must not fail the move: %v", err)
	}
	if caller.calls != 2 {
		t.Fatalf("the move should have been retried after the start, calls = %d", caller.calls)
	}
}

// A start that genuinely fails is still reported, with its reason.
func TestMCPMover_RealStartFailureIsReported(t *testing.T) {
	caller := &scriptedCaller{errs: []error{errors.New("server ws:x is not running")}}
	starter := &scriptedStarter{err: errors.New("exec: npx not found")}
	mover := moverFixture(t, caller, starter)

	err := mover.Move(context.Background(), "ws-1", "/src/a.pdf", "/dst/a.pdf")
	if err == nil {
		t.Fatal("a connector that cannot start must fail the move honestly")
	}
	if !strings.Contains(err.Error(), "could not be started") {
		t.Fatalf("the reason must survive: %v", err)
	}
}

// A connector that is already up is called once, with no start attempt.
func TestMCPMover_RunningConnectorIsNotRestarted(t *testing.T) {
	caller := &scriptedCaller{}
	starter := &scriptedStarter{}
	mover := moverFixture(t, caller, starter)

	if err := mover.Move(context.Background(), "ws-1", "/src/a.pdf", "/dst/a.pdf"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if caller.calls != 1 || starter.calls != 0 {
		t.Fatalf("calls = %d, starts = %d; a running connector needs neither", caller.calls, starter.calls)
	}
	if caller.lastTool != "move_file" {
		t.Fatalf("tool = %q", caller.lastTool)
	}
	if caller.lastArgs["source"] != "/src/a.pdf" || caller.lastArgs["destination"] != "/dst/a.pdf" {
		t.Fatalf("args = %+v", caller.lastArgs)
	}
}

// A tool that reports its own failure is a failed move, even without a
// transport error: the connector's word is not evidence the file moved.
func TestMCPMover_ToolReportedErrorFailsTheMove(t *testing.T) {
	caller := &scriptedCaller{toolErrs: []bool{true}}
	mover := moverFixture(t, caller, &scriptedStarter{})

	if err := mover.Move(context.Background(), "ws-1", "/src/a.pdf", "/dst/a.pdf"); err == nil {
		t.Fatal("a tool-reported error must fail the move")
	}
}
