package downloadsjanitor

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestResetAdmissionJanitorScanHandoffAndStopJoin(t *testing.T) {
	automation, _, _, _, _ := automationFixture(t)
	gate := &resetstate.WorkGate{}
	automation.SetAdmissionGate(gate)
	started, finish := make(chan struct{}), make(chan struct{})
	automation.scan = func(string, ScanSource) (JanitorBatch, bool, error) {
		close(started)
		<-finish
		return JanitorBatch{}, false, nil
	}
	automation.RunCoalescedScan("ws-1", ScanSourceWatcher)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("janitor scan did not start")
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("detached scan not tracked: %v", err)
	}
	release, err := gate.Enter()
	if err != nil {
		t.Fatalf("busy refusal fenced new work: %v", err)
	}
	release()
	stopped := make(chan struct{})
	go func() { automation.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned before scan joined")
	case <-time.After(20 * time.Millisecond):
	}
	close(finish)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not join")
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdmissionJanitorRefusesBeforeCallbacksOrMutation(t *testing.T) {
	automation, _, _, _, _ := automationFixture(t)
	gate := &resetstate.WorkGate{}
	automation.SetAdmissionGate(gate)
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	var scans, listings atomic.Int32
	automation.scan = func(string, ScanSource) (JanitorBatch, bool, error) {
		scans.Add(1)
		return JanitorBatch{}, false, nil
	}
	automation.RunCoalescedScan("ws-1", ScanSourceWatcher)
	automation.tick(func() []string { listings.Add(1); return []string{"ws-1"} })
	automation.Start(func() []string { listings.Add(1); return nil }, time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if scans.Load() != 0 || listings.Load() != 0 {
		t.Fatalf("fenced automation reached callbacks: scans=%d listings=%d", scans.Load(), listings.Load())
	}
	if automation.SchedulerRegistered("ws-1") {
		t.Fatal("fenced automation started scheduler")
	}
	if err := automation.EnsureWatcher("ws-1"); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced watcher mutation = %v", err)
	}
	automation.Stop()
}
