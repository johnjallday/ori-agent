package dailybrief

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

type resetBriefGenerator struct {
	started chan context.Context
	release chan struct{}
	result  GenerationResult
	err     error
}

func (g *resetBriefGenerator) Generate(ctx context.Context, _ GenerationRequest, _ Config) (GenerationResult, error) {
	g.started <- ctx
	select {
	case <-g.release:
		return g.result, g.err
	case <-ctx.Done():
		return GenerationResult{}, ctx.Err()
	}
}

func waitResetBrief[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("daily brief did not reach owned checkpoint")
		var zero T
		return zero
	}
}

func TestResetAdmissionDailyBriefTracksGeneratorCallbackAndFinalStatus(t *testing.T) {
	store := newServiceTestStore(t)
	seedConfig(t, store, "ws-1")
	generator := &resetBriefGenerator{
		started: make(chan context.Context, 1), release: make(chan struct{}),
		result: GenerationResult{Status: GenerationSucceeded, ContentJSON: `{"opening_summary":"owned"}`},
	}
	gate := &resetstate.WorkGate{}
	svc := NewService(store, generator)
	svc.SetAdmissionGate(gate)
	callbackStarted := make(chan struct{})
	callbackRelease := make(chan struct{})
	svc.SetOnRevisionReady(func(Config, *Revision) { close(callbackStarted); <-callbackRelease })
	finishGenerator := sync.OnceFunc(func() { close(generator.release) })
	finishCallback := sync.OnceFunc(func() { close(callbackRelease) })
	t.Cleanup(func() { finishGenerator(); finishCallback() })

	done := make(chan error, 1)
	go func() { _, err := svc.RequestGenerationNow(t.Context(), "ws-1", "local", TriggerManual); done <- err }()
	ctx := waitResetBrief(t, generator.started)
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("generator not tracked: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("reset cancelled generation")
	}
	release, err := gate.Enter()
	if err != nil {
		t.Fatalf("busy refusal fenced ordinary work: %v", err)
	}
	release()
	finishGenerator()
	waitResetBrief(t, callbackStarted)
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("notification/final status not tracked: %v", err)
	}
	finishCallback()
	if err := waitResetBrief(t, done); err != nil {
		t.Fatal(err)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetCurrentRevision(t.Context(), "ws-1")
	if err != nil || current.Status != GenerationSucceeded {
		t.Fatalf("final revision not persisted: %+v, %v", current, err)
	}
}

func TestResetAdmissionDailyBriefRefusesBeforeOwners(t *testing.T) {
	gate := &resetstate.WorkGate{}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, nil)
	svc.SetAdmissionGate(gate)
	cfg := Config{WorkspaceID: "ws"}
	if _, err := svc.UpdateConfig(t.Context(), cfg); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	if _, err := svc.RequestGenerationNow(t.Context(), "ws", "user", TriggerManual); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	if _, err := svc.RequestGeneration(t.Context(), cfg, "user", TriggerManual, "2026-01-01"); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	if _, _, err := svc.GenerateFirstAssignmentBrief(t.Context(), cfg, "user", TriggerManual, "request"); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	if err := svc.PruneHistory(t.Context(), "ws"); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	if _, err := svc.RecordNotificationIfEnabled(t.Context(), cfg, nil); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}

	scheduler := NewScheduler(nil, nil, time.Millisecond)
	scheduler.SetAdmissionGate(gate)
	scheduler.Tick()
	scheduler.Start()
	scheduler.Stop()
}
