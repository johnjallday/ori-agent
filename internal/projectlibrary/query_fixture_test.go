package projectlibrary

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// thousandRecordFixture is a persisted library of 1,000 catalog entries, 40 of
// them edited (active, sourced from "test_edit"), named "Song 0000" to
// "Song 0999". It is shared by the correctness test and the benchmark so both
// read the same data.
type thousandRecordFixture struct {
	scope Scope
	root  string
}

// open returns a Store over a newly opened file store on the fixture's files:
// what a process restart sees, with nothing decoded yet. The caller owns the
// store and calls the returned function exactly once to release it. The
// failure goes to the tb the caller passes, so a sub-benchmark reports its own.
func (f thousandRecordFixture) open(tb testing.TB) (*Store, func()) {
	tb.Helper()
	opened, err := workspace.NewFileStore(f.root)
	if err != nil {
		tb.Fatal(err)
	}
	return NewStore(opened), func() {
		if err := opened.Close(); err != nil {
			tb.Errorf("close the reopened file store: %v", err)
		}
	}
}

func newThousandRecordFixture(tb testing.TB) thousandRecordFixture {
	tb.Helper()
	file, scope := libraryHome(tb)
	s := NewStore(file)
	initializeLibrary(tb, s, scope)
	now := time.Now().UTC()
	rootID, scanID := newID(), newID()
	_, _, err := s.mutate(scope, 1,
		operation{key: "query-fixture", action: "seed", digest: "fixture"},
		func(doc *Document) (string, error) {
			path := tb.TempDir()
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
		tb.Fatal(err)
	}
	return thousandRecordFixture{scope: scope, root: filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID)))}
}

// The benchmark's workload is only meaningful if the fixture and cursors are
// what it assumes, so that is checked here, in the ordinary run, not left to a
// benchmark nobody runs by default.
func TestThousandRecordFixtureAndCursorsAreWhatTheBenchmarkAssumes(t *testing.T) {
	fixture := newThousandRecordFixture(t)
	s, closeStore := fixture.open(t)
	t.Cleanup(closeStore)
	all, err := s.Query(fixture.scope, Search{PageSize: 50})
	if err != nil || all.Total != 1000 || len(all.Rows) != 50 {
		t.Fatalf("the fixture must hold 1,000 rows: %+v %v", all, err)
	}
	edited, err := s.Query(fixture.scope, Search{Status: "active", PageSize: 50})
	if err != nil || edited.Total != 40 {
		t.Fatalf("the fixture must hold 40 edited rows: %+v %v", edited, err)
	}
	middle, last := pageCursors(t, s, fixture.scope)
	for _, tc := range []struct {
		name, cursor, first string
		hasNext             bool
	}{{"middle", middle, "Song 0500", true}, {"last", last, "Song 0950", false}} {
		page, err := s.Query(fixture.scope, Search{PageSize: 50, Cursor: tc.cursor})
		if err != nil || len(page.Rows) != 50 || page.Rows[0].Name != tc.first || (page.NextCursor != "") != tc.hasNext {
			t.Fatalf("%s cursor must start at %s: %+v %v", tc.name, tc.first, page, err)
		}
	}
	// A second store opened over the same files (a restart) reads the same persisted data.
	again, closeAgain := fixture.open(t)
	t.Cleanup(closeAgain)
	if got, err := again.Query(fixture.scope, Search{PageSize: 50}); err != nil || got.Total != 1000 {
		t.Fatalf("a reopened store must read the same 1,000 rows: %+v %v", got, err)
	}
}

// pageCursors walks the name-sorted pages of 50 and returns the cursor that
// starts the eleventh page (after row 499) and the one that starts the
// twentieth, last, page (after row 949).
func pageCursors(tb testing.TB, s *Store, scope Scope) (middle, last string) {
	tb.Helper()
	query := Search{PageSize: 50, Sort: "name"}
	for pageNumber := 1; ; pageNumber++ {
		page, err := s.Query(scope, query)
		if err != nil {
			tb.Fatal(err)
		}
		switch pageNumber {
		case 10:
			middle = page.NextCursor
		case 19:
			last = page.NextCursor
		}
		if page.NextCursor == "" {
			break
		}
		query.Cursor = page.NextCursor
	}
	if middle == "" || last == "" {
		tb.Fatal("the walk produced no middle or last cursor")
	}
	return middle, last
}
