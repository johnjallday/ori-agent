package workspacecontinuity

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

func localFixture(t *testing.T) (*database.DB, *LocalStore) {
	t.Helper()
	db, err := database.Open(t.Context(), &database.Config{Path: filepath.Join(t.TempDir(), "sessions.db"), WALMode: true})
	must(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, NewLocalStore(db)
}

func seedCanonicalWorkspace(t *testing.T, db *database.DB, id string) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `INSERT INTO users(id,created_at,updated_at) VALUES ('local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) ON CONFLICT(id) DO NOTHING`)
	must(t, err)
	_, err = db.ExecContext(t.Context(), `INSERT INTO workspaces(id,name,created_at,updated_at) VALUES (?, 'Synthetic',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id)
	must(t, err)
}

func importFixture(t *testing.T, store *LocalStore, id string) (Operation, []ImportMember, RestoreScope) {
	t.Helper()
	operation := Operation{ID: uuid.NewString(), TreeDigest: Digest([]byte(id)), DestinationDigest: Digest([]byte("destination")), UserID: "local", Action: WorkspaceOnly}
	members := []ImportMember{{WorkspaceID: id, Generation: uuid.NewString(), Digest: Digest([]byte("manifest")), Disposition: WorkspaceOnlyHQ}}
	result, err := store.BeginImport(t.Context(), operation, members)
	must(t, err)
	return result, members, RestoreScope{OperationID: result.ID, WorkspaceID: id, UserID: "local"}
}

func finishComponents(t *testing.T, db *database.DB, scope RestoreScope) {
	t.Helper()
	must(t, db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		for _, domain := range DomainNames() {
			if err := SetComponentOutcome(t.Context(), tx, scope, ComponentOutcome{Domain: domain, Status: "restored"}); err != nil {
				return err
			}
		}
		return nil
	}))
}

func TestLocalReceiptRetryAfterEditAndActivationIsNoOp(t *testing.T) {
	db, store := localFixture(t)
	operation, members, scope := importFixture(t, store, "incoming")
	attachment, err := store.Attachment(t.Context(), scope.WorkspaceID)
	must(t, err)
	if attachment.AllowsAutomatic() || attachment.AllowsManual() || attachment.AllowsPreparation() {
		t.Fatal("partial import admitted")
	}
	if err := store.CompleteImport(t.Context(), operation.ID, "local"); !errors.Is(err, ErrConflict) {
		t.Fatalf("completed missing domains: %v", err)
	}
	must(t, db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		created, err := ClaimRecord(t.Context(), tx, scope, "workspace", "workspaces", scope.WorkspaceID, Digest([]byte("original")))
		if err != nil {
			return err
		}
		if !created {
			t.Fatal("first claim is not new")
		}
		_, err = tx.Exec(`INSERT INTO workspaces(id,name,created_at,updated_at) VALUES (?, 'Imported',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, scope.WorkspaceID)
		return err
	}))
	finishComponents(t, db, scope)
	must(t, store.CompleteImport(t.Context(), operation.ID, "local"))
	attachment, err = store.Attachment(t.Context(), scope.WorkspaceID)
	must(t, err)
	if attachment.AllowsAutomatic() || !attachment.AllowsManual() || !attachment.AllowsPreparation() {
		t.Fatal("complete import must be readable but inactive")
	}
	must(t, store.SetImportedActive(t.Context(), scope.WorkspaceID, attachment.Version, true))
	if err := store.SetImportedActive(t.Context(), scope.WorkspaceID, attachment.Version, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale activation review accepted: %v", err)
	}
	_, err = db.Exec(`UPDATE workspaces SET name='Local edit' WHERE id=?`, scope.WorkspaceID)
	must(t, err)
	operation.ID = uuid.NewString()
	operation.DestinationDigest = Digest([]byte("locally changed destination"))
	retried, err := store.BeginImport(t.Context(), operation, members)
	must(t, err)
	if retried.ID != scope.OperationID || retried.Status != "complete" {
		t.Fatal("repeat allocated a new operation")
	}
	must(t, db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		created, err := ClaimRecord(t.Context(), tx, scope, "workspace", "workspaces", scope.WorkspaceID, Digest([]byte("original")))
		if created {
			t.Fatal("retry would rewrite current content")
		}
		return err
	}))
	attachment, err = store.Attachment(t.Context(), scope.WorkspaceID)
	must(t, err)
	if !attachment.AllowsAutomatic() || attachment.Disposition != WorkspaceOnlyHQ {
		t.Fatal("retry reset activation or adopted an incoming HQ")
	}
	var name string
	must(t, db.QueryRow(`SELECT name FROM workspaces WHERE id=?`, scope.WorkspaceID).Scan(&name))
	if name != "Local edit" {
		t.Fatal("retry overwrote local edit")
	}
	if err := store.RegisterNative(t.Context(), scope.WorkspaceID); !errors.Is(err, ErrConflict) {
		t.Fatalf("promoted import to native: %v", err)
	}
	operation.TreeDigest = Digest([]byte("different generation"))
	if _, err := store.BeginImport(t.Context(), operation, members); !errors.Is(err, ErrConflict) {
		t.Fatalf("different generation overwrote existing IDs: %v", err)
	}
}

