package workspacecontinuity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func newNativeInsert(ctx context.Context, id string) func(*sql.Tx) error {
	return func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO workspaces(id,name,owner_user_id,created_at,updated_at) VALUES (?,'New native','local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id)
		return err
	}
}

func TestNativeCreationStagesAuthorityBeforeRuntimeAdmission(t *testing.T) {
	db, local := localFixture(t)
	creation, err := local.BeginNativeCreation(t.Context(), "created", newNativeInsert(t.Context(), "created"))
	must(t, err)
	a, err := local.Attachment(t.Context(), "created")
	must(t, err)
	if a.State != Native || !a.Provisioning || a.AllowsManual() || a.AllowsAutomatic() || a.AllowsPreparation() {
		t.Fatal("unfinished native creation was admitted", a)
	}
	if err := local.RegisterNative(t.Context(), "created"); !errors.Is(err, ErrConflict) {
		t.Fatal("inventory bypassed provisioning", err)
	}
	if _, err := local.ReadSnapshot(t.Context(), "created", func(Queryer, uint64) error { t.Fatal("creation reached capture"); return nil }); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	p, err := local.Preparation(t.Context(), "created")
	must(t, err)
	if p.Admitted || p.Ready() || !p.PendingMutation {
		t.Fatal("creating workspace reported admitted/ready")
	}
	// Canonical file staging is allowed, but its own failed cleanup cannot be
	// hidden by completing the enclosing creation or by restart reconciliation.
	inner, err := local.BeginFileMutation(t.Context(), "created", "workspace.json")
	must(t, err)
	validate := func(q Queryer) error {
		var owner string
		return q.QueryRowContext(t.Context(), `SELECT owner_user_id FROM workspaces WHERE id='created'`).Scan(&owner)
	}
	if err := creation.Complete(t.Context(), validate); !errors.Is(err, ErrMutationPending) {
		t.Fatal("completed over pending inner write", err)
	}
	must(t, inner.Complete(t.Context(), false))
	if err := creation.Complete(t.Context(), validate); !errors.Is(err, ErrMutationPending) {
		t.Fatal("completed over failed inner write", err)
	}
	replacement, err := local.BeginFileMutation(t.Context(), "created", "workspace.json")
	must(t, err)
	must(t, replacement.Complete(t.Context(), true))
	if err := creation.Complete(t.Context(), func(Queryer) error { return ErrChanged }); !errors.Is(err, ErrChanged) {
		t.Fatal(err)
	}
	a, err = local.Attachment(t.Context(), "created")
	must(t, err)
	if a.AllowsAutomatic() {
		t.Fatal("failed canonical validation admitted execution")
	}
	must(t, creation.Complete(t.Context(), validate))
	a, err = local.Attachment(t.Context(), "created")
	must(t, err)
	if a.Provisioning || !a.AllowsAutomatic() || !a.AllowsManual() {
		t.Fatal("completed new native workspace did not become usable")
	}
	if err := creation.Complete(t.Context(), validate); !errors.Is(err, ErrConflict) {
		t.Fatal("stale completion token succeeded", err)
	}
	p, err = local.Preparation(t.Context(), "created")
	must(t, err)
	if p.Ready() {
		t.Fatal("creation fabricated a checkpoint")
	}
	if _, err := NewLocalStore(db).BeginFileMutation(t.Context(), "created", nativeCreationTarget); !errors.Is(err, ErrInvalid) {
		t.Fatal("generic writer could manufacture creation authority")
	}
}

