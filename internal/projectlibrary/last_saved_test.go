package projectlibrary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// setSaved gives a file the modification time a DAW save would leave.
func setSaved(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestReadDirectory_ReportsSaveTimeFromTheListingStat(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	saved := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	song := filepath.Join(tree.single, "Song.rpp")
	setSaved(t, song, saved)
	before := fileDigest(t, song)
	// The link's own time is now, newer than the song. It must stay a skipped
	// link and never date the song.
	if err := os.Symlink(song, filepath.Join(tree.single, "Link.rpp")); err != nil {
		t.Fatal(err)
	}
	rows, partial, err := r.ReadDirectory(context.Background(), scope, root.ID, "Single", 5)
	if err != nil || partial || len(rows) != 2 {
		t.Fatalf("listing: %+v %v %v", rows, partial, err)
	}
	byName := map[string]DirectoryRow{}
	for _, row := range rows {
		byName[row.Name] = row
	}
	info, err := os.Lstat(song)
	if err != nil {
		t.Fatal(err)
	}
	listed := byName["Song.rpp"]
	if !listed.ModifiedAt.Equal(saved) || !listed.ModifiedAt.Equal(info.ModTime()) || listed.ModifiedAt.Location() != time.UTC {
		t.Fatalf("listed save time %v, want %v in UTC", listed.ModifiedAt, saved)
	}
	if !byName["Link.rpp"].IsLink {
		t.Fatalf("symlink row lost its link flag: %+v", byName["Link.rpp"])
	}
	result, err := r.Discover(context.Background(), scope, root.ID)
	if err != nil || result.SkippedLinks != 1 {
		t.Fatalf("discovery: %+v %v", result, err)
	}
	for _, candidate := range result.Candidates {
		if candidate.RelativeFolder != "Single" {
			continue
		}
		if !slices.Equal(candidate.Alternates, []string{"Song.rpp"}) || !candidate.FileModifiedAt.Equal(saved) {
			t.Fatalf("the skipped link dated or joined the song: %+v", candidate)
		}
	}
	if after := fileDigest(t, song); after != before {
		t.Fatal("reading a save time changed the project file")
	}
}

func TestRecognizedProject_DatesTheSongByItsNewestPrimaryProjectFile(t *testing.T) {
	older := time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC)
	newer := older.Add(36 * time.Hour)
	newest := newer.Add(time.Hour)
	tokyo := time.FixedZone("JST", 9*60*60)
	for _, tc := range []struct {
		name   string
		rows   []DirectoryRow
		format string
		want   time.Time
	}{
		{"one file", []DirectoryRow{{Name: "Song.rpp", ModifiedAt: older}}, "reaper", older},
		{"two project files, newest wins", []DirectoryRow{
			{Name: "Take A.rpp", ModifiedAt: newer}, {Name: "Take B.RPP", ModifiedAt: older}}, "reaper", newer},
		{"mixed formats, only the primary format counts", []DirectoryRow{
			{Name: "Song.rpp", ModifiedAt: older}, {Name: "Song.als", ModifiedAt: newest}}, "reaper", older},
		{"bundle directory", []DirectoryRow{{Name: "Song.logicx", IsDir: true, ModifiedAt: newer}}, "logic", newer},
		{"backups, hidden, cloud, links and unreadable rows never count", []DirectoryRow{
			{Name: "Song.rpp", ModifiedAt: older}, {Name: "Song.rpp-bak", ModifiedAt: newest},
			{Name: ".Hidden.rpp", ModifiedAt: newest}, {Name: "Cloud.rpp.icloud", ModifiedAt: newest},
			{Name: "Link.rpp", IsLink: true, ModifiedAt: newest}, {Name: "Gone.rpp", Unreadable: true, ModifiedAt: newest}},
			"reaper", older},
		{"undated listing", []DirectoryRow{{Name: "Song.rpp"}}, "reaper", time.Time{}},
		{"stored in UTC", []DirectoryRow{{Name: "Song.rpp", ModifiedAt: newer.In(tokyo)}}, "reaper", newer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, ok := recognizedProject("Song", "identity", tc.rows)
			if !ok || candidate.Format != tc.format || !candidate.FileModifiedAt.Equal(tc.want) ||
				(!tc.want.IsZero() && candidate.FileModifiedAt.Location() != time.UTC) {
				t.Fatalf("candidate %+v, want %s saved %v", candidate, tc.format, tc.want)
			}
		})
	}
}

