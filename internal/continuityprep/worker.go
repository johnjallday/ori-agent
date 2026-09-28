package continuityprep

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// FolderResolver maps a registered workspace to its current folder.
type FolderResolver interface {
	GetFolderPath(workspaceID string) (string, error)
}

// Worker prepares checkpoints for this installation's own workspaces. It
// considers only workspaces registered in the local database; a folder that
// was copied in, or retained across an app-record reset, is never prepared
// (and so never overwritten with empty history) until a reviewed import
// registers it. Each pass holds one reset work permit, so a reset drains it.
type Worker struct {
	DB          *database.DB
	Local       *workspacecontinuity.LocalStore
	Collector   *Collector
	Folders     FolderResolver
	SpoolParent string
	Gate        *resetstate.WorkGate
	Interval    time.Duration // between passes; 0 means 20s
	Debounce    time.Duration // quiet time after a change; 0 means 5s
	Now         func() time.Time

	mu        sync.Mutex // serializes preparation attempts
	state     sync.Mutex // guards the maps below
	seen      map[string]observation
	preparing map[string]bool
	verified  map[string]string // workspace -> folder signature at its last Ready
	kick      chan struct{}
	stop      chan struct{}
	done      chan struct{}
	started   bool
	passes    int
}

type observation struct {
	sequence   int64
	since      time.Time
	failures   int
	retryAfter time.Time
}

func (w *Worker) init() {
	w.state.Lock()
	defer w.state.Unlock()
	if w.seen == nil {
		w.seen = map[string]observation{}
		w.preparing = map[string]bool{}
		w.verified = map[string]string{}
		w.kick = make(chan struct{}, 1)
	}
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Start runs passes until Stop. It is safe to call once.
func (w *Worker) Start() {
	w.init()
	w.state.Lock()
	if w.started {
		w.state.Unlock()
		return
	}
	w.started = true
	w.stop, w.done = make(chan struct{}), make(chan struct{})
	w.state.Unlock()
	interval := w.Interval
	if interval <= 0 {
		interval = 20 * time.Second
	}
	go func() {
		defer close(w.done)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			<-w.stop
			cancel()
		}()
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-timer.C:
			case <-w.kick:
			}
			w.Pass(ctx)
			timer.Reset(interval)
		}
	}()
}

// Stop ends the loop and waits for an in-flight pass.
func (w *Worker) Stop() {
	w.state.Lock()
	if !w.started {
		w.state.Unlock()
		return
	}
	w.started = false
	stop, done := w.stop, w.done
	w.state.Unlock()
	close(stop)
	<-done
}

