package mapactivity

import (
	"context"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/featureflags"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Background activities are work that is not a task: a Daily Brief generation
// and an unattended File Janitor scan (tasks/prd-task-run-show.md FR8–FR10).
// They report a start and a finish and nothing in between, so on the map they
// light a building with one line and then deliver — or quietly go dark.

const (
	dailyBriefParcelTitle  = "Daily Brief"
	fileJanitorParcelTitle = "File Janitor"
	// A failed brief's reason stays on the server; the card says only this.
	dailyBriefFailedText = "Your Daily Brief could not be prepared."
	// briefDateLayout is the brief's local date, as the brief service keeps it.
	briefDateLayout = "2006-01-02"
)

// BackgroundActivityID is the stream id of one background run.
func BackgroundActivityID(kind Kind, activityID string) string {
	return string(kind) + ":" + activityID
}

func backgroundKind(ev workspace.Event) (Kind, bool) {
	switch kind := Kind(stringField(ev.Data, "kind")); kind {
	case KindDailyBrief, KindFileJanitor:
		return kind, true
	}
	return "", false
}

func backgroundOutcome(value string) Outcome {
	switch Outcome(value) {
	case OutcomeSucceeded, OutcomePartial, OutcomeFailed:
		return Outcome(value)
	}
	return OutcomeNone
}

func (t *Tracker) handleBackgroundActivity(ev workspace.Event) {
	kind, ok := backgroundKind(ev)
	runID := stringField(ev.Data, "activity_id")
	if !ok || ev.WorkspaceID == "" || runID == "" {
		return
	}
	id := BackgroundActivityID(kind, runID)
	at := t.eventTime(ev)

	if ev.Type == workspace.EventActivityStarted {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.stopped {
			return
		}
		if _, ended := t.finished[id]; ended {
			return
		}
		if t.running[id] == nil {
			t.addRunning(kind, id, ev.WorkspaceID, "", at)
		}
		t.broadcastLocked(StreamMessage{Name: "activity", Payload: ActivityEvent{
			Kind:        kind,
			Phase:       PhaseStarted,
			ActivityID:  id,
			WorkspaceID: ev.WorkspaceID,
			At:          at,
		}})
		return
	}

	outcome := backgroundOutcome(stringField(ev.Data, "outcome"))
	count := int(int64Field(ev.Data, "count"))

	var startedAt *time.Time
	t.mu.Lock()
	if act := t.running[id]; act != nil {
		started := act.StartedAt
		startedAt = &started
	}
	t.mu.Unlock()

	// As for tasks, the parcel is written before the lock is taken.
	parcelID, newParcel := t.createBackgroundParcel(ev, kind, id, outcome, count, startedAt, at)

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	if _, ended := t.finished[id]; ended {
		return
	}
	delete(t.running, id)
	t.finished[id] = at
	message := ActivityEvent{
		Kind:        kind,
		Phase:       PhaseFinished,
		ActivityID:  id,
		WorkspaceID: ev.WorkspaceID,
		Outcome:     outcome,
		ParcelID:    parcelID,
		At:          at,
	}
	if kind == KindFileJanitor && outcome != OutcomeFailed {
		// "Nothing to tidy." is a finish too, so the count travels even at zero.
		message.Count = &count
	}
	t.broadcastLocked(StreamMessage{Name: "activity", Payload: message})
	if newParcel != nil {
		t.broadcastLocked(StreamMessage{Name: "parcel", Payload: ParcelEvent{ParcelSummary: *newParcel}})
	}
}

// createBackgroundParcel writes the parcel for a finished background run, when
// it earns one (FR35): a brief the schedule prepared — not one the user is
// watching generate — and a janitor scan that found something to review.
func (t *Tracker) createBackgroundParcel(ev workspace.Event, kind Kind, id string, outcome Outcome, count int, startedAt *time.Time, at time.Time) (string, *ParcelSummary) {
	store := t.parcels.Store
	if store == nil || !featureflags.MapShowEnabled() || outcome == OutcomeNone {
		return "", nil
	}
	refID := stringField(ev.Data, "ref_id")
	parcel := Parcel{
		WorkspaceID: ev.WorkspaceID,
		Kind:        kind,
		RefID:       refID,
		RunKey:      id,
		Outcome:     outcome,
		StartedAt:   startedAt,
		ProducedAt:  at,
	}
	switch kind {
	case KindDailyBrief:
		if stringField(ev.Data, "trigger") != "scheduled" {
			return "", nil
		}
		if parcel.RefID == "" {
			// A generation that failed before it saved anything has no revision.
			parcel.RefID = id
		}
		parcel.Title = dailyBriefParcelTitle
		if outcome == OutcomeFailed {
			parcel.FailureReason = dailyBriefFailedText
		} else {
			parcel.Summary = briefReadySentence(stringField(ev.Data, "local_date"))
		}
	case KindFileJanitor:
		if outcome != OutcomeSucceeded || count < 1 || refID == "" {
			return "", nil
		}
		parcel.Title = fileJanitorParcelTitle
		parcel.Summary = filesReadySentence(count)
	default:
		return "", nil
	}

	stored, created, err := store.Create(context.Background(), parcel)
	if err != nil {
		logger.Warn("Could not create a result parcel", logger.Fields{"workspace_id": ev.WorkspaceID, "kind": string(kind), "error": err})
		return "", nil
	}
	if !created {
		return stored.ID, nil
	}
	row := stored.Row()
	return stored.ID, &row
}

// briefReadySentence is the card's fixed line for a brief (FR36). The brief's
// own words stay in the brief.
func briefReadySentence(localDate string) string {
	day, err := time.Parse(briefDateLayout, localDate)
	if err != nil {
		return "Your brief is ready."
	}
	return "Your brief for " + day.Weekday().String() + " is ready."
}

// filesReadySentence is the card's fixed line for a janitor scan (FR36).
func filesReadySentence(count int) string {
	if count == 1 {
		return "1 file is ready to review."
	}
	return fmt.Sprintf("%d files are ready to review.", count)
}