func TestReconcile_ObservationCarriesTheProjectFileSaveTime(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	song := filepath.Join(tree.single, "Song.rpp")
	first := time.Date(2026, time.May, 1, 9, 0, 0, 0, time.UTC)
	setSaved(t, song, first)
	reviewedRootScan(t, r, scope, root, "dated-first")
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	created, observation, ok := observationAt(doc, root.ID, "Single")
	if !ok || !observation.FileModifiedAt.Equal(first) {
		t.Fatalf("new entry: %+v %+v", created, observation)
	}

	resaved := first.Add(72 * time.Hour)
	setSaved(t, song, resaved)
	reviewedRootScan(t, r, scope, root, "dated-again")
	doc, err = r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	updated, observation, ok := observationAt(doc, root.ID, "Single")
	if !ok || updated.ID != created.ID || !observation.FileModifiedAt.Equal(resaved) {
		t.Fatalf("updated exact observation kept the old time: %+v %+v", updated, observation)
	}

	// A second, overlapping root sees the same physical folder: the entry gains
	// an appended observation that carries the time too.
	picker := r.picker.(*testRootPicker)
	if picker.path, err = filepath.EvalSymlinks(tree.single); err != nil {
		t.Fatal(err)
	}
	token, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, token, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	inner, _, err := r.Commit(scope, review.Token, "dated-inner-root")
	if err != nil {
		t.Fatal(err)
	}
	reviewedRootScan(t, r, scope, inner, "dated-inner-scan")
	doc, err = r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	shared, appended, ok := observationAt(doc, inner.ID, "")
	if !ok || shared.ID != created.ID || len(shared.Observations) != 2 || !appended.FileModifiedAt.Equal(resaved) {
		t.Fatalf("appended observation: %+v %+v", shared, appended)
	}
}

func TestDigest_AResavedSongIsNotUpdated(t *testing.T) {
	saved := time.Date(2026, time.April, 1, 8, 0, 0, 0, time.UTC)
	before := Observation{RootID: "root", RelativeFolder: "Song", FileIdentity: "1:2", Format: "reaper",
		Alternates: []string{"Song.rpp"}, Availability: "available", FileModifiedAt: saved}
	after := before
	after.FileModifiedAt = saved.Add(time.Hour)
	if observationChanged(before, after) {
		t.Fatal("a new save time alone counted as a changed song")
	}

	r, scope, _, tree, root := connectedMusicRoot(t)
	reviewedRootScan(t, r, scope, root, "before-resave")
	resaved := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	setSaved(t, filepath.Join(tree.single, "Song.rpp"), resaved)
	reviewedRootScan(t, r, scope, root, "after-resave")
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Digest == nil || doc.Digest.Projects != 4 || doc.Digest.New != 0 || doc.Digest.Updated != 0 {
		t.Fatalf("the scan digest counted a re-save: %+v", doc.Digest)
	}
	if _, observation, ok := observationAt(doc, root.ID, "Single"); !ok || !observation.FileModifiedAt.Equal(resaved) {
		t.Fatalf("re-save time not recorded: %+v", observation)
	}
}

func TestScans_ReplayedFinishWithTheSameDatedDiscoveryIsAReplay(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	events := &eventRecorder{}
	r.library.WithEventBus(events)
	setSaved(t, filepath.Join(tree.single, "Song.rpp"), time.Date(2026, time.February, 3, 4, 5, 6, 789, time.UTC))
	started := runningScanRecord(t, r, scope, root, "dated-start")
	observed, err := r.Discover(context.Background(), scope, root.ID)
	if err != nil || len(observed.Candidates) == 0 || observed.Candidates[0].FileModifiedAt.IsZero() {
		t.Fatalf("dated discovery: %+v %v", observed, err)
	}
	first, err := r.finishScan(scope, started, observed, "complete", "")
	if err != nil || first.Status != "complete" {
		t.Fatalf("first finish: %+v %v", first, err)
	}
	afterFirst, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	// A retry carries the same Discovery, times included, through JSON.
	encoded, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	var retried Discovery
	if err := json.Unmarshal(encoded, &retried); err != nil {
		t.Fatal(err)
	}
	if _, err := r.finishScan(scope, started, retried, "complete", ""); err != nil {
		t.Fatalf("same discovery was not a replay: %v", err)
	}
	afterReplay, err := r.library.Read(scope)
	if err != nil || afterReplay.Revision != afterFirst.Revision || len(events.all()) != 1 {
		t.Fatalf("replay wrote or published again: revision %d→%d events=%d %v",
			afterFirst.Revision, afterReplay.Revision, len(events.all()), err)
	}
	// A different time is a different result, never a silent overwrite.
	changed := observed
	changed.Candidates = slices.Clone(observed.Candidates)
	changed.Candidates[0].FileModifiedAt = changed.Candidates[0].FileModifiedAt.Add(time.Second)
	if _, err := r.finishScan(scope, started, changed, "complete", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("a different discovery under the same finish key: %v", err)
	}
}

