package learningmissions

import (
	"context"
	"time"
)

type milestoneWake struct{ owner, id string }

// MilestoneWorker is a bounded event wakeup, not a polling scheduler. Only
// queueMilestone's fresh review commits wake it. Durable consent, expiry,
// fingerprints and budgets remain authoritative at dispatch/publication.
type MilestoneWorker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// NewMilestoneWorker is startup-only wiring, after retiring old pending events.
func NewMilestoneWorker(service *Service) *MilestoneWorker {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &MilestoneWorker{cancel: cancel, done: make(chan struct{})}
	wake := make(chan milestoneWake, 1)
	service.milestoneWake = func(owner, id string) {
		select {
		case <-ctx.Done():
		case wake <- milestoneWake{owner, id}:
		default: // coalesced; never an unbounded in-memory queue
		}
	}
	go func() {
		defer close(worker.done)
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-wake:
				op, err := service.store.Operation(ctx, event.owner, event.id)
				if err != nil || op.Status != "pending" || op.Milestone == nil {
					continue
				}
				wait := max(time.Duration(op.Milestone.NotBefore-service.now().UnixMilli())*time.Millisecond, 0)
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				// Optional suggestions never affect actual work. An unavailable
				// store leaves a durable unknown/pending event, never a retry loop.
				_ = service.ProcessMilestone(ctx, event.owner, event.id)
			}
		}
	}()
	return worker
}

func (w *MilestoneWorker) Stop() {
	if w == nil {
		return
	}
	w.cancel()
	<-w.done
}
