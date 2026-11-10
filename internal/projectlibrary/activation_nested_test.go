package projectlibrary

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// nestSongInFolderGroup moves a linked song into a new ordinary group inside
// its Home with the store's public calls: the trusted unlink shape (with the
// group-requirement snapshot set aside), a move of the now-unprotected folder,
// and the same link and snapshot written back. It returns the group's id.
func nestSongInFolderGroup(t *testing.T, store *workspace.FileStore, homeID, songID string) string {
	t.Helper()
	albums := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Albums"})
	albums.Kind, albums.ParentID = "group", homeID
	if song, err := store.Get(songID); err == nil {
		albums.OwnerUserID = song.OwnerUserID
	}
	if err := store.Save(albums); err != nil {
		t.Fatal(err)
	}
	var link *workspace.AssistantProjectLink
	var requirement *workspace.GroupRequirementSnapshot
	if err := store.Update(songID, func(song *workspace.Workspace) error {
		link = song.GetAssistantProjectLink()
		song.SetAssistantProjectLink(nil)
		song.ParentID = ""
		if provenance := song.GetTemplateProvenance(); provenance != nil {
			requirement = provenance.GroupRequirement
			provenance.GroupRequirement = nil
			song.SetTemplateProvenance(provenance)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MoveWorkspaceFolder(songID, albums.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(songID, func(song *workspace.Workspace) error {
		song.SetAssistantProjectLink(link)
		if provenance := song.GetTemplateProvenance(); provenance != nil {
			provenance.GroupRequirement = requirement
			song.SetTemplateProvenance(provenance)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return albums.ID
}

// FR17: the library's lookups use the link and the station, never the parent.
// A song inside a folder group inside its Home counts exactly as it did flat.
func TestActivation_NestedSongStillCountsAsConnected(t *testing.T) {
	a, scope, _, file, _, installed := activationFixture(t)
	service := NewActivationService(a, realActivationCreator(t, scope, file, installed))
	doc, err := a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song")
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Commit(t.Context(), scope, "single", review.Token, "nested-song")
	if err != nil {
		t.Fatal(err)
	}
	read := func() (string, string, bool, []string) {
		t.Helper()
		page, err := a.library.Query(scope, Search{})
		if err != nil {
			t.Fatal(err)
		}
		rows, _ := json.Marshal(page.Rows)
		summary, err := a.library.Summary(scope)
		if err != nil {
			t.Fatal(err)
		}
		counts, _ := json.Marshal(map[string]any{"to_set_up": summary.ToSetUp, "file_choice": summary.NeedsFileChoice, "digest": summary.Digest})
		linked, err := workspace.NewAssistantProgramStore(file).LinkedProjects(scope.HomeID)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(linked))
		for _, project := range linked {
			ids = append(ids, project.ID)
		}
		reciprocal := a.reciprocalLink(scope, ExactLink{WorkspaceID: result.WorkspaceID, LinkID: result.LinkID, Revision: 1})
		return string(rows), string(counts), reciprocal, ids
	}
	rowsBefore, countsBefore, reciprocalBefore, linkedBefore := read()

	albumsID := nestSongInFolderGroup(t, file, scope.HomeID, result.WorkspaceID)
	if song, err := file.Get(result.WorkspaceID); err != nil || song.ParentID != albumsID {
		t.Fatalf("the song must be nested for this test: %+v %v", song, err)
	}
	rowsAfter, countsAfter, reciprocalAfter, linkedAfter := read()

	if !strings.Contains(rowsAfter, `"connection":"connected"`) {
		t.Fatalf("the nested song's row is not connected: %s", rowsAfter)
	}
	if rowsAfter != rowsBefore {
		t.Fatalf("library rows changed when the song nested:\nbefore %s\nafter  %s", rowsBefore, rowsAfter)
	}
	if countsAfter != countsBefore {
		t.Fatalf("summary counts changed when the song nested:\nbefore %s\nafter  %s", countsBefore, countsAfter)
	}
	if !reciprocalBefore || !reciprocalAfter {
		t.Fatalf("reciprocal link before=%v after=%v, want both true", reciprocalBefore, reciprocalAfter)
	}
	if len(linkedAfter) != 1 || linkedAfter[0] != result.WorkspaceID || len(linkedBefore) != 1 {
		t.Fatalf("LinkedProjects before=%v after=%v", linkedBefore, linkedAfter)
	}
	doc, err = a.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Review(t.Context(), scope, "single", doc.Revision, "Song.rpp", "Song"); err == nil {
		t.Fatal("the sheet offered a nested, already connected song again")
	}
}