func TestStore_AHomeSavedBeforeSaveTimesLoads(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	setSaved(t, filepath.Join(tree.single, "Song.rpp"), time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC))
	reviewedRootScan(t, r, scope, root, "pre-feature")
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	stored := home.GetAssistantProgramState().ProjectLibrary
	if !bytes.Contains(stored, []byte(`"file_modified_at"`)) || !bytes.Contains(stored, []byte(`"2026-07-01T00:00:00Z"`)) {
		t.Fatal("fixture did not record a save time")
	}
	// Rewrite the library the way an older build left it: no save time at all.
	decoder := json.NewDecoder(bytes.NewReader(stored))
	decoder.UseNumber()
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil {
		t.Fatal(err)
	}
	for _, entry := range raw["entries"].([]any) {
		for _, observation := range entry.(map[string]any)["observations"].([]any) {
			delete(observation.(map[string]any), "file_modified_at")
		}
	}
	legacy, err := json.Marshal(raw)
	if err != nil || strings.Contains(string(legacy), "file_modified_at") {
		t.Fatalf("legacy fixture: %v", err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.ProjectLibrary = legacy
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	store := NewStore(file)
	doc, err := store.Read(scope)
	if err != nil || !doc.valid(scope) || len(doc.Entries) != 4 {
		t.Fatalf("an older Home no longer loads: %v", err)
	}
	page, err := store.Query(scope, Search{Sort: "last_saved", Direction: "desc"})
	if err != nil || page.Total != 4 {
		t.Fatalf("query on an older Home: %+v %v", page, err)
	}
	for _, row := range page.Rows {
		if row.LastSavedAt != nil {
			t.Fatalf("an undated observation produced a date: %+v", row)
		}
	}
}

// seedLastSavedLibrary writes rows whose sources differ in availability and
// root state, so only the usable source can date each song.
func seedLastSavedLibrary(t *testing.T) (*Store, Scope, time.Time) {
	t.Helper()
	file, scope := libraryHome(t)
	s := NewStore(file)
	initializeLibrary(t, s, scope)
	base := t.TempDir()
	day := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	at := func(days int) time.Time { return day.AddDate(0, 0, days) }
	_, _, err := s.mutate(scope, 1, operation{key: "last-saved-fixture", action: "seed", digest: "fixture"},
		func(doc *Document) (string, error) {
			revoked := day
			for _, root := range []Root{
				{ID: "home-root", Path: filepath.Join(base, "home"), FileIdentity: "home", Revision: 1, ApprovedAt: at(-30)},
				{ID: "second-root", Path: filepath.Join(base, "second"), FileIdentity: "second", Revision: 1, ApprovedAt: at(-30)},
				{ID: "revoked-root", Path: filepath.Join(base, "revoked"), FileIdentity: "revoked", Revision: 2, ApprovedAt: at(-30), RevokedAt: &revoked},
				{ID: "paused-root", Path: filepath.Join(base, "paused"), FileIdentity: "paused", Revision: 1, ApprovedAt: at(-30)},
			} {
				doc.Roots = append(doc.Roots, root)
				finished := at(-30)
				doc.Scans = append(doc.Scans, Scan{ID: "scan-" + root.ID, RootID: root.ID, RootRevision: 1,
					RootDigest: strings.Repeat("0", 64), ResultDigest: strings.Repeat("0", 64),
					Status: "complete", StartedAt: finished, FinishedAt: &finished})
			}
			type source struct {
				root, availability string
				saved              time.Time
			}
			for _, record := range []struct {
				id      string
				sources []source
			}{
				{"two-sources", []source{{"home-root", "available", at(-3)}, {"second-root", "ambiguous", at(-1)}}},
				{"half-second", []source{{"home-root", "available", at(-1).Add(500 * time.Millisecond)}}},
				{"unavailable-newer", []source{{"home-root", "available", at(-5)}, {"second-root", "unavailable", at(0)}}},
				{"revoked-newer", []source{{"home-root", "available", at(-4)}, {"revoked-root", "available", at(0)}}},
				{"paused-newer", []source{{"home-root", "available", at(-6)}, {"paused-root", "available", at(0)}}},
				{"undated", []source{{"home-root", "available", time.Time{}}}},
				{"only-gone", []source{{"home-root", "unavailable", at(0)}}},
			} {
				entry := Entry{ID: record.id, Revision: 1, Fields: Fields{DisplayName: record.id}}
				for _, src := range record.sources {
					entry.Observations = append(entry.Observations, Observation{RootID: src.root,
						RelativeFolder: record.id, FileIdentity: "fixture:" + record.id, Format: "reaper",
						Alternates: []string{"Song.rpp"}, ScanID: "scan-" + src.root, ScannedAt: at(-30),
						FileModifiedAt: src.saved, Availability: src.availability})
				}
				doc.Entries = append(doc.Entries, entry)
			}
			return "last-saved-fixture", nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.ProjectLibraryInactiveRoots = []string{"paused-root"}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return s, scope, day
}

func TestQuery_LastSavedCountsOnlySourcesUsableNow(t *testing.T) {
	s, scope, day := seedLastSavedLibrary(t)
	page, err := s.Query(scope, Search{Sort: "last_saved", Direction: "desc", PageSize: 50})
	if err != nil || page.Total != 7 {
		t.Fatalf("last_saved query: %+v %v", page, err)
	}
	want := map[string]time.Time{
		"half-second":       day.AddDate(0, 0, -1).Add(500 * time.Millisecond),
		"two-sources":       day.AddDate(0, 0, -1), // ambiguous counts; newest of two usable sources
		"revoked-newer":     day.AddDate(0, 0, -4), // the revoked root's newer save is ignored
		"unavailable-newer": day.AddDate(0, 0, -5), // the unavailable source's newer save is ignored
		"paused-newer":      day.AddDate(0, 0, -6), // the inactive root's newer save is ignored
	}
	var order []string
	for _, row := range page.Rows {
		order = append(order, row.ID)
		expected, dated := want[row.ID]
		switch {
		case dated && (row.LastSavedAt == nil || !row.LastSavedAt.Equal(expected)):
			t.Fatalf("%s saved %v, want %v", row.ID, row.LastSavedAt, expected)
		case !dated && row.LastSavedAt != nil:
			t.Fatalf("%s has no usable dated source but shows %v", row.ID, row.LastSavedAt)
		}
	}
	descending := []string{"half-second", "two-sources", "revoked-newer", "unavailable-newer", "paused-newer", "only-gone", "undated"}
	if !slices.Equal(order, descending) {
		t.Fatalf("descending order %v, want undated rows last: %v", order, descending)
	}
	encoded, err := json.Marshal(page.Rows[len(page.Rows)-1])
	if err != nil || strings.Contains(string(encoded), "last_saved_at") {
		t.Fatalf("an undated row serialized a date: %s %v", encoded, err)
	}
	encoded, err = json.Marshal(page.Rows[0])
	if err != nil || !strings.Contains(string(encoded), `"last_saved_at":"2026-05-31T12:00:00.5Z"`) {
		t.Fatalf("a dated row: %s %v", encoded, err)
	}
}

func TestQuery_LastSavedCursorPagesAcrossThePageBoundary(t *testing.T) {
	s, scope, _ := seedLastSavedLibrary(t)
	for _, tc := range []struct {
		direction string
		want      []string
	}{
		{"desc", []string{"half-second", "two-sources", "revoked-newer", "unavailable-newer", "paused-newer", "only-gone", "undated"}},
		{"asc", []string{"only-gone", "undated", "paused-newer", "unavailable-newer", "revoked-newer", "two-sources", "half-second"}},
	} {
		t.Run(tc.direction, func(t *testing.T) {
			query := Search{Sort: "last_saved", Direction: tc.direction, PageSize: 2}
			var got []string
			for pages := 0; pages < 10; pages++ {
				page, err := s.Query(scope, query)
				if err != nil || page.Total != 7 {
					t.Fatalf("page %d: %+v %v", pages, page, err)
				}
				for _, row := range page.Rows {
					got = append(got, row.ID)
				}
				if page.NextCursor == "" {
					break
				}
				query.Cursor = page.NextCursor
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("paged %s order %v, want %v", tc.direction, got, tc.want)
			}
		})
	}
	for _, unknown := range []string{"last_saved_at", "modified", "LAST_SAVED"} {
		if _, err := s.Query(scope, Search{Sort: unknown}); !errors.Is(err, ErrConflict) {
			t.Fatalf("unknown sort %q accepted: %v", unknown, err)
		}
	}
}
