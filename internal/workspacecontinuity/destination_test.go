package workspacecontinuity

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestReviewedImportBindsSQLDestinationInReceiptTransaction(t *testing.T) {
	db, local := localFixture(t)
	initial, err := DestinationDigest(t.Context(), db, "local")
	must(t, err)
	if len(initial) != 64 {
		t.Fatal("missing destination digest")
	}
	again, err := DestinationDigest(t.Context(), db, "local")
	must(t, err)
	if initial != again {
		t.Fatal("unchanged destination gave different digest")
	}
	member := ImportMember{WorkspaceID: "copied-work", Generation: uuid.NewString(), Digest: Digest([]byte("verified-manifest")), Disposition: Ordinary}
	operation := Operation{ID: uuid.NewString(), UserID: "local", TreeDigest: Digest([]byte("verified-physical-tree")), DestinationDigest: initial, Action: WorkspaceOnly}
	seedCanonicalWorkspace(t, db, "incumbent")
	if _, err := local.BeginReviewedImport(t.Context(), operation, []ImportMember{member}); !errors.Is(err, ErrChanged) {
		t.Fatal("changed topology accepted", err)
	}
	var count int
	must(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM continuity_operations`).Scan(&count))
	if count != 0 {
		t.Fatal("a stale review mutated receipts")
	}
	fresh, err := DestinationDigest(t.Context(), db, "local")
	must(t, err)
	if fresh == initial {
		t.Fatal("new workspace did not change the destination")
	}
	operation.DestinationDigest = fresh
	_, err = db.ExecContext(t.Context(), `INSERT INTO personal_assistant_state (user_id,assistant_id,status,created_at,updated_at)
		VALUES ('local','incumbent-assistant','hiring',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	must(t, err)
	if _, err := local.BeginReviewedImport(t.Context(), operation, []ImportMember{member}); !errors.Is(err, ErrChanged) {
		t.Fatal("new incumbent assistant accepted", err)
	}
	fresh, err = DestinationDigest(t.Context(), db, "local")
	must(t, err)
	operation.DestinationDigest = fresh
	result, err := local.BeginReviewedImport(t.Context(), operation, []ImportMember{member})
	must(t, err)
	if result.Status != "restoring" || result.ID != operation.ID {
		t.Fatal("receipt was not durably inactive")
	}
	attachment, err := local.Attachment(t.Context(), member.WorkspaceID)
	must(t, err)
	if attachment.State != Restoring || attachment.AllowsAutomatic() || attachment.AllowsManual() {
		t.Fatal("pending receipt admitted imported execution")
	}
	// A pending exact retry owns the same receipt. It may not allocate another
	// operation or overwrite an independently inserted canonical row.
	retried, err := local.BeginReviewedImport(t.Context(), operation, []ImportMember{member})
	must(t, err)
	if retried.ID != operation.ID {
		t.Fatal("pending retry changed ownership")
	}
	must(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM continuity_operations`).Scan(&count))
	if count != 1 {
		t.Fatal("retry duplicated the operation")
	}
}

func TestDestinationDigestFailsClosedOnMissingPrincipalAndContradictoryCollisions(t *testing.T) {
	db, local := localFixture(t)
	if _, err := DestinationDigest(t.Context(), db, "foreign"); !errors.Is(err, ErrConflict) {
		t.Fatal("missing local principal produced a valid digest", err)
	}
	initial, err := DestinationDigest(t.Context(), db, "local")
	must(t, err)
	member := ImportMember{WorkspaceID: "colliding-id", Generation: uuid.NewString(), Digest: Digest([]byte("manifest")), Disposition: Ordinary}
	seedCanonicalWorkspace(t, db, member.WorkspaceID)
	operation := Operation{ID: uuid.NewString(), UserID: "local", TreeDigest: Digest([]byte("tree")), DestinationDigest: initial, Action: WorkspaceOnly}
	if _, err := local.BeginReviewedImport(t.Context(), operation, []ImportMember{member}); !errors.Is(err, ErrChanged) {
		t.Fatal("colliding row survived stale review", err)
	}
	operation.DestinationDigest, err = DestinationDigest(t.Context(), db, "local")
	must(t, err)
	if _, err := local.BeginReviewedImport(t.Context(), operation, []ImportMember{member}); !errors.Is(err, ErrConflict) {
		t.Fatal("fresh review adopted a colliding row", err)
	}
	var count int
	must(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM continuity_attachments WHERE workspace_id=?`, member.WorkspaceID).Scan(&count))
	if count != 0 {
		t.Fatal("colliding identity gained an import attachment")
	}
}
