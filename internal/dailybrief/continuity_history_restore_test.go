package dailybrief

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestRestoreContinuityRevisionPreservesHistoryWithoutNewGeneration(t *testing.T) {
	destination, scope := briefContinuityDestination(t)
	at := time.Date(2026, 8, 12, 9, 15, 0, 0, time.UTC)
	source := &Revision{ID: "saved-brief", WorkspaceID: scope.WorkspaceID, UserID: "local", LocalDate: "2026-08-12", RevisionNumber: 2,
		IsCurrent: true, Trigger: TriggerManual, Status: GenerationSucceeded, ConfigRevision: 7,
		ContentJSON: `{"text":"written private brief"}`, CreatedAt: at, GeneratedAt: at, SourceWindowStart: at.Add(-time.Hour), SourceWindowEnd: at}
	record, err := SnapshotContinuityRevision(source, scope.WorkspaceID)
	briefContinuityMust(t, err)
	restore := func() (bool, error) {
		inserted := false
		err := destination.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
			var err error
			inserted, err = destination.RestoreContinuityRevision(t.Context(), tx, scope, record)
			return err
		})
		return inserted, err
	}
	if created, err := restore(); err != nil || !created {
		t.Fatal("terminal history did not restore", err)
	}
	got, err := destination.GetRevision(t.Context(), source.ID)
	briefContinuityMust(t, err)
	if got.ContentJSON != source.ContentJSON || got.ConfigRevision != 7 || got.RevisionNumber != 2 || !got.IsCurrent || !got.GeneratedAt.Equal(at) || !got.CreatedAt.Equal(at) || !got.SourceWindowStart.Equal(at.Add(-time.Hour)) {
		t.Fatal("restored brief changed prose, dates or selection", got)
	}
	var claims, notifications int
	if err := destination.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM daily_brief_generation_claim`).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if err := destination.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM daily_brief_notification`).Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if claims != 0 || notifications != 0 {
		t.Fatal("import replayed a claim or notification")
	}
	if _, err := destination.db.ExecContext(t.Context(), `UPDATE daily_brief_revision SET content_json='{"text":"local edit"}' WHERE id=?`, source.ID); err != nil {
		t.Fatal(err)
	}
	if created, err := restore(); err != nil || created {
		t.Fatal("exact retry replayed historic prose", err)
	}
	got, err = destination.GetRevision(t.Context(), source.ID)
	briefContinuityMust(t, err)
	if got.ContentJSON != `{"text":"local edit"}` {
		t.Fatal("receipt did not protect later edit")
	}
	if _, err := destination.db.ExecContext(t.Context(), `DELETE FROM daily_brief_revision WHERE id=?`, source.ID); err != nil {
		t.Fatal(err)
	}
	if created, err := restore(); err != nil || created {
		t.Fatal("exact retry resurrected deleted revision", err)
	}
}

func TestRestoreContinuityRevisionInterruptsInFlightAndRefusesLocalCollision(t *testing.T) {
	destination, scope := briefContinuityDestination(t)
	at := time.Date(2026, 8, 12, 9, 15, 0, 0, time.UTC)
	inFlight := &Revision{ID: "source-in-flight", WorkspaceID: scope.WorkspaceID, UserID: "local", LocalDate: "2026-08-11", RevisionNumber: 1,
		Trigger: TriggerFirstOpen, Status: GenerationRunning, ContentJSON: "", CreatedAt: at}
	record, err := SnapshotContinuityRevision(inFlight, scope.WorkspaceID)
	briefContinuityMust(t, err)
	err = destination.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := destination.RestoreContinuityRevision(t.Context(), tx, scope, record)
		return err
	})
	briefContinuityMust(t, err)
	restored, err := destination.GetRevision(t.Context(), inFlight.ID)
	if err != nil || restored.Status != GenerationFailed || restored.IsCurrent || restored.FailureReason != InterruptedByTransfer {
		t.Fatal("running source was not recorded as interrupted history", restored, err)
	}
	if claim, err := destination.GetLatestClaim(t.Context(), scope.WorkspaceID, inFlight.LocalDate); err != nil || claim != nil {
		t.Fatal("an interrupted source attempt became a live or completed claim", claim, err)
	}
	r := &Revision{ID: "source-brief", WorkspaceID: scope.WorkspaceID, UserID: "local", LocalDate: "2026-08-12", RevisionNumber: 1,
		Trigger: TriggerFirstOpen, CreatedAt: at}
	r.Status = GenerationSucceeded
	r.ContentJSON = `{"text":"incoming"}`
	record, err = SnapshotContinuityRevision(r, scope.WorkspaceID)
	briefContinuityMust(t, err)
	briefContinuityMust(t, destination.CreateRevision(t.Context(), &Revision{ID: "existing-local", WorkspaceID: scope.WorkspaceID, UserID: "local", LocalDate: r.LocalDate, RevisionNumber: 1,
		Trigger: TriggerManual, Status: GenerationSucceeded, ContentJSON: `{"text":"incumbent"}`, CreatedAt: at}))
	err = destination.db.InTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := destination.RestoreContinuityRevision(t.Context(), tx, scope, record)
		return err
	})
	if !errors.Is(err, workspacecontinuity.ErrConflict) {
		t.Fatal("same-date local revision was overwritten", err)
	}
	got, err := destination.GetRevision(t.Context(), "existing-local")
	if err != nil || got.ContentJSON != `{"text":"incumbent"}` {
		t.Fatal("local collision changed", err)
	}
}