func TestNativeCreationRollbackConflictAndRestart(t *testing.T) {
	db, local := localFixture(t)
	_, err := local.BeginNativeCreation(t.Context(), "rollback", func(tx *sql.Tx) error {
		if err := newNativeInsert(t.Context(), "rollback")(tx); err != nil {
			return err
		}
		return ErrChanged
	})
	if !errors.Is(err, ErrChanged) {
		t.Fatal(err)
	}
	var rows int
	must(t, db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM workspaces WHERE id='rollback')+(SELECT COUNT(*) FROM continuity_attachments WHERE workspace_id='rollback')+(SELECT COUNT(*) FROM continuity_dirty WHERE workspace_id='rollback')`).Scan(&rows))
	if rows != 0 {
		t.Fatal("failed insertion left authority or a canonical row")
	}
	_, err = local.BeginNativeCreation(t.Context(), "expected", newNativeInsert(t.Context(), "wrong"))
	if !errors.Is(err, ErrConflict) {
		t.Fatal("callback inserted the wrong owner identity", err)
	}
	must(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspaces WHERE id='wrong'`).Scan(&rows))
	if rows != 0 {
		t.Fatal("mismatched insertion survived rollback")
	}
	seedCanonicalWorkspace(t, db, "incumbent")
	called := false
	_, err = local.BeginNativeCreation(t.Context(), "incumbent", func(*sql.Tx) error { called = true; return nil })
	if !errors.Is(err, ErrConflict) || called {
		t.Fatal("creation adopted an existing identity")
	}
	creation, err := local.BeginNativeCreation(t.Context(), "interrupted", newNativeInsert(t.Context(), "interrupted"))
	must(t, err)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := creation.Fail(cancelled, ErrIncomplete); !errors.Is(err, ErrIncomplete) {
		t.Fatal(err)
	}
	reopened := NewLocalStore(db)
	a, err := reopened.Attachment(t.Context(), "interrupted")
	must(t, err)
	if !a.Provisioning || a.AllowsAutomatic() {
		t.Fatal("restart/failed creation admitted execution")
	}
	barriers, err := reopened.FileMutationBarriers(t.Context(), "interrupted")
	must(t, err)
	if len(barriers) != 1 || !barriers[0].Failed {
		t.Fatal("creation failure was forgotten")
	}
	must(t, reopened.ReconcileFileMutation(t.Context(), "interrupted", barriers[0], func(Queryer) error { return nil }))
	a, err = reopened.Attachment(t.Context(), "interrupted")
	must(t, err)
	if a.Provisioning || !a.AllowsAutomatic() {
		t.Fatal("explicit canonical reconciliation did not finish creation")
	}
}

func TestAttachmentRuntimeAdmissionRechecksCanonicalOwnership(t *testing.T) {
	db, local := localFixture(t)
	seedCanonicalWorkspace(t, db, "native")
	must(t, local.RegisterNative(t.Context(), "native"))
	_, err := db.ExecContext(t.Context(), `UPDATE workspaces SET deleted_at=CURRENT_TIMESTAMP WHERE id='native'`)
	must(t, err)
	a, err := local.Attachment(t.Context(), "native")
	must(t, err)
	if a.State != Detached || a.AllowsAutomatic() || a.AllowsManual() {
		t.Fatal("soft-deleted native row kept runtime admission")
	}
	_, err = db.ExecContext(t.Context(), `INSERT INTO users(id,created_at,updated_at) VALUES ('foreign',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	must(t, err)
	_, err = db.ExecContext(t.Context(), `UPDATE workspaces SET deleted_at=NULL,owner_user_id='foreign' WHERE id='native'`)
	must(t, err)
	a, err = local.Attachment(t.Context(), "native")
	must(t, err)
	if a.AllowsAutomatic() || a.AllowsManual() {
		t.Fatal("foreign canonical row kept native admission")
	}
	_, err = db.ExecContext(t.Context(), `UPDATE continuity_attachments SET state='imported_inactive' WHERE workspace_id='native'`)
	must(t, err)
	if err := local.SetImportedActive(t.Context(), "native", a.Version, true); !errors.Is(err, ErrConflict) {
		t.Fatal("stale activation granted a foreign row", err)
	}
}

func TestNativeCreationLateCompletionCannotUndoDeletion(t *testing.T) {
	db, local := localFixture(t)
	creation, err := local.BeginNativeCreation(t.Context(), "deleted", newNativeInsert(t.Context(), "deleted"))
	must(t, err)
	_, err = db.ExecContext(t.Context(), `DELETE FROM workspaces WHERE id='deleted'`)
	must(t, err)
	called := false
	if err := creation.Complete(t.Context(), func(Queryer) error { called = true; return nil }); !errors.Is(err, ErrConflict) || called {
		t.Fatal("late creation validation ran after deletion", err)
	}
	a, err := local.Attachment(t.Context(), "deleted")
	must(t, err)
	if a.State != Detached || a.AllowsManual() {
		t.Fatal("completion undid deletion")
	}
	if _, err := local.BeginNativeCreation(t.Context(), "deleted", newNativeInsert(t.Context(), "deleted")); !errors.Is(err, ErrConflict) {
		t.Fatal("a deleted identity became a new creation")
	}
}
