package llm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func waitResetCost(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("cost writer missed owned checkpoint")
	}
}

func TestResetAdmissionCostTrackerOwnsAsynchronousPersistence(t *testing.T) {
	dir := t.TempDir()
	gate := &resetstate.WorkGate{}
	tracker := NewCostTracker(dir)
	tracker.SetAdmissionGate(gate)
	started, persist := make(chan struct{}), make(chan struct{})
	var once sync.Once
	tracker.beforePersist = func() { once.Do(func() { close(started); <-persist }) }
	finish := sync.OnceFunc(func() { close(persist) })
	t.Cleanup(func() { finish(); tracker.Close() })

	if err := tracker.TrackUsage("openai", "fixture-model", "fixture-agent", Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}, "fixture-request"); err != nil {
		t.Fatal(err)
	}
	waitResetCost(t, started)
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("async persistence not tracked: %v", err)
	}
	release, err := gate.Enter()
	if err != nil {
		t.Fatalf("busy refusal fenced ordinary work: %v", err)
	}
	release()
	finish()
	tracker.Close()
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "usage_records.json"))
	if err != nil {
		t.Fatal(err)
	}
	var records []UsageRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].RequestID != "fixture-request" {
		t.Fatalf("final records = %+v", records)
	}
	if err := tracker.TrackUsage("openai", "fixture-model", "fixture-agent", Usage{}, "late"); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("post-fence write = %v", err)
	}
}

func TestResetAdmissionCostTrackerCloseRejectsAndReleasesLateWriter(t *testing.T) {
	gate := &resetstate.WorkGate{}
	tracker := NewCostTracker(t.TempDir())
	tracker.SetAdmissionGate(gate)
	tracker.Close()
	tracker.Close()
	if err := tracker.TrackUsage("openai", "fixture-model", "fixture-agent", Usage{}, "late"); !errors.Is(err, ErrCostTrackerClosed) {
		t.Fatalf("post-close write = %v", err)
	}
	if snapshot := gate.Snapshot(); snapshot.Active != 0 || snapshot.Fenced {
		t.Fatalf("post-close permit leaked: %+v", snapshot)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdmissionCostTrackerRefusesBeforeMutation(t *testing.T) {
	gate := &resetstate.WorkGate{}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	tracker := NewCostTracker(t.TempDir())
	tracker.SetAdmissionGate(gate)
	t.Cleanup(tracker.Close)
	if err := tracker.TrackUsage("openai", "fixture-model", "fixture-agent", Usage{}, "blocked"); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	if stats := tracker.GetAllTimeStats(); stats.TotalRequests != 0 {
		t.Fatalf("fenced record entered memory: %+v", stats)
	}
}
