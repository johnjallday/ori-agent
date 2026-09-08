package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestResetLifecycleEventShutdownDoesNotJoinAlreadyDispatchedSaves(t *testing.T) {
	f := resetfixture.New(t)
	bus := workspace.NewEventBus(4, 4)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	bus.Subscribe(func(workspace.Event) {
		close(started)
		<-release
		done <- f.WriteFile("data/late-event.txt", []byte("late fixture event"))
	}, nil)
	bus.Publish(workspace.Event{Type: workspace.EventType("fixture")})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	defer func() {
		unblock()
		bus.Shutdown()
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("event callback did not start")
	}
	// If Shutdown joined callbacks this could not return before release.
	stopped := make(chan struct{})
	go func() { bus.Shutdown(); close(stopped) }()
	select {
	case <-stopped:
	case <-ctx.Done():
		unblock()
		<-stopped
		t.Fatal("characterization changed: EventBus.Shutdown now waits for callbacks")
	}
	unblock()
	select {
	case err := <-done:
		requireResetNoError(t, err)
	case <-ctx.Done():
		t.Fatal("event callback did not finish")
	}
}

func TestResetLifecycleServerShutdownLeavesCostWriterActive(t *testing.T) {
	f := resetfixture.New(t)
	dir := filepath.Join(f.Paths().Home, ".ori-agent", "usage_data")
	tracker := llm.NewCostTracker(dir)
	t.Cleanup(tracker.Close)
	srv := &Server{Core: &CoreSystemFacade{CostTracker: tracker}}
	srv.Shutdown()
	requireResetNoError(t, tracker.TrackUsage("openai", "gpt-4o", "Fixture", llm.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}, "fixture-request"))
	tracker.Close() // joins the owner's real background writer and final flush
	data, err := os.ReadFile(filepath.Join(dir, "usage_records.json"))
	requireResetNoError(t, err)
	var records []llm.UsageRecord
	requireResetNoError(t, json.Unmarshal(data, &records))
	if len(records) != 1 {
		t.Fatal("expected post-shutdown usage write to persist")
	}
}
