package plugin

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestResetAdmissionPluginUpdateCheckIsJoinedNotCancelled(t *testing.T) {
	source := newFakeUpdateCheckSource("demo")
	source.blockName = "demo"
	source.entered = make(chan struct{})
	source.release = make(chan struct{})
	checker := NewUpdateChecker(source)
	gate := &resetstate.WorkGate{}
	checker.SetAdmissionGate(gate)
	checker.Start(time.Hour)
	select {
	case <-source.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("plugin source check did not start")
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("source check not tracked: %v", err)
	}
	release, err := gate.Enter()
	if err != nil {
		t.Fatalf("busy refusal fenced new work: %v", err)
	}
	release()
	stopped := make(chan struct{})
	go func() { checker.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned before source check joined")
	case <-time.After(20 * time.Millisecond):
	}
	close(source.release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("source check did not join")
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdmissionPluginUpdateRefusesBeforeSourceOrCacheMutation(t *testing.T) {
	source := newFakeUpdateCheckSource("demo")
	checker := NewUpdateChecker(source)
	gate := &resetstate.WorkGate{}
	checker.SetAdmissionGate(gate)
	checker.checkCycle()
	if len(checker.Snapshot().Updates) != 1 {
		t.Fatal("fixture update was not cached")
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	checker.Invalidate("demo")
	if len(checker.Snapshot().Updates) != 1 {
		t.Fatal("fenced invalidation changed cache")
	}

	other := NewUpdateChecker(newFakeUpdateCheckSource("other"))
	other.SetAdmissionGate(gate)
	other.Start(time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if other.source.(*fakeUpdateCheckSource).callCount("other") != 0 {
		t.Fatal("fenced checker reached plugin source")
	}
	other.Stop()
}
