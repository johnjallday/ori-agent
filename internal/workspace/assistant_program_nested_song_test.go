package workspace

import (
	"errors"
	"testing"
)

// nestedSongFixture is a Home holding an ordinary group (a folder workspace)
// whose song is a required, grouped project linked to the Home.
type nestedSongFixture struct {
	store    *AgentSnapshotStore
	programs *AssistantProgramStore
	home     *Workspace
	albums   *Workspace
	outside  *Workspace
	key      AssistantProgramKey
}

func newNestedSongFixture(t *testing.T) *nestedSongFixture {
	t.Helper()
	folders, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := NewAgentSnapshotStore(NewSyncStore(NewInMemoryStore(), folders), nil)
	programs := NewAssistantProgramStore(store)
	key := AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "neutral", ProgramID: "neutral-program"}.Normalize()
	home, _, err := programs.EnsureStation(key, lifecycleProgramDeclaration())
	if err != nil {
		t.Fatal(err)
	}
	albums := NewWorkspace(CreateWorkspaceParams{Name: "Albums"})
	albums.ID, albums.Kind, albums.OwnerUserID, albums.ParentID = "albums", "group", "owner-1", home.ID
	if err := store.Save(albums); err != nil {
		t.Fatal(err)
	}
	outside := NewWorkspace(CreateWorkspaceParams{Name: "Elsewhere"})
	outside.ID, outside.Kind, outside.OwnerUserID = "elsewhere", "group", "owner-1"
	if err := store.Save(outside); err != nil {
		t.Fatal(err)
	}
	return &nestedSongFixture{store: store, programs: programs, home: home, albums: albums, outside: outside, key: key}
}

// linkSong saves a required grouped song under parentID and links it to the
// Home through the ordinary link path.
func (f *nestedSongFixture) linkSong(t *testing.T, id, parentID string) *Workspace {
	t.Helper()
	song := NewWorkspace(CreateWorkspaceParams{Name: id})
	song.ID, song.OwnerUserID, song.ParentID = id, "owner-1", parentID
	song.SetTemplateProvenance(&TemplateProvenance{
		TemplateID:       "plugin:neutral:project",
		PluginOwner:      &PluginTemplateOwner{PluginID: "neutral", PluginVersion: "1.0.0", BlueprintID: "project", BlueprintVersion: 1},
		AssistantProgram: lifecycleProgramDeclaration(),
		GroupRequirement: lifecycleSnapshot("required", GroupRequirementCompositionGrouped, &f.key, f.home.ID, song.ID),
	})
	if err := f.store.Save(song); err != nil {
		t.Fatal(err)
	}
	station, _, err := f.programs.EnsureProjectStation(song.ID)
	if err != nil || station == nil || station.ID != f.home.ID {
		t.Fatalf("link %s: station=%v err=%v", id, station, err)
	}
	linked, err := f.store.Get(song.ID)
	if err != nil {
		t.Fatal(err)
	}
	return linked
}

func TestNestedSong_LinkingKeepsItInsideItsGroup(t *testing.T) {
	f := newNestedSongFixture(t)
	song := f.linkSong(t, "album-1", f.albums.ID)
	if song.ParentID != f.albums.ID {
		t.Fatalf("linking pulled the nested song to %q, want it to stay in %q", song.ParentID, f.albums.ID)
	}
	if link := song.GetAssistantProjectLink(); link == nil || link.StationWorkspaceID != f.home.ID {
		t.Fatalf("the song's station must stay the Home: %#v", link)
	}
	// Linking again (the reuse path) is still a no-op for a nested song.
	if _, _, err := f.programs.EnsureProjectStation(song.ID); err != nil {
		t.Fatalf("relinking a nested song: %v", err)
	}
	if again, _ := f.store.Get(song.ID); again.ParentID != f.albums.ID {
		t.Fatalf("relinking moved the nested song to %q", again.ParentID)
	}
}

func TestNestedSong_StrayLinkedSongStillHealsIntoTheHome(t *testing.T) {
	f := newNestedSongFixture(t)
	song := f.linkSong(t, "loose", "")
	if song.ParentID != f.home.ID {
		t.Fatalf("a top-level linked song heals to the Home as before, got parent %q", song.ParentID)
	}
}

