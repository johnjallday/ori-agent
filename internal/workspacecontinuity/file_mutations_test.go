package workspacecontinuity

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

func TestFileMutationFencesCapturePublicationAndAcknowledgement(t *testing.T) {
	db, store := localFixture(t)
	seedCanonicalWorkspace(t, db, "source")
	must(t, store.RegisterNative(t.Context(), "source"))
	before, err := store.Preparation(t.Context(), "source")
	must(t, err)
	must(t, store.Acknowledge(t.Context(), "source", uint64(before.Sequence), uuid.NewString(), time.Now()))
	mutation, err := store.BeginFileMutation(t.Context(), "source", "workspace.json")
	must(t, err)
	pending, err := store.Preparation(t.Context(), "source")
	must(t, err)
	if pending.Ready() || !pending.PendingMutation || pending.MutationFailed || pending.Sequence <= before.Sequence {
		t.Fatal("pending write not visible")
	}
	if _, err := store.ReadSnapshot(t.Context(), "source", func(Queryer, uint64) error { t.Fatal("collected during write"); return nil }); !errors.Is(err, ErrMutationPending) {
		t.Fatal(err)
	}
	if err := store.SnapshotFence("source", uint64(pending.Sequence))(t.Context()); !errors.Is(err, ErrMutationPending) {
		t.Fatal(err)
	}
	if err := store.Acknowledge(t.Context(), "source", uint64(pending.Sequence), uuid.NewString(), time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("pre-write sequence became Ready", err)
	}
	// Even a stale caller carrying a pre-existing acknowledged sequence cannot
	// construct Ready while a barrier exists.
	_, err = db.ExecContext(t.Context(), `UPDATE continuity_dirty SET prepared_sequence=sequence WHERE workspace_id='source'`)
	must(t, err)
	pending, err = store.Preparation(t.Context(), "source")
	must(t, err)
	if pending.Ready() {
		t.Fatal("pending barrier was merely a sequence comparison")
	}
	if _, err := store.BeginFileMutation(t.Context(), "source", "workspace.json"); !errors.Is(err, ErrMutationPending) {
		t.Fatal("overlapping owner superseded a live writer", err)
	}
	must(t, mutation.Complete(t.Context(), true))
	must(t, mutation.Complete(t.Context(), true)) // same handle is idempotent
	if err := mutation.Complete(t.Context(), false); !errors.Is(err, ErrConflict) {
		t.Fatal("outcome changed after completion")
	}
	after, err := store.Preparation(t.Context(), "source")
	must(t, err)
	if after.Ready() || after.PendingMutation || after.Sequence <= pending.Sequence {
		t.Fatal("completion did not require a fresh snapshot")
	}
	must(t, store.Acknowledge(t.Context(), "source", uint64(after.Sequence), uuid.NewString(), time.Now()))
	after, err = store.Preparation(t.Context(), "source")
	must(t, err)
	if !after.Ready() {
		t.Fatal("completed fresh snapshot not ready")
	}
}

func TestFileMutationFailureSurvivesTriggersAndSuccessfulReplacementClearsOnlyItsTarget(t *testing.T) {
	db, store := localFixture(t)
	seedCanonicalWorkspace(t, db, "source")
	must(t, store.RegisterNative(t.Context(), "source"))
	first, err := store.BeginFileMutation(t.Context(), "source", "workspace.json")
	must(t, err)
	other, err := store.BeginFileMutation(t.Context(), "source", "agents/guide/config.json")
	must(t, err)
	must(t, first.Complete(t.Context(), false))
	_, err = db.ExecContext(t.Context(), `UPDATE workspaces SET name='Ordinary SQL edit' WHERE id='source'`)
	must(t, err)
	state, err := store.Preparation(t.Context(), "source")
	must(t, err)
	if !state.MutationFailed || !state.PendingMutation || state.Ready() {
		t.Fatal("trigger erased file failure")
	}
	replacement, err := store.BeginFileMutation(t.Context(), "source", "workspace.json")
	must(t, err)
	must(t, replacement.Complete(t.Context(), true))
	state, err = store.Preparation(t.Context(), "source")
	must(t, err)
	if state.MutationFailed || !state.PendingMutation {
		t.Fatal("successful replacement cleared another writer")
	}
	must(t, other.Complete(t.Context(), true))
}

