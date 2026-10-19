package externalmemory

import (
	"context"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// Reasons a sync runs, recorded in its result and status.
const (
	ReasonStartup  = "startup"
	ReasonSchedule = "schedule"
	ReasonManual   = "manual"
	ReasonEnabled  = "enabled"
	ReasonPaste    = "paste"
)

// SyncInterval is the daily cadence (decision 3A): the next scheduled sync is
// due this long after the last completed one.
const SyncInterval = 24 * time.Hour

// retryInterval spaces scheduled attempts when a sync could not complete.
const retryInterval = time.Hour

// removedRetention is how long a removed entry that was never promoted stays in
// the mirror before a sync drops it. A promoted entry is kept for good so its
// link to the Personal HQ item survives.
const removedRetention = 30 * 24 * time.Hour

// backgroundSyncTimeout bounds a sync nobody is waiting on.
const backgroundSyncTimeout = 2 * time.Minute

// RunStatus describes the sync in flight, if any.
type RunStatus struct {
	Running   bool       `json:"running"`
	Reason    string     `json:"reason,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
}

// SourceResult is one source's outcome in a sync.
type SourceResult struct {
	Source   string       `json:"source"`
	Result   string       `json:"result"`
	Error    string       `json:"error,omitempty"`
	Counts   SyncCounts   `json:"counts"`
	Problems []ParseError `json:"problems,omitempty"`
	// StoreFull reports that new entries were left out at MaxStoreBytes.
	StoreFull bool      `json:"store_full,omitempty"`
	Finished  time.Time `json:"finished"`
}

// RunResult is one sync's outcome.
type RunResult struct {
	Reason     string         `json:"reason"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt time.Time      `json:"finished_at"`
	Sources    []SourceResult `json:"sources"`
}

// Runner syncs the enabled sources into the store, one sync at a time, and
// schedules the daily sync. The HTTP trigger, the daily timer, and enabling a
// source all go through the same run.
type Runner struct {
	store    *Store
	registry *Registry
	home     string
	now      func() time.Time
	after    func(time.Duration) <-chan time.Time

	runMu      sync.Mutex
	queued     atomic.Bool
	background sync.WaitGroup

	statusMu sync.Mutex
	status   RunStatus

	hooksMu   sync.Mutex
	afterSync []func(RunResult)

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
}

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// WithClock replaces the wall clock and timer, for tests.
func WithClock(now func() time.Time, after func(time.Duration) <-chan time.Time) RunnerOption {
	return func(r *Runner) {
		if now != nil {
			r.now = now
		}
		if after != nil {
			r.after = after
		}
	}
}

// WithHome sets the home folder a leading ~ in an exclusion expands to.
func WithHome(home string) RunnerOption {
	return func(r *Runner) { r.home = home }
}

// NewRunner builds a runner over a store and the registered sources.
func NewRunner(store *Store, registry *Registry, opts ...RunnerOption) *Runner {
	r := &Runner{store: store, registry: registry, now: time.Now, after: time.After}
	if home, err := os.UserHomeDir(); err == nil {
		r.home = home
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Store returns the mirror the runner writes.
func (r *Runner) Store() *Store { return r.store }

// Registry returns the registered sources.
func (r *Runner) Registry() *Registry { return r.registry }

// Home returns the folder a leading ~ in an exclusion expands to.
func (r *Runner) Home() string { return r.home }

// Status reports the sync in flight.
func (r *Runner) Status() RunStatus {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	return r.status
}

func (r *Runner) setStatus(status RunStatus) {
	r.statusMu.Lock()
	r.status = status
	r.statusMu.Unlock()
}

// OnSynced registers fn to run after every completed sync, while the sync lock
// is still held. fn must not start another sync.
func (r *Runner) OnSynced(fn func(RunResult)) {
	if fn == nil {
		return
	}
	r.hooksMu.Lock()
	r.afterSync = append(r.afterSync, fn)
	r.hooksMu.Unlock()
}

// Run syncs every enabled source, waiting for a sync already in flight to
// finish first.
func (r *Runner) Run(ctx context.Context, reason string) (RunResult, error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	return r.runLocked(ctx, reason, nil)
}

// RunSource syncs one enabled source (the account paste), leaving the daily
// timer as it is.
func (r *Runner) RunSource(ctx context.Context, source, reason string) (RunResult, error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	return r.runLocked(ctx, reason, []string{source})
}

// TryRun syncs every enabled source unless a sync is already in flight, in
// which case it returns started=false at once.
func (r *Runner) TryRun(ctx context.Context, reason string) (RunResult, bool, error) {
	if !r.runMu.TryLock() {
		return RunResult{}, false, nil
	}
	defer r.runMu.Unlock()
	result, err := r.runLocked(ctx, reason, nil)
	return result, true, err
}

// RunInBackground queues one sync without waiting for it. Requests made while
// one is already queued share it; a request made during a running sync queues
// another, because the running one may have read the switches before the
// change.
func (r *Runner) RunInBackground(reason string) {
	if !r.queued.CompareAndSwap(false, true) {
		return
	}
	r.background.Go(func() {
		r.runMu.Lock()
		r.queued.Store(false)
		defer r.runMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), backgroundSyncTimeout)
		defer cancel()
		if _, err := r.runLocked(ctx, reason, nil); err != nil {
			logger.Warn("External memory sync failed", logger.Fields{"reason": reason, "error": err})
		}
	})
}

// runLocked is the sync. The caller holds runMu.
func (r *Runner) runLocked(ctx context.Context, reason string, only []string) (RunResult, error) {
	started := r.now()
	r.setStatus(RunStatus{Running: true, Reason: reason, StartedAt: &started})
	defer r.setStatus(RunStatus{})

	result := RunResult{Reason: reason, StartedAt: started}
	doc := r.store.LoadSources()
	totalBytes := r.store.TotalBodyBytes()
	for _, source := range r.registry.All() {
		id := source.ID()
		if only != nil && !slices.Contains(only, id) {
			continue
		}
		state := doc.Source(id)
		if !state.Enabled {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Sources = append(result.Sources, r.syncSource(ctx, source, state, &totalBytes))
	}
	result.FinishedAt = r.now()

	if _, err := r.store.UpdateSources(func(d *SourcesDocument) error {
		for _, sr := range result.Sources {
			// Re-read the state so a switch flipped during the sync survives.
			state := d.Source(sr.Source)
			finished := sr.Finished
			state.LastSyncStarted = &started
			state.LastSyncFinished = &finished
			state.LastResult = sr.Result
			state.LastError = sr.Error
			state.SkippedFiles = len(sr.Problems)
			state.Counts = sr.Counts
			state.StoreFull = sr.StoreFull
			d.Sources[sr.Source] = state
		}
		if only == nil {
			finished := result.FinishedAt
			d.LastCompletedSync = &finished
		}
		return nil
	}); err != nil {
		return result, err
	}

	r.hooksMu.Lock()
	hooks := slices.Clone(r.afterSync)
	r.hooksMu.Unlock()
	for _, hook := range hooks {
		hook(result)
	}
	return result, nil
}

// syncSource reads one source and folds it into the mirror.
func (r *Runner) syncSource(ctx context.Context, source Source, state SourceState, totalBytes *int) SourceResult {
	id := source.ID()
	sr := SourceResult{Source: id}
	prev := r.store.LoadEntries(id)
	finish := func() SourceResult {
		sr.Finished = r.now()
		return sr
	}

	var next []Entry
	if source.Available() {
		entries, problems, err := source.Read(ctx)
		if err != nil {
			// The source as a whole failed: keep the mirror as it was.
			sr.Result, sr.Error = ResultError, err.Error()
			sr.Counts = countEntries(prev)
			logger.Warn("External memory source failed", logger.Fields{"source": id, "error": err})
			return finish()
		}
		for _, problem := range problems {
			logger.Warn("External memory file skipped", logger.Fields{"source": id, "path": problem.Path, "reason": problem.Reason})
		}
		sr.Problems = problems
		prefixes := normalizeExclusions(state.Exclusions, r.home)
		for _, entry := range entries {
			if !excluded(entry.Folder, prefixes) {
				next = append(next, entry)
			}
		}
		sr.Result = ResultOK
		if len(problems) > 0 {
			sr.Result = ResultPartial
		}
	} else {
		// Nothing is there any more: every mirrored entry is no longer
		// present. They are marked removed, not deleted, and come back on the
		// sync after the folder reappears.
		sr.Result = ResultUnavailable
	}

	merged, delta, full := mergeEntries(prev, next, r.now(), totalBytes)
	if delta.Added+delta.Updated+delta.Removed+delta.pruned > 0 {
		if err := r.store.SaveEntries(id, merged); err != nil {
			sr.Result, sr.Error = ResultError, err.Error()
			sr.Counts = countEntries(prev)
			logger.Warn("External memory mirror write failed", logger.Fields{"source": id, "error": err})
			return finish()
		}
	}
	sr.Counts = countEntries(merged)
	sr.Counts.Added, sr.Counts.Updated, sr.Counts.Removed = delta.Added, delta.Updated, delta.Removed
	sr.StoreFull = full
	if full {
		logger.Warn("External memory store is full; new entries were not added", logger.Fields{"source": id, "cap_bytes": MaxStoreBytes})
	}
	return finish()
}

type mergeDelta struct {
	Added, Updated, Removed int
	pruned                  int
}

// mergeEntries diffs a fresh read against the stored mirror (FR13). An
// unchanged entry is kept exactly as stored, so a re-sync with no file
// changes touches nothing. A changed entry is replaced but keeps its promoted
// link. A new entry is added unless the store is full. A stored entry missing
// from the read is marked removed; a removed entry that was never promoted is
// dropped after removedRetention.
func mergeEntries(prev, next []Entry, now time.Time, totalBytes *int) ([]Entry, mergeDelta, bool) {
	prevByID := make(map[string]Entry, len(prev))
	for _, entry := range prev {
		prevByID[entry.ID] = entry
	}
	var delta mergeDelta
	full := false
	seen := make(map[string]bool, len(next))
	out := make([]Entry, 0, len(next)+len(prev))
	for _, entry := range next {
		if seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		old, existed := prevByID[entry.ID]
		switch {
		case existed && old.Active() && old.ContentHash == entry.ContentHash:
			out = append(out, old)
		case existed:
			entry.SyncedAt = now
			entry.PromotedItemID = old.PromotedItemID
			*totalBytes += len(entry.Body) - len(old.Body)
			out = append(out, entry)
			delta.Updated++
		case *totalBytes+len(entry.Body) > MaxStoreBytes:
			full = true
		default:
			entry.SyncedAt = now
			*totalBytes += len(entry.Body)
			out = append(out, entry)
			delta.Added++
		}
	}
	for _, old := range prev {
		if seen[old.ID] {
			continue
		}
		if old.Active() {
			removedAt := now
			old.State, old.RemovedAt = StateRemoved, &removedAt
			out = append(out, old)
			delta.Removed++
			continue
		}
		if old.PromotedItemID == "" && old.RemovedAt != nil && now.Sub(*old.RemovedAt) > removedRetention {
			*totalBytes -= len(old.Body)
			delta.pruned++
			continue
		}
		out = append(out, old)
	}
	return out, delta, full
}

// countEntries counts the active entries and the distinct folders they apply
// to. Entries about the user in general (no folder) count as entries only.
func countEntries(entries []Entry) SyncCounts {
	folders := map[string]bool{}
	counts := SyncCounts{}
	for _, entry := range entries {
		if !entry.Active() {
			continue
		}
		counts.Entries++
		if entry.Folder != "" {
			folders[entry.Folder] = true
		}
	}
	counts.Folders = len(folders)
	return counts
}

// NextDue is when the next scheduled sync is due: SyncInterval after the last
// completed sync, or now when there has never been one.
func (r *Runner) NextDue() time.Time {
	doc := r.store.LoadSources()
	if doc.LastCompletedSync == nil {
		return r.now()
	}
	return doc.LastCompletedSync.Add(SyncInterval)
}

// Start begins the schedule (FR14) in the background and returns at once. The
// first sync runs now when one is due; otherwise the timer is armed for the
// remainder of the day since the last completed sync, so a restart does not
// re-read everything. Start after the server is serving; it never blocks.
func (r *Runner) Start() {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan struct{})
	go r.loop(ctx, r.done)
}

// Stop ends the schedule, cancels a scheduled sync in flight, and waits for the
// loop and any queued background sync to finish. Start may be called again
// afterwards.
func (r *Runner) Stop() {
	r.lifecycleMu.Lock()
	cancel, done := r.cancel, r.done
	r.cancel, r.done = nil, nil
	r.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	r.background.Wait()
}

// WaitIdle returns once no sync is queued or running.
func (r *Runner) WaitIdle() {
	r.background.Wait()
	// Taking the sync lock waits out a sync started by Run or the schedule.
	r.runMu.Lock()
	defer r.runMu.Unlock()
}

func (r *Runner) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	reason := ReasonStartup
	for {
		if wait := r.NextDue().Sub(r.now()); wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-r.after(wait):
			}
			reason = ReasonSchedule
		}
		_, err := r.Run(ctx, reason)
		if ctx.Err() != nil {
			return
		}
		reason = ReasonSchedule
		if err != nil || !r.NextDue().After(r.now()) {
			logger.Warn("Scheduled external memory sync did not complete; retrying later", logger.Fields{"error": err, "retry_in": retryInterval.String()})
			select {
			case <-ctx.Done():
				return
			case <-r.after(retryInterval):
			}
		}
	}
}
