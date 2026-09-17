package mapactivity

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/economy"
	"github.com/johnjallday/ori-agent/internal/featureflags"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	// ParcelMaxAge is how long an unopened parcel waits before the daily sweep
	// marks it opened (FR41).
	ParcelMaxAge = 14 * 24 * time.Hour

	defaultParcelSweepInterval = 24 * time.Hour

	parcelTitleMax         = 160
	parcelSummaryMax       = 280
	parcelFailureReasonMax = 1000
)

// TaskFacts is what a parcel needs to know about the task a run belonged to.
type TaskFacts struct {
	Title     string
	Summary   string
	StartedAt *time.Time
}

// TaskFactsLookup reads one task's facts. The server implements it over the
// workspace store; it is only called once per finished run.
type TaskFactsLookup interface {
	TaskFacts(workspaceID, taskID string) (TaskFacts, bool)
}

// TaskFactsFrom builds a parcel's facts from a task, with the SAME title and
// summary the harvest popover shows for a Farm, so the two never read
// differently (FR36).
func TaskFactsFrom(task *workspace.Task) TaskFacts {
	if task == nil {
		return TaskFacts{}
	}
	return TaskFacts{
		Title:     economy.TaskDisplayName(task),
		Summary:   economy.LastExecutionSummary(task),
		StartedAt: task.StartedAt,
	}
}

// ParcelOptions wires parcels into the tracker.
type ParcelOptions struct {
	Store ParcelStore
	Tasks TaskFactsLookup
	// EconomyEnabled reports whether the City Economy is live. A Farm run is
	// the economy's Harvest while it is, and gets no parcel of its own (FR35).
	EconomyEnabled func() bool
	// WorkspaceIDs lists the workspaces that still exist, for the sweep that
	// clears parcels of deleted ones. Nil skips that part of the sweep.
	WorkspaceIDs func() ([]string, error)
}

// ParcelEvent is one `parcel` stream message: a new parcel, or — with Opened —
// one that is no longer waiting, because it was opened, swept, or deleted.
type ParcelEvent struct {
	ParcelSummary
	Opened bool `json:"opened,omitempty"`
}

// ParcelsWired reports whether the tracker has a parcel store.
func (t *Tracker) ParcelsWired() bool {
	return t != nil && t.parcels.Store != nil
}

// SetParcels turns on parcel creation. Call it before Start.
func (t *Tracker) SetParcels(options ParcelOptions) {
	if t == nil {
		return
	}
	t.parcels = options
}

// WithParcelSweepInterval overrides how often the parcel sweep runs (tests).
func WithParcelSweepInterval(d time.Duration) Option {
	return func(t *Tracker) {
		if d > 0 {
			t.parcelSweepInterval = d
		}
	}
}