func TestFileMutationRecoveryRequiresExactBarrierAndCanonicalValidation(t *testing.T) {
	db, store := localFixture(t)
	seedCanonicalWorkspace(t, db, "source")
	must(t, store.RegisterNative(t.Context(), "source"))
	old, err := store.BeginFileMutation(t.Context(), "source", "workspace.json")
	must(t, err)
	barriers, err := store.FileMutationBarriers(t.Context(), "source")
	must(t, err)
	path := db.Path()
	must(t, db.Close())
	reopened, err := database.Open(t.Context(), &database.Config{Path: path})
	must(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	store = NewLocalStore(reopened)
	if _, err := store.ReadSnapshot(t.Context(), "source", func(Queryer, uint64) error { t.Fatal("restart cleared pending write"); return nil }); !errors.Is(err, ErrMutationPending) {
		t.Fatal(err)
	}
	failed := errors.New("synthetic mismatched canonical data")
	if err := store.ReconcileFileMutation(t.Context(), "source", barriers[0], func(Queryer) error { return failed }); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	still, err := store.FileMutationBarriers(t.Context(), "source")
	must(t, err)
	if len(still) != 1 || still[0].Token != barriers[0].Token {
		t.Fatal("failed verification consumed marker")
	}
	must(t, store.ReconcileFileMutation(t.Context(), "source", barriers[0], func(q Queryer) error {
		var id string
		return q.QueryRowContext(t.Context(), `SELECT id FROM workspaces WHERE id='source'`).Scan(&id)
	}))
	current, err := store.BeginFileMutation(t.Context(), "source", "workspace.json")
	must(t, err)
	if err := store.ReconcileFileMutation(t.Context(), "source", barriers[0], func(Queryer) error { t.Fatal("stale marker reached validation"); return nil }); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	// Rebind the old handle solely to simulate a late completion from a stopped
	// owner after reconciliation. Its token cannot consume the new operation.
	old.store = store
	if err := old.Complete(t.Context(), true); !errors.Is(err, ErrConflict) {
		t.Fatal("late completion cleared new writer", err)
	}
	must(t, current.Complete(t.Context(), true))
}

func TestFileMutationCannotRecreateResetStateOrAdmitForeignWork(t *testing.T) {
	db, store := localFixture(t)
	seedCanonicalWorkspace(t, db, "source")
	if _, err := store.BeginFileMutation(t.Context(), "source", "workspace.json"); !errors.Is(err, ErrConflict) {
		t.Fatal("unreviewed mutation admitted", err)
	}
	must(t, store.RegisterNative(t.Context(), "source"))
	mutation, err := store.BeginFileMutation(t.Context(), "source", "workspace.json")
	must(t, err)
	_, err = db.ExecContext(t.Context(), `DELETE FROM workspaces WHERE id='source'; DELETE FROM continuity_file_mutations; DELETE FROM continuity_dirty; DELETE FROM continuity_attachments`)
	must(t, err)
	if err := mutation.Complete(t.Context(), true); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err := store.MarkDirty(t.Context(), "source"); !errors.Is(err, ErrConflict) {
		t.Fatal("late invalidation recreated metadata", err)
	}
	seedCanonicalWorkspace(t, db, "source")
	must(t, store.RegisterNative(t.Context(), "source"))
	current, err := store.BeginFileMutation(t.Context(), "source", "workspace.json")
	must(t, err)
	if err := mutation.Complete(t.Context(), true); !errors.Is(err, ErrConflict) {
		t.Fatal("erased token reused authority", err)
	}
	must(t, current.Complete(t.Context(), true))
	_, err = db.ExecContext(t.Context(), `INSERT INTO users(id,created_at,updated_at) VALUES ('foreign',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
		UPDATE workspaces SET owner_user_id='foreign' WHERE id='source'`)
	must(t, err)
	if err := store.RegisterNative(t.Context(), "source"); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign canonical owner admitted", err)
	}
	if _, err := store.BeginFileMutation(t.Context(), "source", "workspace.json"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestPublishRefusesWriterThatStartsAfterCollection(t *testing.T) {
	directory, manifest, objects := fixture(t)
	db, store := localFixture(t)
	seedCanonicalWorkspace(t, db, manifest.WorkspaceID)
	must(t, store.RegisterNative(t.Context(), manifest.WorkspaceID))
	state, err := store.Preparation(t.Context(), manifest.WorkspaceID)
	must(t, err)
	var mutation *FileMutation
	source := func(ctx context.Context, digest string) (io.ReadCloser, error) {
		mutation, err = store.BeginFileMutation(ctx, manifest.WorkspaceID, "workspace.json")
		if err != nil {
			return nil, err
		}
		return objectSource(objects)(ctx, digest)
	}
	err = Publish(t.Context(), directory, manifest, source, store.SnapshotFence(manifest.WorkspaceID, uint64(state.Sequence)))
	if !errors.Is(err, ErrMutationPending) {
		t.Fatal("late writer crossed publication fence", err)
	}
	must(t, mutation.Complete(t.Context(), true))
}
