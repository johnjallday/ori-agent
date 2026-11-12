package projectlibrary

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Repeated query timing (the former 100-request p95 loop) is not run here; it is
// BenchmarkQuery_PersistedThousandRecords. This test keeps what is correctness:
// every record is reachable exactly once, a cursor is revision-bound, and the
// first, middle, last and no-match shapes each return what they should.
func TestQuery_BoundedRevisionCursorAtThousandRecords(t *testing.T) {
	fixture := newThousandRecordFixture(t)
	scope := fixture.scope
	s, closeStore := fixture.open(t)
	t.Cleanup(closeStore)
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
	// The four request shapes the persisted-query benchmark measures, once each
	// and with real assertions (the default sort is name, so the rows are known):
	// the first page, the pages that start at the middle and last cursors, and a
	// search that matches nothing.
	first, err := s.Query(scope, Search{PageSize: 50})
	if err != nil || first.Total != 1000 || len(first.Rows) != 50 || first.Rows[0].Name != "Song 0000" || first.NextCursor == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	middle, err := s.Query(scope, Search{PageSize: 50, Cursor: middleCursor})
	if err != nil || len(middle.Rows) != 50 || middle.Rows[0].Name != "Song 0500" || middle.NextCursor == "" {
		t.Fatalf("middle page: %+v %v", middle, err)
	}
	final, err := s.Query(scope, Search{PageSize: 50, Cursor: lastCursor})
	if err != nil || len(final.Rows) != 50 || final.Rows[0].Name != "Song 0950" || final.NextCursor != "" {
		t.Fatalf("last page: %+v %v", final, err)
	}
	none, err := s.Query(scope, Search{Text: "no such project"})
	if err != nil || none.Total != 0 || len(none.Rows) != 0 || none.NextCursor != "" {
		t.Fatalf("no-match search: %+v %v", none, err)
	}
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

func TestQuery_TimestampSortAndCursorFollowChronologyAcrossFractionalSeconds(t *testing.T) {
	file, scope := libraryHome(t)
	s := NewStore(file)
	initializeLibrary(t, s, scope)
	whole := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)
	half, next := whole.Add(500*time.Millisecond), whole.Add(time.Second)
	_, _, err := s.mutate(scope, 1, operation{key: "time-order-fixture", action: "seed", digest: "fixture"},
		func(doc *Document) (string, error) {
			rootID, scanID := "timestamp-root", "timestamp-scan"
			doc.Roots = append(doc.Roots, Root{ID: rootID, Path: t.TempDir(), FileIdentity: "fixture:root", Revision: 1, ApprovedAt: whole})
			doc.Scans = append(doc.Scans, Scan{ID: scanID, RootID: rootID, RootRevision: 1,
				RootDigest: strings.Repeat("a", 64), ResultDigest: strings.Repeat("b", 64),
				Status: "complete", StartedAt: whole, FinishedAt: &next})
			for _, record := range []struct {
				id string
				at time.Time
			}{{"whole", whole}, {"half", half}, {"next", next}} {
				doc.Entries = append(doc.Entries, Entry{ID: record.id, Revision: 1,
					Fields: Fields{DisplayName: record.id, Revision: 1, Source: "user_edit", UpdatedAt: record.at},
					Observations: []Observation{{RootID: rootID, RelativeFolder: record.id, FileIdentity: "fixture:" + record.id,
						Format: "reaper", ScanID: scanID, ScannedAt: record.at, Availability: "available"}}})
			}
			return scanID, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	for _, sortField := range []string{"scanned_at", "sourced_activity"} {
		for _, tc := range []struct {
			direction string
			want      []string
		}{{"asc", []string{"whole", "half", "next"}}, {"desc", []string{"next", "half", "whole"}}} {
			t.Run(sortField+"_"+tc.direction, func(t *testing.T) {
				query := Search{Sort: sortField, Direction: tc.direction, PageSize: 1}
				var got []string
				for {
					page, err := s.Query(scope, query)
					if err != nil || page.Total != 3 || len(page.Rows) != 1 {
						t.Fatalf("chronology page %d: %+v %v", len(got), page, err)
					}
					got = append(got, page.Rows[0].ID)
					if page.NextCursor == "" {
						break
					}
					query.Cursor = page.NextCursor
				}
				if !slices.Equal(got, tc.want) {
					t.Fatalf("%s %s sorts by text instead of observation time: got %v, want %v", sortField, tc.direction, got, tc.want)
				}
			})
		}
	}
	// A cursor minted with the old variable-width timestamp layout is not
	// silently resumed in a different order after an upgrade.
	doc, err := s.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	query := Search{Sort: "scanned_at", Direction: "asc", PageSize: 1}
	if err := query.normalize(doc); err != nil {
		t.Fatal(err)
	}
	oldCursor, err := json.Marshal(pageCursor{Revision: doc.Revision, Query: queryDigest(query),
		LastID: "whole", LastKey: whole.Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	query.Cursor = base64.RawURLEncoding.EncodeToString(oldCursor)
	if _, err := s.Query(scope, query); !errors.Is(err, ErrConflict) {
		t.Fatalf("old timestamp cursor resumed with the wrong ordering: %v", err)
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
	for _, tc := range []struct {
		root, format, availability, name string
		scannedAt                        time.Time
	}{
		{"active-root", "reaper", "available", "Usable song", now.Add(-time.Hour)},
		{"revoked-root", "ableton", "revoked_source", "Old unrelated label", now},
	} {
		t.Run(tc.root, func(t *testing.T) {
			query := Search{RootID: tc.root, Format: tc.format, Availability: tc.availability}
			page, err := s.Query(scope, query)
			if err != nil || page.Total != 1 || len(page.Rows) != 1 || page.Rows[0].Format != tc.format ||
				page.Rows[0].Availability != tc.availability || page.Rows[0].Name != tc.name ||
				!page.Rows[0].LastScannedAt.Equal(tc.scannedAt) {
				t.Fatalf("root-filtered row combined another root's observation: %+v %v", page, err)
			}
		})
	}
	if wrong, err := s.Query(scope, Search{RootID: "revoked-root", Format: "reaper"}); err != nil || wrong.Total != 0 {
		t.Fatalf("revoked root inherited another root's format: %+v %v", wrong, err)
	}
	if wrong, err := s.Query(scope, Search{RootID: "revoked-root", Availability: "available"}); err != nil || wrong.Total != 0 {
		t.Fatalf("revoked root inherited another root's availability: %+v %v", wrong, err)
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

func TestQuery_DetailWorkspaceNavigationRequiresCurrentExactLink(t *testing.T) {
	library, scope, _ := createBrowserFixture(t, t.TempDir())
	for entryID, childID := range map[string]string{
		"browser-000": "browser-child-0", "browser-001": "browser-child-1",
		"browser-002": "", "browser-003": "",
	} {
		detail, err := library.Detail(scope, entryID)
		if err != nil || detail.WorkspaceID != childID {
			t.Fatalf("navigation for %s: %+v %v", entryID, detail, err)
		}
	}
	if err := library.workspaces.Update("browser-child-0", func(child *workspace.Workspace) error {
		child.OwnerUserID = "foreign-owner"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	detail, err := library.Detail(scope, "browser-000")
	if err != nil || detail.WorkspaceID != "" || detail.Row.Connection != "needs_review" {
		t.Fatalf("changed owner leaked a navigation target: %+v %v", detail, err)
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
		len(detail.Sources) != 0 || detail.WorkspaceID != project.ID {
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
