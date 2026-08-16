package workspaceplan

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"testing"
)

// Lifecycle events and logs carry identifiers only (FR-173, FR-174).
//
// The claim these hold is narrow and load-bearing: a subscriber or a log reader
// learns THAT something happened and to WHICH record, never what the plan says.
// A plan's objective and steps are the user's material, and events fan out
// while logs get pasted into bug reports.

type recordingPublisher struct {
	mu     sync.Mutex
	events []PlanEvent
}

func (p *recordingPublisher) PublishPlanEvent(_ context.Context, event PlanEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event)
}

func (p *recordingPublisher) all() []PlanEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]PlanEvent(nil), p.events...)
}

// A transition announces itself with IDs and a status, and the serialized event
// contains none of the plan's words.
func TestLifecycleEventsCarryNoPlanContent(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	publisher := &recordingPublisher{}
	service.SetEventPublisher(publisher)

	plan := newReviewablePlan(t, ctx, service, reviewableContent())
	if _, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"}); err != nil {
		t.Fatalf("request review: %v", err)
	}

	events := publisher.all()
	if len(events) == 0 {
		t.Fatal("no lifecycle event was published")
	}

	// Serialize and look for the plan's actual words. The objective and every
	// step description must be absent.
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	payload := string(encoded)

	forbidden := []string{
		"Migrate reporting safely", // the objective
		"Snapshot staging",         // an item description
		"Verify checksums",
		"Ship the migration", // the title
	}
	for _, text := range forbidden {
		if strings.Contains(payload, text) {
			t.Errorf("the event payload leaked plan content: %q in %s", text, payload)
		}
	}

	// It does carry the identifiers a subscriber needs to go and read it.
	if events[0].PlanID != plan.ID || events[0].WorkspaceID != "ws-1" {
		t.Errorf("the event does not identify the plan: %+v", events[0])
	}
	if events[0].Status != StatusInReview {
		t.Errorf("status = %q, want in_review", events[0].Status)
	}
}

// panickingPublisher is a subscriber that is broken in the worst available way.
type panickingPublisher struct{ called bool }

func (p *panickingPublisher) PublishPlanEvent(context.Context, PlanEvent) {
	p.called = true
	panic("subscriber exploded")
}

// A broken subscriber must not be able to stop a plan from moving.
//
// The event is a notification ABOUT something that already happened and is
// already durable. Letting delivery fail the operation would mean a slow or
// crashing listener could block approvals — the tail wagging the dog (FR-172).
func TestABrokenSubscriberDoesNotFailTheLifecycle(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	publisher := &panickingPublisher{}
	service.SetEventPublisher(publisher)

	plan := newReviewablePlan(t, ctx, service, reviewableContent())

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("a broken subscriber propagated its panic into the lifecycle: %v", recovered)
		}
	}()

	if _, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"}); err != nil {
		t.Fatalf("a broken subscriber failed the transition: %v", err)
	}
	if !publisher.called {
		t.Error("the publisher was never called; the test proved nothing")
	}

	// And the state change is durable regardless.
	reread, err := service.Get(ctx, "ws-1", plan.ID)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if reread.Status != StatusInReview {
		t.Errorf("status = %q, want in_review despite the broken subscriber", reread.Status)
	}
}

// A build with no publisher is quiet, not broken.
func TestPublishingIsOptional(t *testing.T) {
	ctx := context.Background()
	service := reviewService(t)
	plan := newReviewablePlan(t, ctx, service, reviewableContent())

	if _, err := service.RequestReview(ctx, "ws-1", plan.ID, ReviewInput{Actor: "jj"}); err != nil {
		t.Fatalf("a lifecycle move failed with no publisher wired: %v", err)
	}
}

// A status with no event is silently unannounced rather than emitting an empty
// type subscribers would have to guess at.
func TestUnmappedStatusesAnnounceNothing(t *testing.T) {
	for _, status := range []Status{StatusDraft, StatusNeedsInput} {
		if _, announced := planEventFor(status); announced {
			t.Errorf("%s produced an event; only meaningful moves are announced", status)
		}
	}
	for _, status := range []Status{StatusApproved, StatusExecuting, StatusCompleted} {
		kind, announced := planEventFor(status)
		if !announced || kind == "" {
			t.Errorf("%s produced no event type", status)
		}
	}
}

// --- Operational logs (FR-174) ---------------------------------------------

func TestLifecycleLogsCarryIdentifiersNotContent(t *testing.T) {
	var buffer bytes.Buffer
	logger := log.New(&buffer, "", 0)

	LogLifecycle(logger, PlanEvent{
		Type:        PlanEventMaterialized,
		PlanID:      "plan_123",
		WorkspaceID: "ws-1",
		Status:      StatusApproved,
		Version:     3,
		TaskCount:   7,
	})

	line := buffer.String()
	for _, want := range []string{"plan.materialized", "plan=plan_123", "studio=ws-1", "version=3", "tasks=7"} {
		if !strings.Contains(line, want) {
			t.Errorf("the log line is missing %q: %q", want, line)
		}
	}
	// Nothing in the event type can carry content, but the assertion is what
	// stops a future field from being added that does.
	if strings.Contains(line, "objective") || strings.Contains(line, "description") {
		t.Errorf("the log line carries plan content: %q", line)
	}
}

func TestLogsOmitEmptyFieldsRatherThanPrintingBlanks(t *testing.T) {
	var buffer bytes.Buffer
	LogLifecycle(log.New(&buffer, "", 0), PlanEvent{
		Type:        PlanEventCreated,
		PlanID:      "plan_123",
		WorkspaceID: "ws-1",
	})

	line := buffer.String()
	for _, absent := range []string{"version=", "task=", "run=", "tasks="} {
		if strings.Contains(line, absent) {
			t.Errorf("the log line printed an empty %q: %q", absent, line)
		}
	}
}
