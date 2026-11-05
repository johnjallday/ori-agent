package projectlibrary

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Library upkeep: a song whose folder moved inside approved folders keeps its
// record (PRD tasks/prd-library-upkeep.md FR7-FR14).

func setHumanFields(t *testing.T, r *Roots, scope Scope, entryID string, fields Fields) {
	t.Helper()
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.library.mutate(scope, doc.Revision,
		operation{key: "fields-" + newID(), action: "edit_fields", digest: "human"},
		func(current *Document) (string, error) {
			for i := range current.Entries {
				if current.Entries[i].ID == entryID {
					fields.Revision = current.Entries[i].Fields.Revision + 1
					fields.Source, fields.UpdatedAt = "human", time.Now().UTC()
					current.Entries[i].Fields = fields
					current.Entries[i].Revision++
					return entryID, nil
				}
			}
			return "", ErrConflict
		}); err != nil {
		t.Fatal(err)
	}
}

func entryByID(doc Document, id string) (Entry, bool) {
	for _, entry := range doc.Entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return Entry{}, false
}

func scopeIDFor(t *testing.T, doc Document, rootID, relative string) string {
	t.Helper()
	for i := len(doc.Scans) - 1; i >= 0; i-- {
		if doc.Scans[i].RootID != rootID {
			continue
		}
		for _, known := range doc.Scans[i].KnownScopes {
			if known.RelativeFolder == relative {
				return known.ID
			}
		}
	}
	t.Fatalf("no known scope %q", relative)
	return ""
}

