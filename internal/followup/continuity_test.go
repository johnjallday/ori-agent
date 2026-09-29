package followup

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestCollectContinuityFollowUpsKeepsAllOwnerStatesWithoutHQProjection(t *testing.T) {
	_, store := newTestService(t)
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	statuses := []Status{StatusCandidate, StatusActive, StatusSnoozed, StatusReopened, StatusCompleted, StatusDismissed}
	for i, status := range statuses {
		f := &FollowUp{ID: "followup-" + string(rune('a'+i)), UserID: "local", WorkspaceID: "hq-owned", Category: CategoryIOwe,
			Direction: DirectionOutbound, Title: "Synthetic private commitment", Source: SourceRef{Type: "manual"}, Provenance: ProvenanceManual,
			Status: status, CreatedAt: at, UpdatedAt: at.Add(time.Duration(i) * time.Hour)}
		if status == StatusSnoozed {
			f.DueAt, f.SnoozedUntil, f.LastNudgedAt = &at, &at, &at
		}
		if status == StatusCompleted {
			f.CompletedAt = &at
		}
		if status == StatusDismissed {
			f.DismissedAt = &at
		}
		if i == 1 {
			f.Source = SourceRef{Type: "email_thread", ID: "owned-thread", AccountID: "foreign-account"}
		}
		if err := store.Create(t.Context(), f); err != nil {
			t.Fatal(err)
		}
	}
	other := &FollowUp{ID: "email-ops-owned", UserID: "local", WorkspaceID: "email-ops", Category: CategoryWaitingOn,
		Direction: DirectionInbound, Title: "Do not clone HQ projection", Source: SourceRef{Type: "manual"}, Provenance: ProvenanceManual,
		Status: StatusActive, CreatedAt: at, UpdatedAt: at}
	if err := store.Create(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	spool, err := workspacecontinuity.NewSpool(t.TempDir(), "hq-owned")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	if err := store.db.InTransaction(t.Context(), func(tx *sql.Tx) error { return CollectContinuityFollowUps(t.Context(), tx, "hq-owned", spool) }); err != nil {
		t.Fatal(err)
	}
	components, objects, err := spool.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[Status]bool{}
	for _, component := range components {
		if component.Domain != "followups" {
			continue
		}
		if component.Availability != workspacecontinuity.Present || component.Counts["followups"] != 6 {
			t.Fatal("source owner count wrong", component)
		}
		for _, ref := range component.Chunks {
			body, err := objects(t.Context(), ref.Digest)
			if err != nil {
				t.Fatal(err)
			}
			data, readErr := io.ReadAll(io.LimitReader(body, workspacecontinuity.MaxChunkBytes+1))
			if err := body.Close(); err != nil {
				t.Fatal(err)
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			chunk, err := workspacecontinuity.DecodeChunk(bytes.NewReader(data), ref, "hq-owned", "followups")
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range chunk.Records {
				value, err := DecodeContinuityFollowUp(record, "hq-owned")
				if err != nil {
					t.Fatal(err)
				}
				if value.FollowUp.ID == other.ID {
					t.Fatal("Email Ops row leaked through HQ projection")
				}
				seen[value.FollowUp.Status] = true
				if value.FollowUp.Status == StatusSnoozed && (value.FollowUp.DueAt == nil || value.FollowUp.SnoozedUntil == nil || value.FollowUp.LastNudgedAt == nil || !value.FollowUp.DueAt.Equal(at) || !value.FollowUp.SnoozedUntil.Equal(at) || !value.FollowUp.LastNudgedAt.Equal(at)) {
					t.Fatal("due/snooze/nudge history lost")
				}
				if value.FollowUp.ID == "followup-b" && (!value.SourceNeedsReview || value.FollowUp.Source.AccountID != "foreign-account") {
					t.Fatal("historical foreign account lost its inert review marker")
				}
			}
		}
	}
	if len(seen) != len(statuses) {
		t.Fatal("lifecycle history was truncated", seen)
	}
	// When the separately owned Email Ops child is itself included, its row
	// stays with that child; the HQ projection did not steal or duplicate it.
	child, err := workspacecontinuity.NewSpool(t.TempDir(), "email-ops")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Close() }()
	if err := store.db.InTransaction(t.Context(), func(tx *sql.Tx) error { return CollectContinuityFollowUps(t.Context(), tx, "email-ops", child) }); err != nil {
		t.Fatal(err)
	}
	childComponents, _, err := child.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, component := range childComponents {
		if component.Domain == "followups" {
			found = component.Availability == workspacecontinuity.Present && component.Counts["followups"] == 1
		}
	}
	if !found {
		t.Fatal("child-owned follow-up was not captured under its child")
	}
}

func TestContinuityFollowUpCodecRejectsForeignClaimsAndForgedReviewMarker(t *testing.T) {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f := &FollowUp{ID: "private-row", UserID: "local", WorkspaceID: "source-hq", Category: CategoryIOwe,
		Direction: DirectionNone, Title: "Private history", Source: SourceRef{Type: "email_thread", ID: "thread-1", AccountID: "external"},
		Provenance: ProvenanceExplicit, Status: StatusActive, CreatedAt: at, UpdatedAt: at}
	record, err := SnapshotContinuityFollowUp(f, "source-hq")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeContinuityFollowUp(record, "other-hq"); err == nil {
		t.Fatal("foreign owner decoded")
	}
	if _, err := DecodeContinuityFollowUp(workspacecontinuity.Record{ID: "other-row", Data: record.Data}, "source-hq"); err == nil {
		t.Fatal("forged record ID decoded")
	}
	var value ContinuityFollowUp
	if err := json.Unmarshal(record.Data, &value); err != nil {
		t.Fatal(err)
	}
	value.SourceNeedsReview = false
	forged, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeContinuityFollowUp(workspacecontinuity.Record{ID: record.ID, Data: forged}, "source-hq"); err == nil {
		t.Fatal("external account was mislabeled as approved")
	}
	f.RelatedTask = &TaskRef{WorkspaceID: "source-hq"}
	if _, err := SnapshotContinuityFollowUp(f, "source-hq"); err == nil {
		t.Fatal("incomplete linked-task provenance was accepted")
	}
	f.RelatedTask = nil
	f.Source.AccountID = strings.Repeat("x", 4097)
	if _, err := SnapshotContinuityFollowUp(f, "source-hq"); err == nil {
		t.Fatal("unbounded external source reference was accepted")
	}
}

func TestCollectContinuityFollowUpsCrossesBoundedKeysetPage(t *testing.T) {
	_, store := newTestService(t)
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 259; i++ {
		f := &FollowUp{ID: fmt.Sprintf("page-%04d", i), UserID: "local", WorkspaceID: "paged-hq", Category: CategoryIOwe,
			Direction: DirectionNone, Title: "Private synthetic row", Source: SourceRef{Type: "manual"}, Provenance: ProvenanceManual,
			Status: StatusActive, CreatedAt: at, UpdatedAt: at}
		if err := store.Create(t.Context(), f); err != nil {
			t.Fatal(i, err)
		}
	}
	spool, err := workspacecontinuity.NewSpool(t.TempDir(), "paged-hq")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	if err := store.db.InTransaction(t.Context(), func(tx *sql.Tx) error { return CollectContinuityFollowUps(t.Context(), tx, "paged-hq", spool) }); err != nil {
		t.Fatal(err)
	}
	components, _, err := spool.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range components {
		if component.Domain == "followups" {
			if component.Counts["followups"] != 259 {
				t.Fatal("keyset omitted a page", component)
			}
			return
		}
	}
	t.Fatal("pageable follow-up component unavailable")
}

func TestFollowUpDirtyTriggersCoverCreationMoveDeletionAndSourceReference(t *testing.T) {
	_, store := newTestService(t)
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f := &FollowUp{ID: "moving-followup", UserID: "local", WorkspaceID: "old-owner", Category: CategoryIOwe,
		Direction: DirectionNone, Title: "Synthetic commitment", Source: SourceRef{Type: "manual"}, Provenance: ProvenanceManual,
		Status: StatusActive, CreatedAt: at, UpdatedAt: at}
	if err := store.Create(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	sequence := func(owner string) int64 {
		t.Helper()
		var value int64
		if err := store.db.QueryRowContext(t.Context(), `SELECT sequence FROM continuity_dirty WHERE workspace_id=?`, owner).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if sequence("old-owner") != 1 {
		t.Fatal("new follow-up did not dirty owner")
	}
	if _, err := store.db.ExecContext(t.Context(), `UPDATE personal_hq_followup SET workspace_id='new-owner' WHERE id=?`, f.ID); err != nil {
		t.Fatal(err)
	}
	if sequence("old-owner") != 2 || sequence("new-owner") != 1 {
		t.Fatal("move failed to dirty both owners")
	}
	if err := store.Delete(t.Context(), "local", f.ID); err != nil {
		t.Fatal(err)
	}
	if sequence("new-owner") != 2 {
		t.Fatal("deletion retained stale snapshot membership")
	}
}

func TestContinuityFollowUpsFailClosedOnForeignOwnershipAndInvalidSource(t *testing.T) {
	_, store := newTestService(t)
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	f := &FollowUp{ID: "foreign", UserID: "other", WorkspaceID: "hq-owned", Category: CategoryIOwe, Direction: DirectionNone,
		Title: "Another principal", Source: SourceRef{Type: "manual"}, Provenance: ProvenanceManual, Status: StatusActive, CreatedAt: at, UpdatedAt: at}
	if err := store.Create(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	spool, err := workspacecontinuity.NewSpool(t.TempDir(), "hq-owned")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	if err := store.db.InTransaction(t.Context(), func(tx *sql.Tx) error { return CollectContinuityFollowUps(t.Context(), tx, "hq-owned", spool) }); err == nil {
		t.Fatal("foreign user row was captured as local")
	}
	f.UserID = "local"
	f.ID = "local-different-owner"
	f.WorkspaceID = "email-ops"
	if _, err := SnapshotContinuityFollowUp(f, "hq-owned"); err == nil {
		t.Fatal("foreign workspace claimed a follow-up")
	}
}
