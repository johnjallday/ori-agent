package projectlibrary

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestQuery_BoundedRevisionCursorAtThousandRecords(t *testing.T) {
	file, scope := libraryHome(t)
	s := NewStore(file)
	initializeLibrary(t, s, scope)
	now := time.Now().UTC()
	rootID, scanID := newID(), newID()
	_, _, err := s.mutate(scope, 1,
		operation{key: "query-fixture", action: "seed", digest: "fixture"},
		func(doc *Document) (string, error) {
			path := t.TempDir()
			doc.Roots = append(doc.Roots, Root{ID: rootID, Path: path, FileIdentity: "fixture:root",
				Revision: 1, ApprovedAt: now})
			doc.Scans = append(doc.Scans, Scan{ID: scanID, RootID: rootID, RootRevision: 1,
				RootDigest: rootDigest(scope, "scan_source", path, "fixture:root", 1),
				Status:     "complete", StartedAt: now, FinishedAt: &now,
				ResultDigest: strings.Repeat("0", 64)})
			for i := 0; i < 1000; i++ {
				fields := Fields{DisplayName: fmt.Sprintf("Song %04d", i), Revision: 0}
				if i < 40 {
					fields.Status, fields.Revision, fields.Source, fields.UpdatedAt = "active", 1, "test_edit", now
				}
				doc.Entries = append(doc.Entries, Entry{ID: fmt.Sprintf("entry-%04d", i), Revision: 1,
					Fields: fields, Observations: []Observation{{RootID: rootID,
						RelativeFolder: fmt.Sprintf("Folder-%04d", i), FileIdentity: fmt.Sprintf("fixture:%d", i),
						Format: "reaper", Availability: "available", ScanID: scanID, ScannedAt: now}}})
			}
			return scanID, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := workspace.NewFileStore(filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID))))
	if err != nil {
		t.Fatal(err)
	}
	s = NewStore(restarted)
	query := Search{PageSize: 50, Sort: "name"}
	// Measure one cold post-restart query, including file decode and all
	// metadata projections. TotalAlloc is process-wide, so this is diagnostic
	// evidence, not a platform-independent performance assertion.
	runtime.GC()
	var coldBefore, coldAfter runtime.MemStats
	runtime.ReadMemStats(&coldBefore)
	coldStarted := time.Now()
	page, err := s.Query(scope, query)
	coldElapsed := time.Since(coldStarted)
	runtime.ReadMemStats(&coldAfter)
	t.Logf("cold persisted query (1,000 rows, 40 edits): elapsed=%s allocated=%d bytes", coldElapsed, coldAfter.TotalAlloc-coldBefore.TotalAlloc)
	if err != nil || page.Total != 1000 || len(page.Rows) != 50 || page.NextCursor == "" ||
		page.Rows[0].Name != "Song 0000" || page.Rows[0].Status != "active" ||
		page.Rows[0].Stage != "unknown" || page.Rows[0].ActivitySource != "test_edit" {
		t.Fatalf("bounded first page: %+v %v", page, err)
	}
	encoded, err := json.Marshal(page)
	if err != nil || len(encoded) > 128<<10 {
		t.Fatalf("oversized page: %d bytes %v", len(encoded), err)
	}
	seen := map[string]bool{}
	var middleCursor, lastCursor string
	pageNumber := 0
	for page.NextCursor != "" {
		for _, row := range page.Rows {
			if seen[row.ID] {
				t.Fatalf("pagination repeated %s", row.ID)
			}
			seen[row.ID] = true
		}
		pageNumber++
		if pageNumber == 10 {
			middleCursor = page.NextCursor
		}
		if pageNumber == 19 {
			lastCursor = page.NextCursor
		}
		query.Cursor = page.NextCursor
		page, err = s.Query(scope, query)
		if err != nil || len(page.Rows) > 50 {
			t.Fatalf("next page: %+v %v", page, err)
		}
	}
	for _, row := range page.Rows {
		seen[row.ID] = true
	}
	if len(seen) != 1000 || middleCursor == "" || lastCursor == "" {
		t.Fatalf("pagination lost records or middle/last cursors: %d", len(seen))
	}
	var durations []time.Duration
	measured := []Search{{PageSize: 50}, {PageSize: 50, Cursor: middleCursor},
		{PageSize: 50, Cursor: lastCursor}, {Text: "no such project"}}
	for i := 0; i < 100; i++ {
		started := time.Now()
		if _, err := s.Query(scope, measured[i%len(measured)]); err != nil {
			t.Fatalf("measurement %d: %v", i, err)
		}
		durations = append(durations, time.Since(started))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("100 real persisted query requests (1,000 rows, 40 edits): p95=%s; first/middle/last/no-match", durations[94])
	for _, invalid := range []Search{{PageSize: 51}, {Text: strings.Repeat("x", 121)},
		{Sort: "../../secret"}, {Direction: "sideways"}, {Format: "unlisted"}, {RootID: "foreign"}} {
		if _, err := s.Query(scope, invalid); err == nil {
			t.Fatalf("unbounded or forged query accepted: %+v", invalid)
		}
	}
	filtered, err := s.Query(scope, Search{Status: "active", PageSize: 50})
	if err != nil || filtered.Total != 40 || len(filtered.Rows) != 40 || filtered.NextCursor != "" {
		t.Fatalf("40 edited rows not queryable: %+v %v", filtered, err)
	}
	unknown, err := s.Query(scope, Search{Status: "unknown", Text: "Song 042"})
	if err != nil || unknown.Total != 10 {
		t.Fatalf("unconfirmed state was inferred: %+v %v", unknown, err)
	}
	beforeMutation, err := s.Query(scope, Search{PageSize: 25})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ReviewFields(scope, "entry-0000", 1, FieldsPatch{NextAction: text("Record vocals")}, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Query(scope, Search{PageSize: 25, Cursor: beforeMutation.NextCursor}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale page silently skipped updated records: %v", err)
	}
	if _, err := s.Query(scope, Search{PageSize: 25, Cursor: beforeMutation.NextCursor + "x"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("tampered cursor accepted: %v", err)
	}
}

func TestQuery_ActiveObservationTakesPrecedenceOverNewerRevokedFormat(t *testing.T) {
	file, scope := libraryHome(t)
	s := NewStore(file)
	initializeLibrary(t, s, scope)
	base := t.TempDir()
	now := time.Now().UTC()
	_, _, err := s.mutate(scope, 1, operation{key: "query-overlap", action: "seed", digest: "fixture"},
		func(doc *Document) (string, error) {
			past := now.Add(-time.Hour)
			doc.Roots = append(doc.Roots,
				Root{ID: "active-root", Path: filepath.Join(base, "active"), FileIdentity: "active", Revision: 1, ApprovedAt: past},
				Root{ID: "revoked-root", Path: filepath.Join(base, "revoked"), FileIdentity: "revoked", Revision: 2, ApprovedAt: past, RevokedAt: &now})
			for _, rootID := range []string{"active-root", "revoked-root"} {
				doc.Scans = append(doc.Scans, Scan{ID: "scan-" + rootID, RootID: rootID, RootRevision: 1,
					RootDigest: strings.Repeat("0", 64), ResultDigest: strings.Repeat("0", 64),
					Status: "complete", StartedAt: past, FinishedAt: &now})
			}
			doc.Entries = append(doc.Entries, Entry{ID: "same-entry", Revision: 1, Fields: Fields{},
				Observations: []Observation{
					{RootID: "active-root", RelativeFolder: "Usable song", FileIdentity: "same:1", Format: "reaper",
						ScanID: "scan-active-root", ScannedAt: past, Availability: "available", Alternates: []string{"Song.rpp"}},
					{RootID: "revoked-root", RelativeFolder: "Old unrelated label", FileIdentity: "same:1", Format: "ableton",
						ScanID: "scan-revoked-root", ScannedAt: now, Availability: "available", Alternates: []string{"Old.als"}},
				}})
			return "same-entry", nil
		})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Query(scope, Search{Format: "reaper"})
	if err != nil || page.Total != 1 || page.Rows[0].Name != "Usable song" ||
		page.Rows[0].Format != "reaper" || page.Rows[0].Availability != "available" ||
		!page.Rows[0].LastScannedAt.Equal(now) {
		t.Fatalf("newer revoked observation replaced active metadata: %+v %v", page, err)
	}
	if old, err := s.Query(scope, Search{Format: "ableton"}); err != nil || old.Total != 0 {
		t.Fatalf("revoked source unexpectedly won format filter: %+v %v", old, err)
	}
	detail, err := s.Detail(scope, "same-entry")
	if err != nil || detail.Sources[0].RootID != "active-root" || detail.Sources[1].Availability != "revoked_source" {
		t.Fatalf("detail did not prioritize currently approved source: %+v %v", detail, err)
	}
}

func TestQuery_DetailBoundsValidManySourceAndEscapedAlternativePayloads(t *testing.T) {
	file, scope := libraryHome(t)
	s := NewStore(file)
	initializeLibrary(t, s, scope)
	base := t.TempDir()
	now := time.Now().UTC()
	_, _, err := s.mutate(scope, 1, operation{key: "detail-big", action: "seed", digest: "fixture"},
		func(doc *Document) (string, error) {
			entry := Entry{ID: "song-many-sources", Revision: 1, Fields: Fields{DisplayName: "Many versions"}}
			for i := 0; i < 12; i++ {
				rootID, scanID := fmt.Sprintf("root-%02d", i), fmt.Sprintf("scan-%02d", i)
				path := filepath.Join(base, rootID)
				doc.Roots = append(doc.Roots, Root{ID: rootID, Path: path, FileIdentity: rootID,
					Revision: 1, ApprovedAt: now})
				doc.Scans = append(doc.Scans, Scan{ID: scanID, RootID: rootID, RootRevision: 1,
					RootDigest: strings.Repeat("0", 64), Status: "complete", StartedAt: now,
					FinishedAt: &now, ResultDigest: strings.Repeat("0", 64)})
				alternates := make([]string, 64)
				for j := range alternates {
					alternates[j] = fmt.Sprintf("%02d-%s", j, strings.Repeat("<", 237))
				}
				entry.Observations = append(entry.Observations, Observation{RootID: rootID,
					RelativeFolder: fmt.Sprintf("Song-%02d-%s", i, strings.Repeat("<", 990)), FileIdentity: rootID,
					Format: "reaper", Availability: "available", ScanID: scanID, ScannedAt: now,
					Alternates: alternates})
			}
			doc.Entries = append(doc.Entries, entry)
			return entry.ID, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := s.Detail(scope, "song-many-sources")
	if err != nil || detail.TotalSources != 12 || len(detail.Sources) != maxDetailSources {
		t.Fatalf("oversized entry was not visibly bounded: %+v %v", detail, err)
	}
	encoded, err := json.Marshal(detail)
	if err != nil || len(encoded) > maxDetailBytes {
		t.Fatalf("detail exceeded wire budget: %d bytes %v", len(encoded), err)
	}
	t.Logf("bounded detail: %d bytes for %d observed sources, each with 64 escaped alternatives", len(encoded), detail.TotalSources)
	byteBudgetPruned := false
	for _, source := range detail.Sources {
		if source.TotalAlternates != 64 || len(source.Alternates) > maxDetailAlternates ||
			strings.Contains(string(encoded), base) {
			t.Fatalf("detail lost truncation evidence or leaked source paths: %+v", source)
		}
		byteBudgetPruned = byteBudgetPruned || len(source.Alternates) < maxDetailAlternates
	}
	if !byteBudgetPruned {
		t.Fatal("escaped source names did not exercise the actual wire-byte budget")
	}
	if detail.Sources[0].RootID != "root-00" {
		t.Fatalf("equally recent sources are not stable: %+v", detail.Sources[0])
	}
}

func TestQuery_ExactLinkedOwnerAndNoAbsolutePathProjection(t *testing.T) {
	file, scope, project := legacyMusicHome(t)
	s := NewStore(file)
	review, err := s.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CommitInitialize(scope, review.Token, "query-linked-init"); err != nil {
		t.Fatal(err)
	}
	page, err := s.Query(scope, Search{})
	if err != nil || page.Total != 1 || page.Rows[0].Name != project.Name ||
		page.Rows[0].Connection != "connected" || page.Rows[0].Status != "active" ||
		page.Rows[0].Availability != "not_scanned" {
		t.Fatalf("linked-only row: %+v %v", page, err)
	}
	detail, err := s.Detail(scope, page.Rows[0].ID)
	if err != nil || detail.Row.ID != page.Rows[0].ID || detail.Fields.Milestones[0].ID != "mix" ||
		len(detail.Sources) != 0 {
		t.Fatalf("exact link detail was not bounded: %+v %v", detail, err)
	}
	if _, err := s.Detail(scope, "foreign-entry"); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign entry detail leaked: %v", err)
	}
	payload, err := json.Marshal(detail)
	if err != nil || strings.Contains(string(payload), file.BasePath()) ||
		strings.Contains(string(payload), "project_library") || strings.Contains(string(payload), "link_id") {
		t.Fatalf("query leaked Home envelope/private path: %s %v", payload, err)
	}
	foreign := scope
	foreign.OwnerUserID = "other"
	if _, err := s.Query(foreign, Search{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("foreign owner queried private metadata: %v", err)
	}
	for _, direction := range []string{"asc", "desc"} {
		list, err := s.Query(scope, Search{Sort: "priority", Direction: direction})
		if err != nil || !sort.SliceIsSorted(list.Rows, func(i, j int) bool {
			return searchLess(list.Rows[i], list.Rows[j], "priority", direction)
		}) {
			t.Fatalf("non-deterministic priority sort %s: %v", direction, err)
		}
	}
}
