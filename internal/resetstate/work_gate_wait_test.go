package resetstate

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A host that is only shutting down may let admitted writers finish, and may
// not fence: the gate is shared with the lease and the next hosted instance.
func TestWorkGateWaitIdleJoinsFiniteWorkWithoutFencing(t *testing.T) {
	g := &WorkGate{}
	stopOwner, err := g.EnterLifetime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stopOwner)
	_, endStream, err := g.EnterStream(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(endStream)
	release, err := g.Enter()
	if err != nil {
		t.Fatal(err)
	}

	idle := make(chan error, 1)
	go func() { idle <- g.WaitIdle(t.Context()) }()
	select {
	case err := <-idle:
		t.Fatalf("WaitIdle returned while finite work was admitted: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	// Work admitted during the wait is joined too, and nothing was fenced.
	second, err := g.Enter()
	if err != nil {
		t.Fatalf("WaitIdle fenced ordinary work: %v", err)
	}
	release()
	select {
	case err := <-idle:
		t.Fatalf("WaitIdle returned with a later operation still admitted: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	second()
	select {
	case err := <-idle:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitIdle did not return after the last release")
	}
	// The owner and the stream never held it up, and the gate is still open.
	if got := g.Snapshot(); got.Fenced || got.Active != 0 || got.Owners != 1 || got.Streams != 1 {
		t.Fatalf("after wait: %+v", got)
	}
	third, err := g.Enter()
	if err != nil {
		t.Fatalf("entry after wait: %v", err)
	}
	third()
}

func TestWorkGateWaitIdleReturnsContextErrorAndChangesNothing(t *testing.T) {
	g := &WorkGate{}
	release, err := g.Enter()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := g.WaitIdle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired wait = %v", err)
	}
	if got := g.Snapshot(); got.Fenced || got.Active != 1 {
		t.Fatalf("an expired wait changed state: %+v", got)
	}
}

func TestNilWorkGateWaitIdleIsImmediate(t *testing.T) {
	var g *WorkGate
	if err := g.WaitIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
}
