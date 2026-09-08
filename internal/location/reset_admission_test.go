package location

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

type blockingResetDetector struct {
	started chan context.Context
	finish  chan struct{}
	once    sync.Once
}

func (d *blockingResetDetector) Name() string { return "mock-wifi" }
func (d *blockingResetDetector) Detect(ctx context.Context) (string, error) {
	d.once.Do(func() { d.started <- ctx })
	select {
	case <-d.finish:
		return "owned-network", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func waitResetLocation(t *testing.T, ch <-chan context.Context) context.Context {
	t.Helper()
	select {
	case ctx := <-ch:
		return ctx
	case <-time.After(5 * time.Second):
		t.Fatal("location work missed owned checkpoint")
		return nil
	}
}

func TestResetAdmissionLocationDetectionIsJoinedNotCancelled(t *testing.T) {
	detector := &blockingResetDetector{started: make(chan context.Context, 1), finish: make(chan struct{})}
	manager := NewManager([]Detector{detector}, nil)
	gate := &resetstate.WorkGate{}
	manager.SetAdmissionGate(gate)
	started := make(chan struct{})
	go func() { manager.Start(context.Background(), time.Hour); close(started) }()
	workCtx := waitResetLocation(t, detector.started)
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("detection not tracked: %v", err)
	}
	select {
	case <-workCtx.Done():
		t.Fatalf("busy reset cancelled detection: %v", workCtx.Err())
	default:
	}
	release, err := gate.Enter()
	if err != nil {
		t.Fatalf("busy refusal fenced new work: %v", err)
	}
	release()
	close(detector.finish)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("initial detection did not finish")
	}
	manager.Stop()
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdmissionLocationCallbacksTransferOwnership(t *testing.T) {
	manager := NewManager(nil, nil)
	gate := &resetstate.WorkGate{}
	manager.SetAdmissionGate(gate)
	callbackStarted, callbackFinish := make(chan struct{}), make(chan struct{})
	manager.OnLocationChange(func(LocationChangeEvent) { close(callbackStarted); <-callbackFinish })
	setDone := make(chan error, 1)
	go func() { setDone <- manager.SetManualLocation("Home") }()
	select {
	case <-callbackStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("callback did not start")
	}
	select {
	case err := <-setDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("manual update did not hand off")
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("callback not tracked: %v", err)
	}
	close(callbackFinish)
	manager.Stop()
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdmissionLocationRefusesBeforeMutation(t *testing.T) {
	manager := NewManager(nil, nil)
	gate := &resetstate.WorkGate{}
	manager.SetAdmissionGate(gate)
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetManualLocation("must-not-apply"); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	if got := manager.GetCurrentLocation(); got != "Unknown" {
		t.Fatalf("fenced location changed: %q", got)
	}
	if err := manager.AddZone(Zone{Name: "must-not-write"}); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	if zones := manager.GetZones(); len(zones) != 0 {
		t.Fatalf("fenced zone added: %+v", zones)
	}
}
