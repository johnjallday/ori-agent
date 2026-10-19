package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

// Shutdown joins the writers the gate admitted — a task's tail after its
// status flipped, a subscriber persisting what that completion meant — before
// anything they write to is closed behind it, and leaves the gate open because
// it is shared with the lease and the next hosted instance.
func TestShutdownWaitsForAdmittedWorkWithoutFencing(t *testing.T) {
	g := &resetstate.WorkGate{}
	release, err := g.Enter()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{resetWork: g}
	stopped := make(chan struct{})
	go func() { s.Shutdown(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Shutdown returned while admitted work was still running")
	case <-time.After(50 * time.Millisecond):
	}
	// Work admitted while Shutdown waits is joined too; nothing was fenced.
	later, err := g.Enter()
	if err != nil {
		t.Fatalf("Shutdown fenced ordinary work: %v", err)
	}
	release()
	select {
	case <-stopped:
		t.Fatal("Shutdown returned with a later operation still admitted")
	case <-time.After(50 * time.Millisecond):
	}
	later()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return after the admitted work released")
	}
	if err := s.shutdownBackground(t.Context()); err != nil {
		t.Fatalf("shutdown reported: %v", err)
	}
	if got := g.Snapshot(); got.Fenced || got.Active != 0 {
		t.Fatalf("gate after shutdown: %+v", got)
	}
	again, err := g.Enter()
	if err != nil {
		t.Fatalf("entry after shutdown: %v", err)
	}
	again()
}

// A stuck operation must not hold Quit: the wait is bounded, the shortfall is
// reported, and the gate is still not fenced.
func TestShutdownStopsWaitingForStuckAdmittedWorkAndReportsIt(t *testing.T) {
	g := &resetstate.WorkGate{}
	release, err := g.Enter()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	s := &Server{resetWork: g, shutdownWorkWait: 20 * time.Millisecond}
	stopped := make(chan struct{})
	go func() { s.Shutdown(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown waited past its bound for stuck work")
	}
	err = s.shutdownBackground(t.Context())
	if !errors.Is(err, errAdmittedWorkStillRunning) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stuck work was not reported: %v", err)
	}
	if got := g.Snapshot(); got.Fenced || got.Active != 1 {
		t.Fatalf("gate after bounded shutdown: %+v", got)
	}
	release()
	if err := s.shutdownBackground(t.Context()); err != nil {
		t.Fatalf("completed work could not retry shutdown: %v", err)
	}
}

// No gate means nothing to join; a Server built without one shuts down as before.
func TestShutdownWithoutGateDoesNotWait(t *testing.T) {
	s := &Server{}
	stopped := make(chan struct{})
	go func() { s.Shutdown(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown without a gate waited")
	}
	if err := s.shutdownBackground(t.Context()); err != nil {
		t.Fatal(err)
	}
}