func cut(value string, limit int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

// createTaskParcel writes the parcel for a finished task run, when the run
// earns one (FR35). It returns the parcel's id and, only when it was newly
// written, its pile row.
func (t *Tracker) createTaskParcel(ev workspace.Event, workspaceID, taskID, agent string, at time.Time) (string, *ParcelSummary) {
	store := t.parcels.Store
	if store == nil || !featureflags.MapShowEnabled() {
		return "", nil
	}
	outcome := taskOutcomeFor(ev.Type)
	if outcome == OutcomeNone {
		return "", nil
	}
	// A user accepting a ticket by hand also publishes task.completed. No run
	// finished; the user is looking at the task already.
	if accepted, _ := ev.Data["accepted"].(bool); accepted {
		return "", nil
	}
	if scheduled, _ := ev.Data["scheduled"].(bool); scheduled && t.parcels.EconomyEnabled != nil && t.parcels.EconomyEnabled() {
		return "", nil
	}

	facts := TaskFacts{}
	if t.parcels.Tasks != nil {
		facts, _ = t.parcels.Tasks.TaskFacts(workspaceID, taskID)
	}
	parcel := Parcel{
		WorkspaceID: workspaceID,
		Kind:        KindTask,
		RefID:       taskID,
		RunKey:      economy.RunKey(ev, t.now()),
		AgentName:   agent,
		Title:       cut(facts.Title, parcelTitleMax),
		Outcome:     outcome,
		StartedAt:   facts.StartedAt,
		ProducedAt:  at,
		XP:          xpReportFrom(ev.Data),
	}
	if outcome == OutcomeSucceeded || outcome == OutcomePartial {
		parcel.Summary = cut(facts.Summary, parcelSummaryMax)
	} else {
		// Stored for the card, never streamed (FR61, FR62).
		parcel.FailureReason = cut(stringField(ev.Data, "error"), parcelFailureReasonMax)
	}

	stored, created, err := store.Create(context.Background(), parcel)
	if err != nil {
		logger.Warn("Could not create a result parcel", logger.Fields{"workspace_id": workspaceID, "task_id": taskID, "error": err})
		return "", nil
	}
	if !created {
		return stored.ID, nil
	}
	row := stored.Row()
	return stored.ID, &row
}

// xpReportFrom reads the executor's award fields off a task.completed event.
func xpReportFrom(data map[string]any) XPReport {
	return XPReport{
		Awarded:        int64Field(data, "xp_awarded"),
		LevelBefore:    int(int64Field(data, "level_before")),
		LevelAfter:     int(int64Field(data, "level_after")),
		ProgressBefore: floatField(data, "progress_before"),
		ProgressAfter:  floatField(data, "progress_after"),
		StageBefore:    stringField(data, "stage_before"),
		StageAfter:     stringField(data, "stage_after"),
	}
}

func int64Field(data map[string]any, key string) int64 {
	switch v := data[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

func floatField(data map[string]any, key string) float64 {
	switch v := data[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

// unopenedParcelRows is the snapshot's parcel list. A read failure shows no
// parcels rather than failing the whole snapshot.
func (t *Tracker) unopenedParcelRows() []ParcelSummary {
	rows := []ParcelSummary{}
	if t.parcels.Store == nil || !featureflags.MapShowEnabled() {
		return rows
	}
	parcels, err := t.parcels.Store.ListUnopened(context.Background())
	if err != nil {
		logger.Warn("Could not list result parcels", logger.Fields{"error": err})
		return rows
	}
	for _, parcel := range parcels {
		rows = append(rows, parcel.Row())
	}
	return rows
}

func (t *Tracker) broadcastRemoved(parcels []Parcel) {
	if len(parcels) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	for _, parcel := range parcels {
		t.broadcastLocked(StreamMessage{Name: "parcel", Payload: ParcelEvent{ParcelSummary: parcel.Row(), Opened: true}})
	}
}

// OpenParcel marks one parcel opened and tells every open map it is gone.
// Opening an already-opened parcel returns it again and changes nothing (FR37).
func (t *Tracker) OpenParcel(ctx context.Context, id string) (Parcel, error) {
	if t == nil || t.parcels.Store == nil {
		return Parcel{}, ErrParcelStoreUnavailable
	}
	parcel, err := t.parcels.Store.Open(ctx, id, t.now())
	if err != nil {
		return Parcel{}, err
	}
	t.broadcastRemoved([]Parcel{parcel})
	return parcel, nil
}

// OpenParcelsByRef opens every waiting parcel for one thing the user just saw
// somewhere else — a task result, the Daily Brief, the File Janitor console
// (FR40).
func (t *Tracker) OpenParcelsByRef(ctx context.Context, kind Kind, workspaceID, refID string) ([]Parcel, error) {
	if t == nil || t.parcels.Store == nil {
		return nil, ErrParcelStoreUnavailable
	}
	opened, err := t.parcels.Store.OpenByRef(ctx, kind, workspaceID, refID, t.now())
	if err != nil {
		return nil, err
	}
	t.broadcastRemoved(opened)
	return opened, nil
}

func (t *Tracker) unopenedMatching(match func(Parcel) bool) []Parcel {
	parcels, err := t.parcels.Store.ListUnopened(context.Background())
	if err != nil {
		return nil
	}
	var matched []Parcel
	for _, parcel := range parcels {
		if match(parcel) {
			matched = append(matched, parcel)
		}
	}
	return matched
}

// deleteTaskParcels removes a deleted task's parcels (FR41).
func (t *Tracker) deleteTaskParcels(ev workspace.Event) {
	taskID := stringField(ev.Data, "task_id")
	if t.parcels.Store == nil || ev.WorkspaceID == "" || taskID == "" {
		return
	}
	waiting := t.unopenedMatching(func(p Parcel) bool {
		return p.Kind == KindTask && p.WorkspaceID == ev.WorkspaceID && p.RefID == taskID
	})
	if _, err := t.parcels.Store.DeleteForTask(context.Background(), ev.WorkspaceID, taskID); err != nil {
		logger.Warn("Could not delete a deleted task's parcels", logger.Fields{"task_id": taskID, "error": err})
		return
	}
	t.broadcastRemoved(waiting)
}

// SweepParcels marks parcels older than ParcelMaxAge opened, and deletes the
// parcels of workspaces that no longer exist (FR41). Nothing on the bus says a
// workspace was deleted, so this is where that cleanup happens.
func (t *Tracker) SweepParcels(ctx context.Context) {
	if t == nil || t.parcels.Store == nil {
		return
	}
	now := t.now()
	cutoff := now.Add(-ParcelMaxAge)
	stale := t.unopenedMatching(func(p Parcel) bool { return p.ProducedAt.Before(cutoff) })
	if _, err := t.parcels.Store.SweepUnopenedOlderThan(ctx, cutoff, now); err != nil {
		logger.Warn("Could not sweep old parcels", logger.Fields{"error": err})
	} else {
		t.broadcastRemoved(stale)
	}

	if t.parcels.WorkspaceIDs == nil {
		return
	}
	ids, err := t.parcels.WorkspaceIDs()
	if err != nil {
		return
	}
	exists := make(map[string]bool, len(ids))
	for _, id := range ids {
		exists[id] = true
	}
	orphaned := t.unopenedMatching(func(p Parcel) bool { return !exists[p.WorkspaceID] })
	gone := map[string]bool{}
	for _, parcel := range orphaned {
		gone[parcel.WorkspaceID] = true
	}
	for workspaceID := range gone {
		if _, err := t.parcels.Store.DeleteForWorkspace(ctx, workspaceID); err != nil {
			logger.Warn("Could not delete a deleted workspace's parcels", logger.Fields{"workspace_id": workspaceID, "error": err})
		}
	}
	t.broadcastRemoved(orphaned)
}

func (t *Tracker) runParcelSweeps(stopCh <-chan struct{}) {
	t.SweepParcels(context.Background())
	ticker := time.NewTicker(t.parcelSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			t.SweepParcels(context.Background())
		}
	}
}
