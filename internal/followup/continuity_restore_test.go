package followup

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func restoreFollowupWorkspaceFixture(t *testing.T, store *SQLiteStore) workspacecontinuity.RestoreScope {
	t.Helper()
	ctx := t.Context()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Copied commitments"})
	ws.ID, ws.FolderSlug, ws.OwnerUserID, ws.Version = "copied-hq", "copied-commitments", "local", 2
	canonical, err := ws.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	record, err := workspace.SnapshotContinuityWorkspace(canonical)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor workspace.ContinuityWorkspace
	if err := workspacecontinuity.DecodeRecord(record, &descriptor); err != nil {
		t.Fatal(err)
	}
	descriptor.SQLMetadata = &workspace.ContinuityWorkspaceSQLMetadata{Color: ""}
	record, err = workspacecontinuity.EncodeRecord(ws.ID, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	local := workspacecontinuity.NewLocalStore(store.db)
	destDigest, err := workspacecontinuity.DestinationDigest(ctx, store.db, "local")
	if err != nil {
		t.Fatal(err)
	}
	op := workspacecontinuity.Operation{ID: uuid.NewString(), UserID: "local", Action: workspacecontinuity.WorkspaceOnly,
		TreeDigest: workspacecontinuity.Digest([]byte("private copied fixture")), DestinationDigest: destDigest}
	if _, err := local.BeginReviewedImport(ctx, op, []workspacecontinuity.ImportMember{{WorkspaceID: ws.ID,
		Generation: uuid.NewString(), Digest: workspacecontinuity.Digest([]byte("manifest")), Disposition: workspacecontinuity.Ordinary}}); err != nil {
		t.Fatal(err)
	}
	scope := workspacecontinuity.RestoreScope{OperationID: op.ID, WorkspaceID: ws.ID, UserID: "local"}
	if err := store.db.InTransaction(ctx, func(tx *sql.Tx) error {
		inserted, err := session.NewSQLiteStore(store.db).RestoreContinuityWorkspace(ctx, tx, scope, record, canonical, "")
		if err != nil {
			return err
		}
		if !inserted {
			return workspacecontinuity.ErrIncomplete
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestRestoreContinuityFollowUpPreservesHistoryButDeniesSourceAuthority(t *testing.T) {
	_, destination := newTestService(t)
	scope := restoreFollowupWorkspaceFixture(t, destination)
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f := &FollowUp{ID: "source-followup", UserID: "local", WorkspaceID: scope.WorkspaceID, Category: CategoryWaitingOn,
		Direction: DirectionInbound, Title: "Wait for a private reply", Detail: "Keep the original meaning", Status: StatusCompleted,
		Provenance: ProvenanceExplicit, Source: SourceRef{Type: "email_thread", ID: "external-thread", AccountID: "external-account"},
		DedupKey: "external-dedup", RelatedTask: &TaskRef{WorkspaceID: "not-imported", TaskID: "external-task"},
		CreatedAt: at, UpdatedAt: at.Add(time.Hour), CompletedAt: &at}
	record, err := SnapshotContinuityFollowUp(f, scope.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	restore := func() (bool, error) {
		inserted := false
		err := destination.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
			var err error
			inserted, err = destination.RestoreContinuityFollowUp(t.Context(), tx, scope, record)
			return err
		})
		return inserted, err
	}
	if inserted, err := restore(); err != nil || !inserted {
		t.Fatal("owned receipt did not restore", err)
	}
	got, err := destination.Get(t.Context(), "local", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted || got.CompletedAt == nil || !got.CompletedAt.Equal(at) || !got.CreatedAt.Equal(at) || got.Source.AccountID != "" || got.DedupKey != "" || got.RelatedTask != nil || got.Detail != f.Detail {
		t.Fatal("restored work changed or foreign authority was installed", got)
	}
	if _, err := destination.GetByDedupKey(t.Context(), "local", "external-dedup"); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign dedup key resolved an operational follow-up", err)
	}
	var account, dedup, linkWS, linkTask string
	if err := destination.db.QueryRowContext(t.Context(), `SELECT source_account_id,source_dedup_key,related_workspace_id,related_task_id FROM followup_continuity_source_refs WHERE followup_id=?`, f.ID).Scan(&account, &dedup, &linkWS, &linkTask); err != nil || account != "external-account" || dedup != "external-dedup" || linkWS != "not-imported" || linkTask != "external-task" {
		t.Fatal("historical source evidence lost", err)
	}
	var before, after int64
	if err := destination.db.QueryRowContext(t.Context(), `SELECT sequence FROM continuity_dirty WHERE workspace_id=?`, scope.WorkspaceID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.db.ExecContext(t.Context(), `UPDATE followup_continuity_source_refs SET saved_at=CURRENT_TIMESTAMP WHERE followup_id=?`, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := destination.db.QueryRowContext(t.Context(), `SELECT sequence FROM continuity_dirty WHERE workspace_id=?`, scope.WorkspaceID).Scan(&after); err != nil || after != before+1 {
		t.Fatal("historical reference change did not dirty owner", err, before, after)
	}
	got.Title = "Local edited title"
	if err := destination.Update(t.Context(), got); err != nil {
		t.Fatal(err)
	}
	if inserted, err := restore(); err != nil || inserted {
		t.Fatal("exact retry replayed a follow-up", err)
	}
	got, err = destination.Get(t.Context(), "local", f.ID)
	if err != nil || got.Title != "Local edited title" {
		t.Fatal("local edit overwritten", err)
	}
	spool, err := workspacecontinuity.NewSpool(t.TempDir(), scope.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	if err := destination.db.InTransaction(t.Context(), func(tx *sql.Tx) error { return CollectContinuityFollowUps(t.Context(), tx, scope.WorkspaceID, spool) }); err != nil {
		t.Fatal(err)
	}
	components, objects, err := spool.Seal(t.Context())
	if err != nil || len(components) == 0 {
		t.Fatal("re-export failed", err)
	}
	seen := false
	for _, component := range components {
		if component.Domain != "followups" {
			continue
		}
		for _, ref := range component.Chunks {
			reader, err := objects(t.Context(), ref.Digest)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(io.LimitReader(reader, workspacecontinuity.MaxChunkBytes+1))
			if err != nil {
				t.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			chunk, err := workspacecontinuity.DecodeChunk(bytes.NewReader(data), ref, scope.WorkspaceID, "followups")
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range chunk.Records {
				value, err := DecodeContinuityFollowUp(record, scope.WorkspaceID)
				if err != nil {
					t.Fatal(err)
				}
				if value.FollowUp.ID != f.ID {
					continue
				}
				seen = true
				if value.FollowUp.Title != "Local edited title" || value.FollowUp.Source.AccountID != "external-account" || value.FollowUp.DedupKey != "external-dedup" || value.FollowUp.RelatedTask == nil || value.FollowUp.RelatedTask.WorkspaceID != "not-imported" || !value.SourceNeedsReview || !value.TaskLinkNeedsReview {
					t.Fatal("re-export lost local edit or historical inert references", value)
				}
			}
		}
	}
	if !seen {
		t.Fatal("canonical follow-up was omitted from re-export")
	}
	// Re-export is bound to the real, edited canonical row, not a stale source
	// blob. A further import still reports source and link as Needs review.
	if _, err := destination.db.ExecContext(t.Context(), `DELETE FROM personal_hq_followup WHERE id=?`, f.ID); err != nil {
		t.Fatal(err)
	}
	if inserted, err := restore(); err != nil || inserted {
		t.Fatal("retry resurrected deliberately deleted work", err)
	}
	var sidecars int
	if err := destination.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM followup_continuity_source_refs WHERE followup_id=?`, f.ID).Scan(&sidecars); err != nil || sidecars != 0 {
		t.Fatal("deletion retained foreign provenance", sidecars, err)
	}
}

func TestRestoreContinuityFollowUpDoesNotAdoptSameSourceDedupIncumbent(t *testing.T) {
	_, destination := newTestService(t)
	scope := restoreFollowupWorkspaceFixture(t, destination)
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	local := &FollowUp{ID: "incumbent-source", UserID: "local", WorkspaceID: "unrelated-local", Category: CategoryIOwe,
		Direction: DirectionInbound, Title: "Local source decision", Status: StatusActive, Source: SourceRef{Type: "email_thread", ID: "same-thread", AccountID: "locally-connected"},
		DedupKey: "same-source", Provenance: ProvenanceExplicit, CreatedAt: at, UpdatedAt: at}
	if err := destination.Create(t.Context(), local); err != nil {
		t.Fatal(err)
	}
	copied := *local
	copied.ID = "copied-source"
	copied.WorkspaceID = scope.WorkspaceID
	copied.Title = "Different historical source choice"
	copied.Source.AccountID = "foreign-account"
	record, err := SnapshotContinuityFollowUp(&copied, scope.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := destination.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		inserted, err := destination.RestoreContinuityFollowUp(t.Context(), tx, scope, record)
		if err == nil && !inserted {
			return workspacecontinuity.ErrIncomplete
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got, err := destination.GetByDedupKey(t.Context(), "local", "same-source")
	if err != nil || got.ID != local.ID || got.Title != local.Title {
		t.Fatal("copied foreign dedup replaced local source", err, got)
	}
	imported, err := destination.Get(t.Context(), "local", copied.ID)
	if err != nil || imported.DedupKey != "" || imported.Source.AccountID != "" {
		t.Fatal("copied source gained local identity", err, imported)
	}
}

func TestRestoreContinuityFollowUpRejectsExistingLocalIDWithoutReplacement(t *testing.T) {
	_, destination := newTestService(t)
	scope := restoreFollowupWorkspaceFixture(t, destination)
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f := &FollowUp{ID: "collision", UserID: "local", WorkspaceID: scope.WorkspaceID, Category: CategoryIOwe,
		Direction: DirectionNone, Title: "Source choice", Source: SourceRef{Type: "manual"}, Provenance: ProvenanceManual,
		Status: StatusActive, CreatedAt: at, UpdatedAt: at}
	record, err := SnapshotContinuityFollowUp(f, scope.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	f.Title = "Existing local choice"
	if err := destination.Create(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	err = destination.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := destination.RestoreContinuityFollowUp(t.Context(), tx, scope, record)
		return err
	})
	if !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("local ID collision was adopted", err)
	}
	got, err := destination.Get(t.Context(), "local", f.ID)
	if err != nil || got.Title != "Existing local choice" {
		t.Fatal("collision overwrote destination", err)
	}
}
