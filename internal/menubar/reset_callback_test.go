package menubar

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestResetAdmissionMenubarStatusCallbackOwnedBeforeLaunch(t *testing.T) {
	lease, err := resetstate.Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	controller := NewControllerWithResetLease(0, lease)
	started, finish := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	controller.WatchStatus(func(ServerStatus) { calls.Add(1); close(started); <-finish })
	controller.notifyStatusChange(StatusRunning)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("status callback did not start")
	}
	if err := lease.WorkGate().TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("callback not tracked: %v", err)
	}
	closeOnce := sync.OnceFunc(func() { close(finish) })
	t.Cleanup(closeOnce)
	closeOnce()
	deadline := time.Now().Add(5 * time.Second)
	for lease.WorkGate().Snapshot().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := lease.WorkGate().TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	controller.notifyStatusChange(StatusStopped)
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("fenced controller launched another callback")
	}
}