func scopedScan(t *testing.T, r *Roots, scope Scope, rootID, scopeID, key string) Scan {
	t.Helper()
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.ReviewScopedScan(scope, rootID, scopeID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := r.CommitScopedScan(context.Background(), scope, rootID, scopeID, review.Token, key)
	if err != nil || result.Status != "complete" {
		t.Fatalf("scoped scan: %+v %v", result, err)
	}
	return result
}

func connectRoot(t *testing.T, r *Roots, scope Scope, path, key string) Root {
	t.Helper()
	picker := r.picker.(*testRootPicker)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	picker.path = resolved
	token, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, token, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := r.Commit(scope, review.Token, key)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRelink_RenamedFolderKeepsItsRecordFieldsAndHistory(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	reviewedRootScan(t, r, scope, root, "relink-initial")
	before, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	song, _, ok := observationAt(before, root.ID, "Single")
	if !ok {
		t.Fatal("fixture song missing")
	}
	priority := 1
	setHumanFields(t, r, scope, song.ID, Fields{Stage: "mixing", Priority: &priority})
	if err := os.Rename(tree.single, filepath.Join(tree.root, "Night Drive")); err != nil {
		t.Fatal(err)
	}
	// Edited between the move and the scan: fields live on the entry and stay.
	setHumanFields(t, r, scope, song.ID, Fields{Stage: "mastering", Priority: &priority, NextAction: "Bounce"})
	scan := reviewedRootScan(t, r, scope, root, "relink-after-rename")
	after, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Entries) != len(before.Entries) {
		t.Fatalf("a rename created or dropped a record: %d -> %d", len(before.Entries), len(after.Entries))
	}
	moved, observation, ok := observationAt(after, root.ID, "Night Drive")
	if !ok || moved.ID != song.ID || moved.Fields.Stage != "mastering" || moved.Fields.NextAction != "Bounce" ||
		moved.Fields.Priority == nil || *moved.Fields.Priority != 1 {
		t.Fatalf("renamed song lost its record or fields: %+v", moved)
	}
	if observation.MovedFrom == nil || *observation.MovedFrom != (MovedFrom{RootID: root.ID, RelativeFolder: "Single", ScanID: scan.ID}) ||
		observation.ScanID != scan.ID || observation.Availability != "available" || len(moved.Observations) != 1 {
		t.Fatalf("moved note: %+v", observation)
	}
	if after.Digest == nil || after.Digest.Moved != 1 || after.Digest.Unavailable != 0 || after.Digest.New != 0 ||
		after.Digest.RelinkConflicts != 0 {
		t.Fatalf("digest: %+v", after.Digest)
	}
	// The note survives a restart: a reopened store reads moved_from.
	folderPath, err := file.GetFolderPath(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := workspace.NewFileStore(filepath.Dir(folderPath))
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewStore(reopened).Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, again, ok := observationAt(restarted, root.ID, "Night Drive"); !ok || again.MovedFrom == nil ||
		again.MovedFrom.RelativeFolder != "Single" {
		t.Fatalf("restart lost moved_from: %+v", again)
	}
	// The next complete scan sees the folder where it is: no move, note cleared.
	reviewedRootScan(t, r, scope, root, "relink-settled")
	settled, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, quiet, ok := observationAt(settled, root.ID, "Night Drive"); !ok || quiet.MovedFrom != nil || settled.Digest.Moved != 0 {
		t.Fatalf("note outlived the next scan: %+v %+v", quiet, settled.Digest)
	}
}

// FR12: the shelf row and the detail carry the move note only while the
// root's newest complete scan is the one that found the move.
func TestRelink_ShelfRowAndDetailShowTheMoveUntilTheNextScan(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	reviewedRootScan(t, r, scope, root, "note-initial")
	if err := os.Rename(tree.single, filepath.Join(tree.root, "Night Drive")); err != nil {
		t.Fatal(err)
	}
	reviewedRootScan(t, r, scope, root, "note-moved")
	rowFor := func(query Search) SearchRow {
		t.Helper()
		query.PageSize, query.Sort, query.Text = 25, "name", "Night Drive"
		page, err := r.library.Query(scope, query)
		if err != nil || len(page.Rows) != 1 {
			t.Fatalf("query: %+v %v", page, err)
		}
		return page.Rows[0]
	}
	row := rowFor(Search{})
	want := RowMove{RootID: root.ID, RelativeFolder: "Night Drive", FromRootID: root.ID, FromFolder: "Single"}
	if row.Moved == nil || *row.Moved != want {
		t.Fatalf("row move note: %+v", row.Moved)
	}
	if filtered := rowFor(Search{RootID: root.ID}); filtered.Moved == nil {
		t.Fatal("a root filter on the song's own root hid its note")
	}
	detail, err := r.library.Detail(scope, row.ID)
	if err != nil || detail.Row.Moved == nil || *detail.Row.Moved != want {
		t.Fatalf("detail move note: %+v %v", detail.Row.Moved, err)
	}
	encoded, err := json.Marshal(row)
	if err != nil || strings.Contains(string(encoded), tree.root) {
		t.Fatalf("row leaks an absolute path: %s", encoded)
	}
	// The next complete scan of the root: the note is gone, the record stays.
	reviewedRootScan(t, r, scope, root, "note-next")
	if again := rowFor(Search{}); again.Moved != nil || again.ID != row.ID {
		t.Fatalf("note outlived the next complete scan: %+v", again)
	}
}

// A root scan reads songs one folder deep, so a song moved into a new
// subfolder is first "no longer found"; the scan of that subfolder then
// relinks the record an earlier scan already marked unavailable.
func TestRelink_MoveIntoSubfolderRelinksWhenTheSubfolderIsScanned(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	reviewedRootScan(t, r, scope, root, "sub-initial")
	before, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	song, _, ok := observationAt(before, root.ID, "Single")
	if !ok {
		t.Fatal("fixture song missing")
	}
	released := filepath.Join(tree.root, "Released")
	if err := os.Mkdir(released, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tree.single, filepath.Join(released, "Single")); err != nil {
		t.Fatal(err)
	}
	reviewedRootScan(t, r, scope, root, "sub-root-rescan")
	marked, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if entry, _ := entryByID(marked, song.ID); entry.Observations[0].Availability != "unavailable" || marked.Digest.Moved != 0 {
		t.Fatalf("a song outside the root scan's reach was relinked or kept available: %+v", entry)
	}
	scan := scopedScan(t, r, scope, root.ID, scopeIDFor(t, marked, root.ID, "Released"), "sub-scoped")
	after, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	moved, observation, ok := observationAt(after, root.ID, filepath.Join("Released", "Single"))
	if !ok || moved.ID != song.ID || len(after.Entries) != len(before.Entries) ||
		observation.MovedFrom == nil || observation.MovedFrom.RelativeFolder != "Single" || observation.MovedFrom.ScanID != scan.ID {
		t.Fatalf("subfolder move was not relinked: %+v %+v", moved, observation)
	}
	if after.Digest.Moved != 1 || after.Digest.New != 0 {
		t.Fatalf("scoped digest: %+v", after.Digest)
	}
}

func TestRelink_MoveAcrossApprovedRootsKeepsTheRecord(t *testing.T) {
	r, scope, _, tree, first := connectedMusicRoot(t)
	reviewedRootScan(t, r, scope, first, "cross-initial")
	before, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	song, _, ok := observationAt(before, first.ID, "Single")
	if !ok {
		t.Fatal("fixture song missing")
	}
	other := filepath.Join(filepath.Dir(tree.root), "Demos")
	if err := os.Mkdir(other, 0o750); err != nil {
		t.Fatal(err)
	}
	second := connectRoot(t, r, scope, other, "cross-second-root")
	if err := os.Rename(tree.single, filepath.Join(other, "Single")); err != nil {
		t.Fatal(err)
	}
	// The second root is scanned first: the prior is still "available" in the
	// first root, but its folder is gone from there.
	reviewedRootScan(t, r, scope, second, "cross-second-scan")
	after, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	moved, observation, ok := observationAt(after, second.ID, "Single")
	if !ok || moved.ID != song.ID || len(moved.Observations) != 1 || observation.MovedFrom == nil ||
		observation.MovedFrom.RootID != first.ID || len(after.Entries) != len(before.Entries) {
		t.Fatalf("cross-root move: %+v %+v", moved, observation)
	}
	// Rescanning the first root does not resurrect or mark it.
	reviewedRootScan(t, r, scope, first, "cross-first-rescan")
	settled, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if entry, _ := entryByID(settled, song.ID); len(entry.Observations) != 1 || entry.Observations[0].RootID != second.ID ||
		entry.Observations[0].Availability != "available" || len(settled.Entries) != len(before.Entries) {
		t.Fatalf("first-root rescan disturbed the moved song: %+v", entry)
	}
}

func TestRelink_DeletedAndReplacedFoldersAreNeverRelinked(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	reviewedRootScan(t, r, scope, root, "gone-initial")
	before, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	song, _, _ := observationAt(before, root.ID, "Single")
	logic, _, _ := observationAt(before, root.ID, "Logic")
	// Deleted for good.
	if err := os.RemoveAll(tree.logic); err != nil {
		t.Fatal(err)
	}
	// Moved out of every approved folder (kept alive so its identity is not
	// reused), then a new folder of the same name with a different project file.
	if err := os.Rename(tree.single, filepath.Join(filepath.Dir(tree.root), "Outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(tree.single, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree.single, "Other.rpp"), []byte("another song"), 0o600); err != nil {
		t.Fatal(err)
	}
	reviewedRootScan(t, r, scope, root, "gone-rescan")
	after, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{song.ID, logic.ID} {
		entry, ok := entryByID(after, id)
		if !ok || entry.Observations[0].Availability != "unavailable" || entry.Observations[0].MovedFrom != nil {
			t.Fatalf("a deleted or moved-out song was relinked or dropped: %+v", entry)
		}
	}
	replacement, _, ok := observationAt(after, root.ID, "Single")
	if !ok || replacement.ID == song.ID || replacement.Fields.Revision != 0 {
		t.Fatalf("same-name replacement inherited the record: %+v", replacement)
	}
	if after.Digest.Moved != 0 || after.Digest.Unavailable != 2 || after.Digest.New != 1 {
		t.Fatalf("digest: %+v", after.Digest)
	}
}

// On ext4/tmpfs a folder made right after a delete usually gets the deleted
// folder's inode number, so its identity equals the old song's. A different
// project-file name still keeps it a new song (FR7). On APFS the identity
// differs anyway; the assertion holds on both.
func TestRelink_ReusedIdentityWithAnotherProjectFileIsANewSong(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	reviewedRootScan(t, r, scope, root, "reuse-initial")
	before, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	song, old, _ := observationAt(before, root.ID, "Single")
	if err := os.RemoveAll(tree.single); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(tree.root, "Fresh")
	if err := os.Mkdir(fresh, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fresh, "Other.rpp"), []byte("a different song"), 0o600); err != nil {
		t.Fatal(err)
	}
	reviewedRootScan(t, r, scope, root, "reuse-rescan")
	after, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	created, observation, ok := observationAt(after, root.ID, "Fresh")
	if !ok || created.ID == song.ID || observation.MovedFrom != nil || after.Digest.Moved != 0 {
		t.Fatalf("a reused identity with another project file was relinked: %+v", created)
	}
	t.Logf("identity reused by the new folder: %v", observation.FileIdentity == old.FileIdentity)
	if entry, _ := entryByID(after, song.ID); entry.Observations[0].Availability != "unavailable" {
		t.Fatalf("deleted song is not unavailable: %+v", entry)
	}
}

func TestRelink_LinkedSongKeepsItsLinkAndTheWorkspaceIsUntouched(t *testing.T) {
	r, scope, file, tree, root := connectedMusicRoot(t)
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	child := &workspace.Workspace{ID: "linked-song", Name: "Linked", OwnerUserID: scope.OwnerUserID,
		Status: workspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	link := &workspace.AssistantProjectLink{ID: workspace.AssistantProjectLinkID(scope.HomeID, child.ID),
		StationWorkspaceID: scope.HomeID, Key: key, StateRevision: 1}
	child.SetAssistantProjectLink(link)
	if err := projectconnection.RecordAttachedProject(child, "Song", tree.single, "Song.rpp", "approved-directory"); err != nil {
		t.Fatal(err)
	}
	if err := file.Save(child); err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.LinkedProjectIDs = append(state.LinkedProjectIDs, child.ID)
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	linkedID := newID()
	if _, _, err := r.library.mutate(scope, doc.Revision, operation{key: "linked", action: "edit_fields", digest: "saved"},
		func(current *Document) (string, error) {
			current.Entries = append(current.Entries, Entry{ID: linkedID, Revision: 1,
				Link:   &ExactLink{WorkspaceID: child.ID, LinkID: link.ID, Revision: link.StateRevision},
				Fields: Fields{DisplayName: "Linked song", Revision: 1}})
			return linkedID, nil
		}); err != nil {
		t.Fatal(err)
	}
	reviewedRootScan(t, r, scope, root, "linked-adopt")
	childBefore, err := json.Marshal(mustGet(t, file, child.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tree.single, filepath.Join(tree.root, "Renamed")); err != nil {
		t.Fatal(err)
	}
	reviewedRootScan(t, r, scope, root, "linked-rename")
	after, err := r.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	entry, observation, ok := observationAt(after, root.ID, "Renamed")
	if !ok || entry.ID != linkedID || entry.Link == nil || entry.Link.WorkspaceID != child.ID ||
		observation.MovedFrom == nil || after.Digest.Moved != 1 || after.Digest.Connected != 1 {
		t.Fatalf("linked song was not relinked with its link: %+v %+v", entry, after.Digest)
	}
	childAfter, err := json.Marshal(mustGet(t, file, child.ID))
	if err != nil {
		t.Fatal(err)
	}
	if string(childBefore) != string(childAfter) {
		t.Fatal("a relink changed the linked workspace")
	}
}

func mustGet(t *testing.T, store workspace.Store, id string) *workspace.Workspace {
	t.Helper()
	ws, err := store.Get(id)
	if err != nil || ws == nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return ws
}

// The match rule on synthetic documents: every prior here is already
// unavailable, so no disk check is involved.
func TestRelink_MatchNeedsIdentityFormatAndANameAndNeverGuesses(t *testing.T) {
	now := time.Now().UTC()
	roots := []Root{{ID: "a", Path: "/music/a"}, {ID: "b", Path: "/music/b"}}
	prior := func(entry, root, folder, identity, format string, names ...string) Entry {
		return Entry{ID: entry, Revision: 1, Observations: []Observation{{RootID: root, RelativeFolder: folder,
			FileIdentity: identity, Format: format, Alternates: names, ScanID: "old", ScannedAt: now,
			Availability: "unavailable"}}}
	}
	candidate := Candidate{RelativeFolder: "New Name", FileIdentity: "id-1", Format: "reaper", Alternates: []string{"Song.rpp"}}
	discovery := func(candidates ...Candidate) Discovery {
		return Discovery{RootID: "a", StartedAt: now, Candidates: candidates}
	}
	plan := func(entries []Entry, observed Discovery) relinkPlan {
		keys := map[string]bool{}
		for _, c := range observed.Candidates {
			keys[c.RelativeFolder+"\x00"+c.FileIdentity] = true
		}
		return planRelinks(Document{Roots: roots, Entries: entries}, Scan{ID: "new", RootID: "a"}, observed, roots[0], keys)
	}
	cases := map[string]struct {
		entries   []Entry
		observed  Discovery
		relinked  bool
		conflicts int
	}{
		"all three hold": {[]Entry{prior("e1", "a", "Old", "id-1", "reaper", "Take.rpp", "Song.rpp")},
			discovery(candidate), true, 0},
		"identity reused, different file name": {[]Entry{prior("e1", "a", "Old", "id-1", "reaper", "Other.rpp")},
			discovery(candidate), false, 0},
		"same name, different identity": {[]Entry{prior("e1", "a", "Old", "id-2", "reaper", "Song.rpp")},
			discovery(candidate), false, 0},
		"different format": {[]Entry{prior("e1", "a", "Old", "id-1", "ableton", "Song.rpp")},
			discovery(candidate), false, 0},
		"two priors for one folder": {[]Entry{prior("e1", "a", "Old", "id-1", "reaper", "Song.rpp"),
			prior("e2", "b", "Older", "id-1", "reaper", "Song.rpp")}, discovery(candidate), false, 1},
		"one prior for two folders": {[]Entry{prior("e1", "a", "Old", "id-1", "reaper", "Song.rpp")},
			discovery(candidate, Candidate{RelativeFolder: "Copy", FileIdentity: "id-1", Format: "reaper",
				Alternates: []string{"Song.rpp"}}), false, 2},
	}
	for name, tc := range cases {
		got := plan(tc.entries, tc.observed)
		if _, relinked := got.byCandidate[0]; relinked != tc.relinked || got.conflicts != tc.conflicts {
			t.Fatalf("%s: relinked=%v conflicts=%d", name, relinked, got.conflicts)
		}
	}
	// A disconnected root is never a source.
	revoked := []Root{{ID: "a", Path: "/music/a"}, {ID: "b", Path: "/music/b", RevokedAt: &now}}
	got := planRelinks(Document{Roots: revoked, Entries: []Entry{prior("e1", "b", "Old", "id-1", "reaper", "Song.rpp")}},
		Scan{ID: "new", RootID: "a"}, discovery(candidate), revoked[0], map[string]bool{})
	if len(got.byCandidate) != 0 || got.conflicts != 0 {
		t.Fatalf("revoked root was a relink source: %+v", got)
	}
	// A move into a root where the entry already has an observation is not a
	// relink (one observation per root).
	both := prior("e1", "b", "Old", "id-1", "reaper", "Song.rpp")
	both.Observations = append(both.Observations, Observation{RootID: "a", RelativeFolder: "Elsewhere",
		FileIdentity: "id-9", Format: "reaper", ScanID: "old", ScannedAt: now, Availability: "available"})
	got = planRelinks(Document{Roots: roots, Entries: []Entry{both}}, Scan{ID: "new", RootID: "a"},
		discovery(candidate), roots[0], map[string]bool{})
	if len(got.byCandidate) != 0 {
		t.Fatalf("relink would give an entry two observations in one root: %+v", got)
	}
}

func TestRelink_DocumentRejectsForgedMoveNotesAndDigestCounts(t *testing.T) {
	r, scope, _, tree, root := connectedMusicRoot(t)
	reviewedRootScan(t, r, scope, root, "forge-initial")
	if err := os.Rename(tree.single, filepath.Join(tree.root, "Moved")); err != nil {
		t.Fatal(err)
	}
	reviewedRootScan(t, r, scope, root, "forge-moved")
	doc, err := r.library.Read(scope)
	if err != nil || !doc.valid(scope) || doc.Digest == nil || doc.Digest.Moved != 1 {
		t.Fatalf("baseline: %+v %v", doc.Digest, err)
	}
	forgeNote := func(edit func(*MovedFrom)) Document {
		forged := doc
		forged.Entries, _ = cloneEntries(doc.Entries)
		for i := range forged.Entries {
			for j := range forged.Entries[i].Observations {
				if note := forged.Entries[i].Observations[j].MovedFrom; note != nil {
					edit(note)
				}
			}
		}
		return forged
	}
	for name, edit := range map[string]func(*MovedFrom){
		"same place":    func(m *MovedFrom) { m.RelativeFolder = "Moved" },
		"no scan":       func(m *MovedFrom) { m.ScanID = "" },
		"no root":       func(m *MovedFrom) { m.RootID = "" },
		"escaping path": func(m *MovedFrom) { m.RelativeFolder = "../elsewhere" },
	} {
		if forgeNote(edit).valid(scope) {
			t.Fatalf("%s: forged move note accepted", name)
		}
	}
	for name, forge := range map[string]func(*LibraryDigest){
		"more moved than seen":   func(d *LibraryDigest) { d.Moved = d.Projects + 1 },
		"negative moved":         func(d *LibraryDigest) { d.Moved = -1 },
		"negative conflicts":     func(d *LibraryDigest) { d.RelinkConflicts = -1 },
		"buckets over projects":  func(d *LibraryDigest) { d.New = d.Projects },
		"conflicts over a limit": func(d *LibraryDigest) { d.RelinkConflicts = maxEntries + 1 },
	} {
		forged := doc
		copied := *doc.Digest
		forge(&copied)
		forged.Digest = &copied
		if forged.valid(scope) {
			t.Fatalf("%s: forged digest accepted: %+v", name, copied)
		}
	}
	// An older document without the new fields reads as "none".
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.NewReplacer(`,"moved":1`, ``, `"moved":1,`, ``).Replace(string(encoded))
	if strings.Contains(legacy, `"moved"`) {
		t.Fatalf("legacy fixture still names moved: %s", legacy)
	}
	var older Document
	if err := json.Unmarshal([]byte(legacy), &older); err != nil || older.Digest.Moved != 0 {
		t.Fatalf("legacy digest: %+v %v", older.Digest, err)
	}
}
