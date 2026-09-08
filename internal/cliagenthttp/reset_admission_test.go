package cliagenthttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/cliagent"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

// This adapter stops at a deterministic fake availability check. It never
// executes a provider, process, git probe, native credential call or file write.
type resetPausedAdapter struct {
	cliagent.Adapter
	release <-chan struct{}
}

func (*resetPausedAdapter) Backend() string     { return cliagent.BackendClaude }
func (a *resetPausedAdapter) IsAvailable() bool { <-a.release; return false }

type resetAdmissionResponse struct {
	*httptest.ResponseRecorder
	check func()
}

func (w *resetAdmissionResponse) WriteHeader(code int) {
	w.check()
	w.ResponseRecorder.WriteHeader(code)
}

func TestResetAdmissionCLIHTTPRegistersBeforeAcknowledgingDetachedWork(t *testing.T) {
	g := &resetstate.WorkGate{}
	release := make(chan struct{})
	adapter := &resetPausedAdapter{release: release}
	registry := cliagent.NewRegistry(adapter)
	// Leave the executor's gate unwired on purpose: this assertion must prove
	// the handler registered BEFORE 202, not be masked by later executor entry.
	executor := cliagent.NewMicroStepExecutor(registry, nil, nil, nil, nil)
	handler := NewHandler(executor, registry, nil)
	handler.SetAdmissionGate(g)
	finish := sync.OnceFunc(func() { close(release) })
	t.Cleanup(func() {
		finish()
		deadline := time.Now().Add(5 * time.Second)
		for g.Snapshot().Active != 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if g.Snapshot().Active != 0 {
			t.Error("detached fixture did not join")
		}
	})
	body, err := json.Marshal(createTaskRequest{CLIBackend: cliagent.BackendClaude, Prompt: "fixture", WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	w := &resetAdmissionResponse{ResponseRecorder: httptest.NewRecorder(), check: func() {
		if err := g.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
			t.Errorf("202 acknowledged unregistered work: %v", err)
		}
	}}
	handler.HandleCreateTask(w, httptest.NewRequest(http.MethodPost, "/api/cli-agents/tasks", bytes.NewReader(body)))
	if w.Code != http.StatusAccepted {
		t.Fatalf("create=%d", w.Code)
	}
}

func TestResetAdmissionCLIHTTPRefusesBeforeLaunching(t *testing.T) {
	g := &resetstate.WorkGate{}
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, nil, nil)
	h.SetAdmissionGate(g)
	body, err := json.Marshal(createTaskRequest{CLIBackend: cliagent.BackendClaude, Prompt: "fixture", WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.HandleCreateTask(w, httptest.NewRequest(http.MethodPost, "/api/cli-agents/tasks", bytes.NewReader(body)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("fenced create=%d", w.Code)
	}
}