func TestNestedSong_CountsAsConnected(t *testing.T) {
	f := newNestedSongFixture(t)
	nested := f.linkSong(t, "album-1", f.albums.ID)
	flat := f.linkSong(t, "single", f.home.ID)
	if nested.ParentID != f.albums.ID {
		t.Fatalf("the song must stay nested for this test, got parent %q", nested.ParentID)
	}

	if status := EvaluateGroupRequirementLifecycle(nested, f.store.Get); status == nil || status.State != GroupRequirementStatusReadyGrouped {
		t.Fatalf("nested song lifecycle = %#v, want ready_grouped", status)
	}
	linked, err := f.programs.LinkedProjects(f.home.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, project := range linked {
		got[project.ID] = true
	}
	if !got[nested.ID] || !got[flat.ID] {
		t.Fatalf("LinkedProjects = %v, want both the nested and the flat song", got)
	}
	if _, _, err := f.programs.RecordAcceptedCompletion(nested.ID, "manual:one"); err != nil {
		t.Fatalf("a nested song's accepted completion was refused: %v", err)
	}
	if _, _, _, _, err := f.programs.reconnectResources(nested.ID, true); err != nil {
		t.Fatalf("a linked nested song failed the reconnect topology check: %v", err)
	}
}

func TestNestedSong_LeavingTheGroupIsNotConnected(t *testing.T) {
	f := newNestedSongFixture(t)
	nested := f.linkSong(t, "album-1", f.albums.ID)
	lookup := func(id string) (*Workspace, error) {
		ws, err := f.store.Get(id)
		if err == nil && id == f.albums.ID {
			ws.ParentID = f.outside.ID
		}
		return ws, err
	}
	if status := EvaluateGroupRequirementLifecycle(nested, lookup); status == nil || status.State != GroupRequirementStatusUnfulfilled {
		t.Fatalf("a song whose group left the Home must be unfulfilled: %#v", status)
	}
}

// FR11: nesting does not loosen the mirror check. The folder mirror's parent
// must still equal the record's parent exactly, not merely be inside the Home.
func TestNestedSong_MirrorsMustAgreeOnTheExactParent(t *testing.T) {
	f := newNestedSongFixture(t)
	nested := f.linkSong(t, "album-1", f.albums.ID)
	if !AssistantProjectLinkMirrorsAgree(f.store, nested) {
		t.Fatal("a nested song written to both mirrors must agree")
	}
	pulledOut, err := f.store.Get(nested.ID)
	if err != nil {
		t.Fatal(err)
	}
	pulledOut.ParentID = f.home.ID
	if AssistantProjectLinkMirrorsAgree(f.store, pulledOut) {
		t.Fatal("a record parent of the Home against a folder parent of the group must disagree")
	}
}

// CanonicalUpdate is where the record-save topology guard runs; callers such
// as task execution use it over the full store stack.
func TestNestedSong_SaveIsJudgedLikeAFlatSong(t *testing.T) {
	f := newNestedSongFixture(t)
	nested := f.linkSong(t, "album-1", f.albums.ID)
	flat := f.linkSong(t, "single", f.home.ID)
	for _, id := range []string{nested.ID, flat.ID} {
		if err := CanonicalUpdate(f.store, id, func(current *Workspace) error {
			current.Description = "edited"
			return nil
		}); err != nil {
			t.Fatalf("an ordinary edit of %s was refused: %v", id, err)
		}
	}
	if err := CanonicalUpdate(f.store, nested.ID, func(current *Workspace) error {
		current.ParentID = f.home.ID
		return nil
	}); !errors.Is(err, ErrAssistantProgramProtected) {
		t.Fatalf("pulling a nested song to the Home through Update must be refused, got %v", err)
	}
	if err := CanonicalUpdate(f.store, flat.ID, func(current *Workspace) error {
		current.ParentID = f.albums.ID
		return nil
	}); !errors.Is(err, ErrAssistantProgramProtected) {
		t.Fatalf("pushing a flat song into a group through Update must be refused, got %v", err)
	}
	if err := CanonicalUpdate(f.store, nested.ID, func(current *Workspace) error {
		current.ParentID = f.outside.ID
		return nil
	}); !errors.Is(err, ErrAssistantProgramProtected) {
		t.Fatalf("moving a nested song out of the Home through Update must be refused, got %v", err)
	}
}

func TestNestedSong_MovesAreCaught(t *testing.T) {
	f := newNestedSongFixture(t)
	nested := f.linkSong(t, "album-1", f.albums.ID)
	for name, destination := range map[string]string{
		"out of the Home":    f.outside.ID,
		"up to the Home":     f.home.ID,
		"to the root":        "",
		"under another song": f.linkSong(t, "single", f.home.ID).ID,
	} {
		if _, err := f.store.MoveWorkspaceFolder(nested.ID, destination); !errors.Is(err, ErrAssistantProgramProtected) {
			t.Fatalf("moving the nested song %s: err=%v, want ErrAssistantProgramProtected", name, err)
		}
	}
	if _, err := f.store.MoveWorkspaceFolder(f.albums.ID, f.outside.ID); !errors.Is(err, ErrAssistantProgramProtected) {
		t.Fatalf("moving the group that holds a song out of the Home: err=%v", err)
	}
	if err := f.store.Delete(f.albums.ID); !errors.Is(err, ErrAssistantProgramProtected) {
		t.Fatalf("deleting the group that holds a song: err=%v", err)
	}
	if current, _ := f.store.Get(nested.ID); current.ParentID != f.albums.ID {
		t.Fatalf("a refused move still changed the parent to %q", current.ParentID)
	}
}
