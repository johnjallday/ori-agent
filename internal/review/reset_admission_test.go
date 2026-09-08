package review

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/session"
)

type resetReviewStore struct {
	Store
	creates atomic.Int32
	updated chan struct{}
	finish  chan struct{}
	once    sync.Once
}

func (s *resetReviewStore) CreateReviewRun(context.Context) (*Run, error) {
	s.creates.Add(1)
	return &Run{ID: "owned-review", Status: ReviewRunStatusRunning}, nil
}
func (s *resetReviewStore) UpdateReviewRun(context.Context, *Run) error {
	s.once.Do(func() { close(s.updated); <-s.finish })
	return nil
}

type resetReviewSessions struct {
	session.SessionStore
	started chan context.Context
	finish  chan struct{}
}

func (s *resetReviewSessions) ListSessions(ctx context.Context, _ *session.SessionFilter, _ *session.ListOptions) (*session.ListResult, error) {
	s.started <- ctx
	select {
	case <-s.finish:
		return &session.ListResult{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestResetAdmissionReviewOwnsDetachedWorkThroughFinalStatus(t *testing.T) {
	store := &resetReviewStore{updated: make(chan struct{}), finish: make(chan struct{})}
	sessions := &resetReviewSessions{started: make(chan context.Context, 1), finish: make(chan struct{})}
	runner := NewRunner(store, sessions, nil, DefaultDetectionConfig())
	gate := &resetstate.WorkGate{}
	runner.SetAdmissionGate(gate)
	if _, err := runner.StartReview(t.Context(), Options{}); err != nil {
		t.Fatal(err)
	}
	var workCtx context.Context
	select {
	case workCtx = <-sessions.started:
	case <-time.After(5 * time.Second):
		t.Fatal("review did not start")
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("review read not tracked: %v", err)
	}
	select {
	case <-workCtx.Done():
		t.Fatalf("busy reset cancelled review: %v", workCtx.Err())
	default:
	}
	closeSessions := sync.OnceFunc(func() { close(sessions.finish) })
	closeUpdate := sync.OnceFunc(func() { close(store.finish) })
	t.Cleanup(func() { closeSessions(); closeUpdate() })
	closeSessions()
	select {
	case <-store.updated:
	case <-time.After(5 * time.Second):
		t.Fatal("review did not reach final status")
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("review final status not tracked: %v", err)
	}
	closeUpdate()
	deadline := time.Now().Add(5 * time.Second)
	for gate.Snapshot().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdmissionReviewRefusesBeforeCreatingRun(t *testing.T) {
	store := &resetReviewStore{updated: make(chan struct{}), finish: make(chan struct{})}
	runner := NewRunner(store, nil, nil, DefaultDetectionConfig())
	gate := &resetstate.WorkGate{}
	runner.SetAdmissionGate(gate)
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.StartReview(t.Context(), Options{}); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced review = %v", err)
	}
	if store.creates.Load() != 0 {
		t.Fatal("fenced review created a durable run")
	}
}
