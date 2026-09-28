package workspacecontinuity

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
)

// SourceCollection is supplied only by the trusted installation composition,
// never by a portable file. All collectors must share this SQL read view and
// enumerate the complete canonical owner, including deletions and empty states.
// Fingerprints describe canonical files, not checkpoint objects.
type SourceCollection func(context.Context, Queryer, *Spool) ([]Fingerprint, []string, error)

// PreparationRun is one operation-owned attempt, not an automatic background
// worker or a promise that all domain collectors have been installed. A server
// must acquire its reset/write permit, supply its complete fixed collectors,
// validate physical owned-file inventory, and schedule retries before exposing
// Ready. No production caller is installed until those lifecycles are complete.
type PreparationRun struct {
	Local         *LocalStore
	WorkspaceID   string
	Directory     string
	SpoolParent   string
	Acquire       func(context.Context) (func(), error)
	Collect       SourceCollection
	ValidateFiles func(context.Context, string, []Fingerprint) error
}

// PrepareOnce publishes a generation only for a completely implemented owner:
// an unsupported/unavailable domain never becomes Ready. SQL view, physical
// fingerprint checks, dirty sequence, reset ownership and final acknowledgement
// belong to one attempt. A crash between publication and acknowledgement leaves
// the old status stale, not an invented success; the next attempt can retry.
func (run PreparationRun) PrepareOnce(ctx context.Context) (Inspection, error) {
	var result Inspection
	if run.Local == nil || !ValidID(run.WorkspaceID) || run.Directory == "" || run.SpoolParent == "" || run.Acquire == nil || run.Collect == nil || run.ValidateFiles == nil {
		return result, ErrInvalid
	}
	release, err := run.Acquire(ctx)
	if err != nil {
		return result, err
	}
	if release == nil {
		return result, ErrInvalid
	}
	defer release()
	preparation, err := run.Local.Preparation(ctx, run.WorkspaceID)
	if err != nil {
		return result, err
	}
	if !preparation.Admitted || preparation.Sequence < 1 || preparation.PendingMutation {
		return result, ErrConflict
	}
	// #nosec G115 -- SQLite sequence is a positive signed int64 after the check above; it fits in uint64.
	failureSequence := uint64(preparation.Sequence)
	fail := func(cause error) (Inspection, error) {
		// Cancellation of the requesting handler must not hide a durable failure.
		// A concurrent canonical edit may advance sequence; in that case its dirty
		// row already supersedes this attempt and the failed CAS is intentional.
		failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		recordErr := run.Local.RecordPreparationFailure(failureCtx, run.WorkspaceID, failureSequence, preparationFailureReason(cause))
		if recordErr != nil && !errors.Is(recordErr, ErrConflict) {
			return Inspection{}, errors.Join(cause, recordErr)
		}
		return Inspection{}, cause
	}
	// Publish only through the opened workspace handle. If an external rename
	// detaches its path, no writes may be redirected to the replacement folder.
	// #nosec G703 -- Directory is the admitted local source owner supplied by the server, not a portable manifest path.
	original, err := os.Lstat(run.Directory)
	if err != nil || !original.IsDir() || original.Mode()&os.ModeSymlink != 0 {
		return fail(ErrUnsafe)
	}
	pinned, err := os.OpenRoot(run.Directory)
	if err != nil {
		return fail(ErrUnsafe)
	}
	defer func() { _ = pinned.Close() }()
	checkRoot := func() error {
		opened, openedErr := pinned.Stat(".")
		// #nosec G703 -- the same admitted installation path must still resolve to the pinned workspace directory.
		current, currentErr := os.Lstat(run.Directory)
		if openedErr != nil || currentErr != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(original, opened) || !os.SameFile(original, current) {
			return ErrChanged
		}
		return nil
	}
	if err := checkRoot(); err != nil {
		return fail(err)
	}
	spool, err := NewSpool(run.SpoolParent, run.WorkspaceID)
	if err != nil {
		return fail(err) // disk full/read-only source must not leave an old Ready label
	}
	defer func() { _ = spool.Close() }()
	var files []Fingerprint
	var children []string
	sequence, err := run.Local.ReadSnapshot(ctx, run.WorkspaceID, func(q Queryer, _ uint64) error {
		collected, physical, collectErr := run.Collect(ctx, q, spool)
		if collectErr != nil {
			return collectErr
		}
		files = collected
		// A parent names its immediate physical children; each child publishes
		// its own generation. Review binds every member separately, so a stale
		// or missing child makes the copied tree incomplete, never this parent.
		children = slices.Clone(physical)
		slices.Sort(children)
		return nil
	})
	if err != nil {
		return fail(err)
	}
	components, objects, err := spool.Seal(ctx)
	if err != nil {
		return fail(err)
	}
	for _, component := range components {
		if component.Availability != Present && component.Availability != Empty {
			reason := component.Domain + "_" + component.Reason
			if component.Reason == "" || len(reason) > 64 {
				reason = component.Domain + "_unavailable"
			}
			return fail(WithReason(reason, ErrIncomplete))
		}
	}
	if err := checkRoot(); err != nil {
		return fail(err)
	}
	if err := run.ValidateFiles(ctx, run.Directory, files); err != nil {
		return fail(err)
	}
	if err := checkRoot(); err != nil {
		return fail(err)
	}
	if children == nil {
		children = []string{}
	}
	manifest := Manifest{Version: Version, WorkspaceID: run.WorkspaceID, Generation: uuid.NewString(),
		CheckpointAt: time.Now().UTC(), SourceRevision: sequence, Children: children, Files: files, Components: components}
	manifest.SourceFingerprint = FilesDigest(files)
	if err := manifest.Validate(); err != nil {
		return fail(err)
	}
	fence := func(ctx context.Context) error {
		if err := checkRoot(); err != nil {
			return err
		}
		if err := run.Local.SnapshotFence(run.WorkspaceID, sequence)(ctx); err != nil {
			return err
		}
		if err := run.ValidateFiles(ctx, run.Directory, files); err != nil {
			return err
		}
		return checkRoot()
	}
	if err := PublishPinned(ctx, pinned, manifest, objects, fence); err != nil {
		return fail(err)
	}
	result, err = Inspect(ctx, run.Directory)
	if err != nil || !reflect.DeepEqual(result.Manifest, manifest) || checkRoot() != nil {
		return fail(ErrChanged)
	}
	if err := fence(ctx); err != nil {
		return fail(err)
	}
	if err := spool.Close(); err != nil {
		return fail(err) // cleanup failures cannot leave private objects unreported as Ready
	}
	if err := run.Local.Acknowledge(ctx, run.WorkspaceID, sequence, result.Pointer.Generation, manifest.CheckpointAt); err != nil {
		return fail(err)
	}
	return result, nil
}

// ReasonError labels a preparation failure with a stable, content-free reason
// code (lower-case letters, digits and underscores) for status and the UI.
type ReasonError struct {
	Reason string
	Err    error
}

func (e *ReasonError) Error() string { return e.Reason + ": " + e.Err.Error() }
func (e *ReasonError) Unwrap() error { return e.Err }

// WithReason wraps err with a status reason. A nil err stays nil.
func WithReason(reason string, err error) error {
	if err == nil {
		return nil
	}
	return &ReasonError{Reason: reason, Err: err}
}

func preparationFailureReason(err error) string {
	var labeled *ReasonError
	if errors.As(err, &labeled) && validLabel(labeled.Reason) {
		return labeled.Reason
	}
	switch {
	case errors.Is(err, ErrIncomplete):
		return "incomplete_coverage"
	case errors.Is(err, ErrMutationPending):
		return "file_mutation_pending"
	case errors.Is(err, ErrChanged):
		return "source_changed"
	case errors.Is(err, ErrLimit):
		return "limit_exceeded"
	default:
		return "preparation_failed"
	}
}