func TestRecordOwnershipRollsBackAndNeverClaimsForeignRows(t *testing.T) {
	db, store := localFixture(t)
	op, _, scope := importFixture(t, store, "incoming")
	failure := errors.New("synthetic adapter failure")
	err := db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		if _, err := ClaimRecord(t.Context(), tx, scope, "sessions", "sessions", "session-id", Digest(nil)); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	var count int
	must(t, db.QueryRow(`SELECT COUNT(*) FROM continuity_records`).Scan(&count))
	if count != 0 {
		t.Fatal("failed canonical write retained record ownership")
	}
	must(t, store.Interrupt(t.Context(), op.ID, "local"))
	must(t, db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		created, err := ClaimRecord(t.Context(), tx, scope, "sessions", "sessions", "session-id", Digest(nil))
		if !created {
			t.Fatal("interrupted operation cannot resume its missing record")
		}
		return err
	}))
	_, _, other := importFixture(t, store, "foreign")
	for _, input := range []RestoreScope{other, {OperationID: scope.OperationID, WorkspaceID: "not-a-member", UserID: "local"}, {OperationID: scope.OperationID, WorkspaceID: scope.WorkspaceID, UserID: "other-user"}} {
		err := db.InTransaction(t.Context(), func(tx *sql.Tx) error {
			_, err := ClaimRecord(t.Context(), tx, input, "sessions", "sessions", "session-id", Digest(nil))
			return err
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("foreign claim accepted: %v", err)
		}
	}
	seedCanonicalWorkspace(t, db, "already-local")
	operation := Operation{ID: uuid.NewString(), TreeDigest: Digest([]byte("collision")), DestinationDigest: Digest(nil), UserID: "local", Action: WorkspaceOnly}
	_, err = store.BeginImport(t.Context(), operation, []ImportMember{{WorkspaceID: "already-local", Generation: uuid.NewString(), Digest: Digest(nil), Disposition: Ordinary}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("existing local row accepted: %v", err)
	}
	if _, err := store.Operation(t.Context(), operation.ID, "local"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("collision left a partial operation")
	}
}

func TestInterruptedReceiptAndComponentReportSurviveReopen(t *testing.T) {
	db, store := localFixture(t)
	op, members, scope := importFixture(t, store, "incoming")
	must(t, db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		return SetComponentOutcome(t.Context(), tx, scope, ComponentOutcome{Domain: "uploads", Status: "failed", Reason: "disk_full"})
	}))
	must(t, store.Interrupt(t.Context(), op.ID, "local"))
	path := db.Path()
	must(t, db.Close())
	reopened, err := database.Open(t.Context(), &database.Config{Path: path})
	must(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	store = NewLocalStore(reopened)
	op.ID = uuid.NewString()
	retried, err := store.BeginImport(t.Context(), op, members)
	must(t, err)
	if retried.ID != scope.OperationID || retried.Status != "interrupted" {
		t.Fatal("restart lost the original partial receipt")
	}
	outcomes, err := store.ComponentOutcomes(t.Context(), scope)
	must(t, err)
	found := false
	for _, outcome := range outcomes {
		if outcome.Domain == "uploads" && outcome.Status == "failed" && outcome.Reason == "disk_full" {
			found = true
		}
	}
	if !found {
		t.Fatal("restart lost component failure")
	}
	foreign := scope
	foreign.UserID = "foreign-user"
	if _, err := store.ComponentOutcomes(t.Context(), foreign); !errors.Is(err, ErrConflict) {
		t.Fatal("exposed another owner's report:", err)
	}
	members[0].Digest = Digest([]byte("changed source"))
	if _, err := store.BeginImport(t.Context(), op, members); !errors.Is(err, ErrConflict) {
		t.Fatal("retry accepted changed reviewed content:", err)
	}
}

