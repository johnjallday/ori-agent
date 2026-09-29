package dailybrief

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestCollectContinuityRevisionsCrossesPageWithoutCollapsingOneDate(t *testing.T) {
	store := newTestStore(t)
	at := time.Date(2026, 8, 12, 9, 15, 0, 0, time.UTC)
	for i := 0; i < 259; i++ {
		if err := store.CreateRevision(t.Context(), &Revision{ID: fmt.Sprintf("brief-%04d", i), WorkspaceID: "paged-briefs", UserID: "local",
			LocalDate: "2026-08-12", RevisionNumber: i + 1, Trigger: TriggerManual, Status: GenerationSucceeded,
			ContentJSON: `{"text":"synthetic"}`, CreatedAt: at}); err != nil {
			t.Fatal(i, err)
		}
	}
	spool, err := workspacecontinuity.NewSpool(t.TempDir(), "paged-briefs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	if err := store.db.InTransaction(t.Context(), func(tx *sql.Tx) error { return CollectContinuityRevisions(t.Context(), tx, "paged-briefs", spool) }); err != nil {
		t.Fatal(err)
	}
	components, _, err := spool.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range components {
		if component.Domain == "brief_history" {
			if component.Counts["revisions"] != 259 {
				t.Fatal("second same-date page disappeared", component)
			}
			return
		}
	}
	t.Fatal("brief history component missing")
}

func TestBriefRevisionContinuityDirtyTracksCurrentAndRetention(t *testing.T) {
	store := newTestStore(t)
	at := time.Date(2026, 8, 12, 9, 15, 0, 0, time.UTC)
	create := func(id, date string) {
		t.Helper()
		if err := store.CreateRevision(t.Context(), &Revision{ID: id, WorkspaceID: "brief-owner", UserID: "local", LocalDate: date,
			RevisionNumber: 1, Trigger: TriggerManual, Status: GenerationSucceeded, ContentJSON: `{"text":"synthetic"}`, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	sequence := func() int64 {
		t.Helper()
		var value int64
		if err := store.db.QueryRowContext(t.Context(), `SELECT sequence FROM continuity_dirty WHERE workspace_id='brief-owner'`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	create("old-brief", "2026-08-11")
	if sequence() != 1 {
		t.Fatal("creating revision did not dirty owner")
	}
	if err := store.SetCurrentRevision(t.Context(), "brief-owner", "old-brief"); err != nil {
		t.Fatal(err)
	}
	if sequence() != 2 {
		t.Fatal("first current selection was not snapshotted")
	}
	create("new-brief", "2026-08-12")
	if err := store.SetCurrentRevision(t.Context(), "brief-owner", "new-brief"); err != nil {
		t.Fatal(err)
	}
	before := sequence()
	if err := store.PruneHistory(t.Context(), "brief-owner", 1); err != nil {
		t.Fatal(err)
	}
	if sequence() != before+1 {
		t.Fatal("pruning obsolete revision did not invalidate prior snapshot")
	}
}

func TestCollectContinuityRevisionsPreservesEverySameDayRevisionAndExactOwner(t *testing.T) {
	store := newTestStore(t)
	at := time.Date(2026, 8, 12, 9, 15, 0, 0, time.UTC)
	for i, id := range []string{"brief-one", "brief-two", "brief-other-date", "unrelated-workspace"} {
		owner, date, number := "source-hq", "2026-08-12", i+1
		if i == 2 {
			date, number = "2026-08-13", 1
		}
		if i == 3 {
			owner, number = "email-ops", 1
		}
		rev := &Revision{ID: id, WorkspaceID: owner, UserID: "local", LocalDate: date, RevisionNumber: number,
			Trigger: TriggerManual, Status: GenerationSucceeded, ConfigRevision: 7, ContentJSON: `{"text":"synthetic private history"}`,
			CreatedAt: at.Add(time.Duration(i) * time.Hour), GeneratedAt: at.Add(time.Duration(i) * time.Hour), SourceWindowStart: at.Add(-time.Hour), SourceWindowEnd: at}
		if err := store.CreateRevision(t.Context(), rev); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetCurrentRevision(t.Context(), "source-hq", "brief-two"); err != nil {
		t.Fatal(err)
	}
	spool, err := workspacecontinuity.NewSpool(t.TempDir(), "source-hq")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	if err := store.db.InTransaction(t.Context(), func(tx *sql.Tx) error { return CollectContinuityRevisions(t.Context(), tx, "source-hq", spool) }); err != nil {
		t.Fatal(err)
	}
	components, objects, err := spool.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]ContinuityRevision{}
	for _, component := range components {
		if component.Domain != "brief_history" {
			continue
		}
		if component.Availability != workspacecontinuity.Present || component.Counts["revisions"] != 3 {
			t.Fatal("brief history collapsed or crossed owners", component)
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
			chunk, err := workspacecontinuity.DecodeChunk(bytes.NewReader(data), ref, "source-hq", "brief_history")
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range chunk.Records {
				value, err := DecodeContinuityRevision(record, "source-hq")
				if err != nil {
					t.Fatal(err)
				}
				seen[record.ID] = value
			}
		}
	}
	if len(seen) != 3 || seen["brief-one"].Revision.RevisionNumber != 1 || seen["brief-one"].Revision.IsCurrent || seen["brief-two"].Revision.RevisionNumber != 2 || !seen["brief-two"].Revision.IsCurrent || seen["brief-other-date"].Revision.LocalDate != "2026-08-13" {
		t.Fatal("revision identity, date, or current selection changed", seen)
	}
	if _, ok := seen["unrelated-workspace"]; ok {
		t.Fatal("other workspace leaked")
	}
	if _, err := SnapshotContinuityRevision(&Revision{ID: "bad", WorkspaceID: "source-hq", UserID: "local", LocalDate: "2026-08-12", RevisionNumber: 1, Trigger: TriggerManual, Status: GenerationSucceeded, ContentJSON: "not-json", CreatedAt: at}, "source-hq"); err == nil {
		t.Fatal("bad source content mislabeled as safe history")
	}
}
