package learningmissions

import (
	"context"
	"testing"
	"time"
)

func TestMilestoneWorkerCancellationJoinsTheOwnedCallWithoutPublishing(t *testing.T) {
	s, designer, out, _ := milestoneFixture(t, true)
	advanceMilestoneClock(s, MilestoneDelay)
	entered := make(chan struct{})
	designer.generate = func(ctx context.Context, _ ContextSnapshot) (GenerationReply, error) {
		close(entered)
		<-ctx.Done()
		return GenerationReply{}, ctx.Err()
	}
	worker := NewMilestoneWorker(s)
	t.Cleanup(worker.Stop)
	// Constructor/startup does not scan durable events. Only the host wakeup
	// from a fresh review commit can dispatch this queued identity.
	if designer.calls.Load() != 0 {
		t.Fatal("worker startup caught up an event")
	}
	s.milestoneWake("local", out.Operation.MilestoneID)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not dispatch")
	}
	worker.Stop()
	claim, err := s.store.Operation(t.Context(), "local", "auto-"+out.Operation.MilestoneID)
	if err != nil || claim.Status != "failed" || claim.Failure != "cancelled" || claim.InstanceID != "" || claim.ReservedTokens != GenerationReservedTokens || designer.calls.Load() != 1 {
		t.Fatal("shutdown published or lost the cost reservation", claim, err)
	}
	worker.Stop() // repeated shutdown is safe
}
