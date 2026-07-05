package workspace

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestClassifyLocalError(t *testing.T) {
	cases := map[string]localErrorClass{
		"dial tcp 127.0.0.1:11434: connect: connection refused": localErrorOffline,
		"lookup localhost: no such host":                        localErrorOffline,
		"connect: network is unreachable":                       localErrorOffline,
		"read tcp: connection reset by peer":                    localErrorColdLoad,
		"Post \"...\": context deadline exceeded":               localErrorColdLoad,
		"model is loading, please wait":                         localErrorColdLoad,
		"i/o timeout":                                           localErrorColdLoad,
		"invalid JSON in response":                              localErrorOther,
		"":                                                      localErrorOther,
	}
	for msg, want := range cases {
		var err error
		if msg != "" {
			err = errors.New(msg)
		}
		if got := classifyLocalError(err); got != want {
			t.Errorf("classifyLocalError(%q) = %d, want %d", msg, got, want)
		}
	}
}

func TestClassifyLocalError_OfflineWinsOverColdLoad(t *testing.T) {
	// A message with both a connect refusal and a timeout must classify offline.
	err := errors.New("dial tcp: connect: connection refused (i/o timeout)")
	if got := classifyLocalError(err); got != localErrorOffline {
		t.Fatalf("expected offline to win, got %d", got)
	}
}

func TestEffectiveTaskTimeout(t *testing.T) {
	// Explicit timeout honored regardless of provider.
	if got := effectiveTaskTimeout(90*time.Second, true, 2); got != 90*time.Second {
		t.Fatalf("explicit timeout = %v, want 90s", got)
	}
	// Cloud default unscaled.
	if got := effectiveTaskTimeout(0, false, 2); got != defaultTaskTimeout {
		t.Fatalf("cloud default = %v, want %v", got, defaultTaskTimeout)
	}
	// Local default scaled by the multiplier.
	if got := effectiveTaskTimeout(0, true, 2); got != 2*defaultTaskTimeout {
		t.Fatalf("local default = %v, want %v", got, 2*defaultTaskTimeout)
	}
	// Multiplier <= 1 leaves the default alone.
	if got := effectiveTaskTimeout(0, true, 1); got != defaultTaskTimeout {
		t.Fatalf("multiplier 1 = %v, want %v", got, defaultTaskTimeout)
	}
}

func TestLocalProviderConcurrency(t *testing.T) {
	t.Setenv("ORI_LOCAL_PROVIDER_CONCURRENCY", "")
	if got := localProviderConcurrency(); got != 1 {
		t.Fatalf("default = %d, want 1", got)
	}
	t.Setenv("ORI_LOCAL_PROVIDER_CONCURRENCY", "3")
	if got := localProviderConcurrency(); got != 3 {
		t.Fatalf("configured = %d, want 3", got)
	}
	t.Setenv("ORI_LOCAL_PROVIDER_CONCURRENCY", "bogus")
	if got := localProviderConcurrency(); got != 1 {
		t.Fatalf("invalid should fall back to 1, got %d", got)
	}
}

// localSchedFakeHandler is a fake task handler that also resolves a fixed
// provider profile, so the executor's per-provider concurrency can be exercised.
type localSchedFakeHandler struct {
	*fakeTaskHandler
	profile TaskProviderProfile
}

func (h *localSchedFakeHandler) ResolveTaskProviderProfile(_ Task) TaskProviderProfile {
	return h.profile
}

func TestCheckAndExecuteTasks_PerProviderConcurrencyLimit(t *testing.T) {
	store := newExecutorTestStore(t)
	tasks := make([]Task, 0, 3)
	for i := 0; i < 3; i++ {
		tasks = append(tasks, Task{
			ID:     "task-" + string(rune('a'+i)),
			To:     "agent-a",
			Status: TaskStatusAssigned,
		})
	}
	ws := newWorkspaceWithTasks(t, tasks)
	if err := store.Save(ws); err != nil {
		t.Fatalf("seed: %v", err)
	}

	release := make(chan struct{})
	handler := &localSchedFakeHandler{
		fakeTaskHandler: &fakeTaskHandler{block: release},
		profile: TaskProviderProfile{
			ConcurrencyKey: "local:ollama",
			Limit:          1,
			IsLocal:        true,
			OrderKey:       "ollama|m",
		},
	}

	// Global capacity is generous; the per-provider limit of 1 is the constraint.
	te := NewTaskExecutor(store, handler, ExecutorConfig{
		PollInterval:  time.Hour,
		MaxConcurrent: 5,
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		te.Stop()
	})

	te.checkAndExecuteTasks()

	// Exactly one task should be running; the other two are deferred by the
	// per-provider limit, not failed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt64(&handler.calls) < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt64(&handler.calls); got != 1 {
		t.Fatalf("running tasks = %d, want 1 (per-provider limit)", got)
	}
	// Confirm it stays at 1 (limit holds) rather than draining all three.
	time.Sleep(150 * time.Millisecond)
	if got := atomic.LoadInt64(&handler.calls); got != 1 {
		t.Fatalf("per-provider limit breached: running = %d, want 1", got)
	}

	// The deferred tasks are still assigned (not failed/cancelled).
	fresh, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assigned := 0
	for _, task := range fresh.Tasks {
		if task.Status == TaskStatusAssigned {
			assigned++
		}
	}
	if assigned != 2 {
		t.Fatalf("deferred tasks assigned = %d, want 2", assigned)
	}
}