// Kick asks for a pass soon, e.g. after a change the database cannot see.
func (w *Worker) Kick() {
	w.init()
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// MarkDirty records a change to a workspace's portable state and wakes the
// worker. Unknown or non-local workspaces are ignored.
func (w *Worker) MarkDirty(ctx context.Context, workspaceID string) {
	if w == nil || w.Local == nil || !workspacecontinuity.ValidID(workspaceID) {
		return
	}
	if err := w.Local.MarkDirty(ctx, workspaceID); err == nil {
		w.forget(workspaceID)
		w.Kick()
	}
}

// MarkSessionDirty resolves a session's owning workspace, if any.
func (w *Worker) MarkSessionDirty(sessionID string) {
	if w == nil || w.DB == nil || !workspacecontinuity.ValidID(sessionID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var owner string
	if err := w.DB.QueryRowContext(ctx, `SELECT COALESCE(workspace_id,'') FROM sessions WHERE id=?`, sessionID).Scan(&owner); err != nil || owner == "" {
		return
	}
	w.MarkDirty(ctx, owner)
}

func (w *Worker) forget(workspaceID string) {
	w.state.Lock()
	delete(w.verified, workspaceID)
	w.state.Unlock()
}

// Pass considers every local workspace once. Exported for tests and for an
// explicit "prepare now" request.
func (w *Worker) Pass(ctx context.Context) {
	w.init()
	if w.DB == nil || w.Local == nil || w.Collector == nil || w.Folders == nil {
		return
	}
	release, err := w.Gate.Enter()
	if err != nil {
		return // a reset is fencing work; the next pass retries
	}
	defer release()
	w.state.Lock()
	w.passes++
	reconcile := w.passes%3 == 1
	w.state.Unlock()
	ids, err := w.localWorkspaces(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		w.consider(ctx, id, reconcile)
	}
}

func (w *Worker) localWorkspaces(ctx context.Context) ([]string, error) {
	rows, err := w.DB.QueryContext(ctx, `SELECT id FROM workspaces WHERE owner_user_id='local' AND deleted_at IS NULL ORDER BY id LIMIT 4096`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, errors.Join(rows.Err(), rows.Close())
}

func (w *Worker) consider(ctx context.Context, id string, reconcile bool) {
	if !workspacecontinuity.ValidID(id) {
		return
	}
	attachment, err := w.Local.Attachment(ctx, id)
	if err != nil {
		return
	}
	if attachment.Version == 0 {
		// An existing native workspace from before continuity: record it as
		// native so it can be prepared. Discovery never reaches here for an
		// unknown copied folder, which has no local database row.
		if folder, err := w.Folders.GetFolderPath(id); err != nil || folder == "" {
			return
		}
		if err := w.Local.RegisterNative(ctx, id); err != nil {
			return
		}
		if attachment, err = w.Local.Attachment(ctx, id); err != nil {
			return
		}
	}
	if !attachment.AllowsPreparation() {
		return
	}
	prep, err := w.Local.Preparation(ctx, id)
	if err != nil {
		return
	}
	if prep.Ready() {
		if reconcile {
			w.reconcileReady(ctx, id, prep)
		}
		return
	}
	now := w.now()
	w.state.Lock()
	seen := w.seen[id]
	if seen.sequence != prep.Sequence {
		seen = observation{sequence: prep.Sequence, since: now}
		w.seen[id] = seen
	}
	debounce := w.Debounce
	if debounce <= 0 {
		debounce = 5 * time.Second
	}
	wait := now.Sub(seen.since) < debounce || now.Before(seen.retryAfter)
	w.state.Unlock()
	if wait {
		return
	}
	if err := w.prepare(ctx, id); err != nil {
		w.state.Lock()
		current := w.seen[id]
		if current.sequence == prep.Sequence {
			current.failures++
			backoff := time.Minute << min(current.failures-1, 5)
			current.retryAfter = now.Add(min(backoff, 30*time.Minute))
			w.seen[id] = current
		}
		w.state.Unlock()
	}
}

// reconcileReady catches edits made outside Ori (or missed while stopped):
// the first check after start verifies every byte against the published
// manifest, later checks compare a cheap stat signature.
func (w *Worker) reconcileReady(ctx context.Context, id string, prep workspacecontinuity.Preparation) {
	folder, err := w.Folders.GetFolderPath(id)
	if err != nil || folder == "" {
		return
	}
	signature, err := workspace.ContinuityFolderSignature(ctx, folder)
	if err != nil {
		w.MarkDirty(ctx, id)
		return
	}
	w.state.Lock()
	known, ok := w.verified[id]
	w.state.Unlock()
	if ok {
		if known != signature {
			w.MarkDirty(ctx, id)
		}
		return
	}
	inspected, err := workspacecontinuity.Inspect(ctx, folder)
	if err == nil && inspected.Pointer.Generation == prep.Generation {
		err = workspace.CheckContinuityFolderCoverage(ctx, folder, inspected.Manifest.Files)
	} else if err == nil {
		err = workspacecontinuity.ErrChanged
	}
	if err == nil {
		var children []string
		children, err = PhysicalChildren(ctx, folder)
		if err == nil && !sameIDs(children, inspected.Manifest.Children) {
			err = workspacecontinuity.ErrChanged
		}
	}
	if err != nil {
		if ctx.Err() == nil {
			w.MarkDirty(ctx, id)
		}
		return
	}
	w.state.Lock()
	w.verified[id] = signature
	w.state.Unlock()
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// PrepareNow runs one attempt for workspaceID immediately (the caller holds
// no permit; this takes one). It returns the resulting status.
func (w *Worker) PrepareNow(ctx context.Context, workspaceID string) (Status, error) {
	w.init()
	release, err := w.Gate.Enter()
	if err != nil {
		return Status{}, err
	}
	defer release()
	attachment, err := w.Local.Attachment(ctx, workspaceID)
	if err != nil {
		return Status{}, err
	}
	if attachment.Version == 0 {
		if err := w.Local.RegisterNative(ctx, workspaceID); err != nil {
			return w.Status(ctx, workspaceID)
		}
	}
	if prep, err := w.Local.Preparation(ctx, workspaceID); err == nil && !prep.Ready() {
		_ = w.prepare(ctx, workspaceID)
	}
	return w.Status(ctx, workspaceID)
}

func (w *Worker) prepare(ctx context.Context, id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	folder, err := w.Folders.GetFolderPath(id)
	if err != nil || folder == "" {
		return workspacecontinuity.ErrIncomplete
	}
	w.state.Lock()
	w.preparing[id] = true
	w.state.Unlock()
	defer func() {
		w.state.Lock()
		delete(w.preparing, id)
		w.state.Unlock()
	}()
	run := workspacecontinuity.PreparationRun{
		Local: w.Local, WorkspaceID: id, Directory: folder, SpoolParent: w.SpoolParent,
		// The pass (or PrepareNow) already holds this attempt's reset permit.
		Acquire: func(context.Context) (func(), error) { return func() {}, nil },
		Collect: func(ctx context.Context, q workspacecontinuity.Queryer, spool *workspacecontinuity.Spool) ([]workspacecontinuity.Fingerprint, []string, error) {
			return w.Collector.Collect(ctx, q, id, folder, spool)
		},
		ValidateFiles: func(ctx context.Context, dir string, files []workspacecontinuity.Fingerprint) error {
			if err := workspacecontinuity.VerifyFingerprints(ctx, dir, files); err != nil {
				return err
			}
			return workspace.CheckContinuityFolderCoverage(ctx, dir, files)
		},
	}
	if _, err := run.PrepareOnce(ctx); err != nil {
		if ctx.Err() == nil {
			// Error text carries field/reason names only, never file content.
			logger.Info("Workspace continuity preparation incomplete", logger.Fields{"workspace_id": id, "reason": reasonOf(err), "cause": err.Error()})
		}
		return err
	}
	if signature, sigErr := workspace.ContinuityFolderSignature(ctx, folder); sigErr == nil {
		w.state.Lock()
		w.verified[id] = signature
		delete(w.seen, id)
		w.state.Unlock()
	}
	return nil
}

func reasonOf(err error) string {
	var labeled *workspacecontinuity.ReasonError
	if errors.As(err, &labeled) {
		return labeled.Reason
	}
	return "preparation_failed"
}

func (w *Worker) isPreparing(id string) bool {
	w.state.Lock()
	defer w.state.Unlock()
	return w.preparing[id]
}
