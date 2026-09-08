package resetstate

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestWorkGateRefusesBusyWithoutCancellingOrClosingAdmission(t *testing.T) {
	g := &WorkGate{}
	release, err := g.Enter()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	if err := g.TryFence(t.Context()); !errors.Is(err, ErrWorkActive) {
		t.Fatalf("busy fence: %v", err)
	}
	second, err := g.Enter()
	if err != nil {
		t.Fatalf("refusal fenced ordinary work: %v", err)
	}
	second()
	release()
	if got := g.Snapshot(); got.Active != 0 || got.Fenced || !got.Known {
		t.Fatalf("after release: %+v", got)
	}
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Enter(); !errors.Is(err, ErrWorkFenced) {
		t.Fatalf("entry after fence: %v", err)
	}
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatalf("repeat fence: %v", err)
	}
}

func TestWorkGateFencesFiniteWorkThenLeavesLifetimeOwnersForDrain(t *testing.T) {
	g := &WorkGate{}
	stopOwner, err := g.EnterLifetime()
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Snapshot(); got.Active != 0 || got.Owners != 1 {
		t.Fatalf("lifetime registration = %+v", got)
	}
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Enter(); !errors.Is(err, ErrWorkFenced) {
		t.Fatalf("finite entry after fence = %v", err)
	}
	if _, err := g.EnterLifetime(); !errors.Is(err, ErrWorkFenced) {
		t.Fatalf("lifetime entry after fence = %v", err)
	}
	stopOwner()
	stopOwner()
	if got := g.Snapshot(); got.Owners != 0 || !got.Fenced {
		t.Fatalf("drained owner = %+v", got)
	}
}

func TestWorkGateEntryAndFenceHaveOneWinner(t *testing.T) {
	for range 1000 {
		g := &WorkGate{}
		start := make(chan struct{})
		var entryErr, fenceErr error
		var release func()
		var wg sync.WaitGroup
		wg.Go(func() { <-start; release, entryErr = g.Enter() })
		wg.Go(func() { <-start; fenceErr = g.TryFence(t.Context()) })
		close(start)
		wg.Wait()
		switch {
		case entryErr == nil && errors.Is(fenceErr, ErrWorkActive):
			release()
		case errors.Is(entryErr, ErrWorkFenced) && fenceErr == nil:
		default:
			t.Fatalf("no exclusive winner: entry=%v fence=%v", entryErr, fenceErr)
		}
	}
}

func TestWorkGateReleaseIsIdempotentAcrossGoroutines(t *testing.T) {
	g := &WorkGate{}
	release, err := g.Enter()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(release)
	}
	wg.Wait()
	if got := g.Snapshot().Active; got != 0 {
		t.Fatalf("active=%d", got)
	}
	if err := g.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestWorkGateCancellationAndMissingInstrumentationAreNotIdle(t *testing.T) {
	g := &WorkGate{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := g.TryFence(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled fence: %v", err)
	}
	if g.Snapshot().Fenced {
		t.Fatal("cancelled request fenced work")
	}
	var missing *WorkGate
	if missing.Snapshot().Known {
		t.Fatal("unwired gate represented as idle")
	}
	if err := missing.TryFence(t.Context()); !errors.Is(err, ErrWorkUntracked) {
		t.Fatalf("unwired fence: %v", err)
	}
	release, err := missing.Enter()
	if err != nil {
		t.Fatal(err)
	}
	release()
}
