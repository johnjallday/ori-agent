package workspaceplan

import (
	"context"
	"log"
	"strconv"
)

// Plan lifecycle events and operational logs (FR-172–FR-175).
//
// Both carry IDENTIFIERS ONLY. A Plan's content is the user's material: their
// objective, their steps, sometimes what they are trying to do and why. Events
// fan out to subscribers and logs land in files that get pasted into bug
// reports, so putting content in either would copy that material into places
// nobody chose to put it.
//
// Everything here is therefore built from IDs, a type, a time, and counts. A
// subscriber that needs the content reads the Plan; a reader who needs the
// content opens the app. That indirection is the point — it means the person
// looking has to be entitled to look.

// PlanEventPublisher receives redacted lifecycle events.
//
// It is an interface rather than a direct dependency on the workspace event bus
// so this package does not import it, and so a build with no bus simply has no
// publisher instead of a nil check at every call site.
type PlanEventPublisher interface {
	PublishPlanEvent(ctx context.Context, event PlanEvent)
}

// PlanEvent is one lifecycle notification.
//
// The field list is the redaction: there is no free-text field, so there is
// nowhere for an objective or a step description to be put by a future caller
// who did not read this comment (FR-173).
type PlanEvent struct {
	Type        PlanEventType `json:"type"`
	PlanID      string        `json:"plan_id"`
	WorkspaceID string        `json:"studio_id"`
	// Version, TaskID, and RunID reference the specific record this event is
	// about, when it is about one.
	Version int    `json:"plan_version,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
	RunID   string `json:"run_id,omitempty"`
	// Status is the Plan's status after the event.
	Status Status `json:"status,omitempty"`
	// TaskCount reports how much work an event concerns, so a subscriber can
	// show "3 tasks created" without reading any of them.
	TaskCount int `json:"task_count,omitempty"`
}

// PlanEventType names a lifecycle moment.
type PlanEventType string

const (
	PlanEventCreated      PlanEventType = "plan.created"
	PlanEventReviewed     PlanEventType = "plan.review_requested"
	PlanEventApproved     PlanEventType = "plan.approved"
	PlanEventMaterialized PlanEventType = "plan.materialized"
	PlanEventStarted      PlanEventType = "plan.execution_started"
	PlanEventPaused       PlanEventType = "plan.paused"
	PlanEventResumed      PlanEventType = "plan.resumed"
	PlanEventCompleted    PlanEventType = "plan.completed"
	PlanEventFailed       PlanEventType = "plan.failed"
	PlanEventCancelled    PlanEventType = "plan.cancelled"
	PlanEventArchived     PlanEventType = "plan.archived"
)

// SetEventPublisher attaches lifecycle event publishing.
func (s *Service) SetEventPublisher(publisher PlanEventPublisher) {
	s.events = publisher
}

// publishEvent emits one lifecycle event, if anything is listening.
//
// Publishing never fails a lifecycle operation. A subscriber that is slow or
// broken must not be able to stop a Plan from being approved; the event is a
// notification about something that already happened.
func (s *Service) publishEvent(ctx context.Context, event PlanEvent) {
	if s == nil || s.events == nil {
		return
	}
	s.events.PublishPlanEvent(ctx, event)
}

// setStatus writes a validated status change and announces it.
//
// Several lifecycle paths write status directly rather than going through
// Transition — requesting review and marking approved both have extra work to
// do around the write. Routing them through here is what keeps the announcement
// attached to the write instead of to one caller who remembered.
func (s *Service) setStatus(ctx context.Context, workspaceID, planID string, to Status, change Activity) error {
	if err := s.store.SetPlanStatus(ctx, workspaceID, planID, to, change); err != nil {
		return err
	}
	if kind, announced := planEventFor(to); announced {
		s.publishEvent(ctx, PlanEvent{
			Type:        kind,
			PlanID:      planID,
			WorkspaceID: workspaceID,
			Status:      to,
			Version:     change.Version,
			TaskID:      change.TaskID,
			RunID:       change.RunID,
		})
	}
	return nil
}

// planEventFor maps a status onto the event that announces reaching it.
//
// Statuses with no event return false rather than a zero value, so a new status
// added later is silently unannounced rather than emitting an empty type that
// subscribers would have to guess at.
func planEventFor(status Status) (PlanEventType, bool) {
	switch status {
	case StatusInReview:
		return PlanEventReviewed, true
	case StatusApproved:
		return PlanEventApproved, true
	case StatusExecuting:
		return PlanEventStarted, true
	case StatusPaused:
		return PlanEventPaused, true
	case StatusCompleted:
		return PlanEventCompleted, true
	case StatusFailed:
		return PlanEventFailed, true
	case StatusCancelled:
		return PlanEventCancelled, true
	default:
		return "", false
	}
}

// LogLifecycle writes one operational log line for a Plan stage.
//
// It logs the stage and the stable IDs, never the prompt, the objective, or any
// step text (FR-174). A log line is for answering "did this happen, to what,
// and when" — the content is one API call away for anyone entitled to it.
func LogLifecycle(logger *log.Logger, event PlanEvent) {
	line := "workspaceplan " + string(event.Type) +
		" plan=" + event.PlanID +
		" studio=" + event.WorkspaceID
	if event.Status != "" {
		line += " status=" + string(event.Status)
	}
	if event.Version > 0 {
		line += " version=" + strconv.Itoa(event.Version)
	}
	if event.TaskID != "" {
		line += " task=" + event.TaskID
	}
	if event.RunID != "" {
		line += " run=" + event.RunID
	}
	if event.TaskCount > 0 {
		line += " tasks=" + strconv.Itoa(event.TaskCount)
	}

	if logger != nil {
		logger.Print(line)
		return
	}
	log.Print(line)
}