func TestDirtySnapshotFenceAndCASObserveCanonicalTransactions(t *testing.T) {
	db, store := localFixture(t)
	seedCanonicalWorkspace(t, db, "source")
	if _, err := store.ReadSnapshot(t.Context(), "source", func(Queryer, uint64) error { t.Fatal("unreviewed export ran"); return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown admission: %v", err)
	}
	must(t, store.RegisterNative(t.Context(), "source"))
	sequence, err := store.ReadSnapshot(t.Context(), "source", func(query Queryer, sequence uint64) error {
		var name string
		if sequence == 0 {
			t.Fatal("missing durable dirty sequence")
		}
		return query.QueryRowContext(t.Context(), `SELECT name FROM workspaces WHERE id='source'`).Scan(&name)
	})
	must(t, err)
	must(t, store.SnapshotFence("source", sequence)(t.Context()))
	must(t, store.Acknowledge(t.Context(), "source", sequence, uuid.NewString(), time.Now()))
	status, err := store.Preparation(t.Context(), "source")
	must(t, err)
	if !status.Ready() {
		t.Fatal("completed sequence not ready")
	}
	failure := errors.New("abort canonical mutation")
	err = db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE workspaces SET name='Rolled back' WHERE id='source'`); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	must(t, store.SnapshotFence("source", sequence)(t.Context()))
	_, err = db.Exec(`UPDATE workspaces SET name='New canonical value' WHERE id='source'`)
	must(t, err)
	if err := store.SnapshotFence("source", sequence)(t.Context()); !errors.Is(err, ErrChanged) {
		t.Fatalf("canonical update not fenced: %v", err)
	}
	if err := store.Acknowledge(t.Context(), "source", sequence, uuid.NewString(), time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("old worker acknowledged newer writes: %v", err)
	}
	status, err = store.Preparation(t.Context(), "source")
	must(t, err)
	if status.Ready() {
		t.Fatal("changed data still ready")
	}
	_, err = db.Exec(`DELETE FROM workspaces WHERE id='source'`)
	must(t, err)
	attachment, err := store.Attachment(t.Context(), "source")
	must(t, err)
	if attachment.State != Detached || attachment.AllowsPreparation() {
		t.Fatal("removed workspace can backfill empty state")
	}
}

func TestCanonicalWriteDuringPublicationFailsFreshSQLFence(t *testing.T) {
	db, store := localFixture(t)
	root, manifest, objects := fixture(t)
	seedCanonicalWorkspace(t, db, manifest.WorkspaceID)
	must(t, store.RegisterNative(t.Context(), manifest.WorkspaceID))
	sequence, err := store.ReadSnapshot(t.Context(), manifest.WorkspaceID, func(query Queryer, _ uint64) error {
		var id string
		return query.QueryRowContext(t.Context(), `SELECT id FROM workspaces WHERE id=?`, manifest.WorkspaceID).Scan(&id)
	})
	must(t, err)
	manifest.SourceRevision = sequence
	source := func(ctx context.Context, digest string) (io.ReadCloser, error) {
		// The read transaction is already released: the canonical write can
		// complete, and the final fence must see its committed dirty sequence.
		_, err := db.ExecContext(ctx, `UPDATE workspaces SET name='Changed during preparation' WHERE id=?`, manifest.WorkspaceID)
		if err != nil {
			return nil, err
		}
		return objectSource(objects)(ctx, digest)
	}
	if err := Publish(t.Context(), root, manifest, source, store.SnapshotFence(manifest.WorkspaceID, sequence)); !errors.Is(err, ErrChanged) {
		t.Fatalf("mixed database generation was published: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, Directory, "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale source snapshot acquired a current pointer")
	}
}

func TestDirtyMoveIncludesBothOwnersAndResetClearsAllLocalAuthority(t *testing.T) {
	db, store := localFixture(t)
	root, manifest, objects := fixture(t)
	seedCanonicalWorkspace(t, db, manifest.WorkspaceID)
	must(t, store.RegisterNative(t.Context(), manifest.WorkspaceID))
	seedCanonicalWorkspace(t, db, "other")
	_, err := db.Exec(`INSERT INTO daily_brief_config(workspace_id,user_id,timezone,created_at,updated_at) VALUES (?,'local','UTC',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, manifest.WorkspaceID)
	must(t, err)
	before, err := store.Preparation(t.Context(), manifest.WorkspaceID)
	must(t, err)
	_, err = db.Exec(`UPDATE daily_brief_config SET workspace_id='other' WHERE workspace_id=?`, manifest.WorkspaceID)
	must(t, err)
	after, err := store.Preparation(t.Context(), manifest.WorkspaceID)
	must(t, err)
	other, err := store.Preparation(t.Context(), "other")
	must(t, err)
	if after.Sequence <= before.Sequence || other.Sequence < 2 {
		t.Fatal("ownership move missed OLD or NEW owner")
	}
	manifest.SourceRevision = uint64(after.Sequence)
	must(t, Publish(t.Context(), root, manifest, objectSource(objects), store.SnapshotFence(manifest.WorkspaceID, manifest.SourceRevision)))
	_, _, scope := importFixture(t, store, "incoming")
	must(t, db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := ClaimRecord(t.Context(), tx, scope, "sessions", "sessions", "owned-record", Digest(nil))
		return err
	}))
	inspection, err := database.InspectReset(t.Context(), db.DB)
	must(t, err)
	if len(inspection.Problems) != 0 {
		t.Fatalf("new schema unclassified by reset: %v", inspection.Problems)
	}
	path := db.Path()
	must(t, db.Close())
	_, err = database.ResetAppRecords(t.Context(), path)
	must(t, err)
	reopened, err := database.Open(t.Context(), &database.Config{Path: path})
	must(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	store = NewLocalStore(reopened)
	attachment, err := store.Attachment(t.Context(), manifest.WorkspaceID)
	must(t, err)
	if attachment.State != Unreviewed || attachment.AllowsPreparation() || attachment.AllowsAutomatic() {
		t.Fatal("reset restored old attachment authority")
	}
	status, err := store.Preparation(t.Context(), manifest.WorkspaceID)
	must(t, err)
	if status.Sequence != 0 {
		t.Fatal("reset canonical-delete triggers repopulated dirty state after it was cleared")
	}
	_, err = Inspect(t.Context(), root)
	must(t, err)
	if _, err := os.Stat(filepath.Join(root, Directory, "current.json")); err != nil {
		t.Fatal("reset erased retained private checkpoint")
	}
	if _, err := store.ReadSnapshot(context.Background(), manifest.WorkspaceID, func(Queryer, uint64) error { t.Fatal("detached export ran"); return nil }); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
