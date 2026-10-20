package workspace

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func resourceProviderOwner() AssistantProjectProviderOwner {
	return AssistantProjectProviderOwner{
		PluginID: "reaper", PluginVersion: "4.0.0", BlueprintID: "studio", BlueprintVersion: 1,
		ProjectTeamID: "studio_team", ProjectTeamSchema: 1, ProjectTeamVersion: 1,
		ProjectTeamDigest: strings.Repeat("7", 64), PluginGeneration: 19, ComponentFingerprint: strings.Repeat("d", 64),
		WorkspaceKind: AssistantWorkspaceKindResource,
	}
}

type resourceHomeFixture struct {
	store     *FileStore
	programs  *AssistantProgramStore
	home      *Workspace
	homeOwner AssistantProgramHomeOwner
	songOwner AssistantProjectProviderOwner
}

func newResourceHomeFixture(t *testing.T) resourceHomeFixture {
	t.Helper()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	programs := NewAssistantProgramStore(store)
	homeOwner, songOwner := splitProviderOwners()
	key := AssistantProgramKey{OwnerUserID: "owner", PluginID: "music", ProgramID: "music_home"}
	home, _, err := programs.EnsureNamedIndependentStation(key, splitHomeDeclaration(), "Music Home", homeOwner)
	if err != nil {
		t.Fatal(err)
	}
	return resourceHomeFixture{store: store, programs: programs, home: home, homeOwner: homeOwner, songOwner: songOwner}
}

func (f resourceHomeFixture) child(t *testing.T, id string, owner AssistantProjectProviderOwner) *Workspace {
	t.Helper()
	project := NewWorkspace(CreateWorkspaceParams{Name: id})
	project.ID = id
	project.OwnerUserID = "owner"
	project.SetTemplateProvenance(splitProjectProvenance(f.home.ID, project.ID, f.homeOwner, owner))
	if err := f.store.Save(project); err != nil {
		t.Fatal(err)
	}
	return project
}

func TestResourceWorkspace_LinksOnceAndASecondOneNamesTheFirst(t *testing.T) {
	fixture := newResourceHomeFixture(t)
	first := fixture.child(t, "studio-one", resourceProviderOwner())
	if _, _, err := fixture.programs.EnsureProjectStation(first.ID); err != nil {
		t.Fatalf("link the first resource workspace: %v", err)
	}
	linked, err := fixture.store.Get(first.ID)
	if err != nil || !AssistantProjectLinkIsResource(linked.GetAssistantProjectLink()) {
		t.Fatalf("first link = %+v, %v", linked.GetAssistantProjectLink(), err)
	}
	// Linking it again is the same link, not a second workspace.
	if _, _, err := fixture.programs.EnsureProjectStation(first.ID); err != nil {
		t.Fatalf("relink the same resource workspace: %v", err)
	}

	second := fixture.child(t, "studio-two", resourceProviderOwner())
	_, _, err = fixture.programs.EnsureProjectStation(second.ID)
	var exists *ResourceWorkspaceExistsError
	if !errors.As(err, &exists) || exists.WorkspaceID != first.ID || !errors.Is(err, ErrResourceWorkspaceExists) {
		t.Fatalf("second resource workspace err = %v", err)
	}
	unlinked, err := fixture.store.Get(second.ID)
	if err != nil || unlinked.GetAssistantProjectLink() != nil {
		t.Fatalf("the refused workspace was linked: %+v, %v", unlinked.GetAssistantProjectLink(), err)
	}
	active, err := fixture.programs.ActiveResourceWorkspace(fixture.home.ID, ptr(resourceProviderOwner()))
	if err != nil || active == nil || active.ID != first.ID {
		t.Fatalf("active resource workspace = %+v, %v", active, err)
	}
}

func TestResourceWorkspace_ConcurrentLinksLeaveExactlyOne(t *testing.T) {
	fixture := newResourceHomeFixture(t)
	const racers = 6
	for index := 0; index < racers; index++ {
		fixture.child(t, "studio-"+string(rune('a'+index)), resourceProviderOwner())
	}
	var wait sync.WaitGroup
	results := make([]error, racers)
	for index := 0; index < racers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, _, results[index] = fixture.programs.EnsureProjectStation("studio-" + string(rune('a'+index)))
		}(index)
	}
	wait.Wait()
	winners := 0
	for _, err := range results {
		switch {
		case err == nil:
			winners++
		case !errors.Is(err, ErrResourceWorkspaceExists):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	linked, err := fixture.programs.LinkedProjects(fixture.home.ID)
	if winners != 1 || err != nil || len(linked) != 1 {
		t.Fatalf("winners = %d, linked = %d, %v", winners, len(linked), err)
	}
}

func TestResourceWorkspace_IsNeverASong(t *testing.T) {
	fixture := newResourceHomeFixture(t)
	song := fixture.child(t, "song-one", fixture.songOwner)
	studio := fixture.child(t, "studio-one", resourceProviderOwner())
	for _, id := range []string{song.ID, studio.ID} {
		if _, _, err := fixture.programs.EnsureProjectStation(id); err != nil {
			t.Fatalf("link %s: %v", id, err)
		}
	}
	members, err := fixture.programs.LinkedProjects(fixture.home.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("members = %d, %v", len(members), err)
	}
	songs, err := fixture.programs.LinkedSongProjects(fixture.home.ID)
	if err != nil || len(songs) != 1 || songs[0].ID != song.ID {
		t.Fatalf("songs = %+v, %v", songs, err)
	}
	home, err := fixture.store.Get(fixture.home.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ids := SongProjectIDs(fixture.store, home.GetAssistantProgramState().LinkedProjectIDs); len(ids) != 1 || ids[0] != song.ID {
		t.Fatalf("song ids = %v", ids)
	}
	// A song link never reads as a resource, with or without provenance.
	if AssistantProjectLinkIsResource(&AssistantProjectLink{}) || AssistantProjectLinkIsResource(nil) {
		t.Fatal("a legacy link read as a resource workspace")
	}
}

// PRD FR1 / AC12: a Studio linked beside songs is never counted, staged,
// planned or learned from as a song.
func TestResourceWorkspace_StaysOutOfSongOnlyWork(t *testing.T) {
	fixture := newResourceHomeFixture(t)
	if err := fixture.store.Update(fixture.home.ID, func(current *Workspace) error {
		state := current.GetAssistantProgramState()
		state.Hired = true
		state.Declaration.Reflection.MinimumProjects = 3
		state.Declaration.Reflection.CadenceHours = 24
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	songs := []*Workspace{fixture.child(t, "song-one", fixture.songOwner), fixture.child(t, "song-two", fixture.songOwner)}
	studio := fixture.child(t, "studio-one", resourceProviderOwner())
	for _, project := range append(songs, studio) {
		if _, _, err := fixture.programs.EnsureProjectStation(project.ID); err != nil {
			t.Fatalf("link %s: %v", project.ID, err)
		}
	}
	home, err := fixture.store.Get(fixture.home.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Two songs and a Studio are three members but only two songs: the
	// three-song reflection threshold is not met.
	if state := home.GetAssistantProgramState(); len(state.LinkedProjectIDs) != 3 || state.Reflection.ScheduleTaskID != "" {
		t.Fatalf("members = %v, reflection = %+v", state.LinkedProjectIDs, state.Reflection)
	}
	if err := NewAssistantReflectionService(fixture.store, nil, nil).EnsureSchedule(fixture.home.ID); err != nil {
		t.Fatal(err)
	}
	if home, _ = fixture.store.Get(fixture.home.ID); home.GetAssistantProgramState().Reflection.ScheduleTaskID != "" {
		t.Fatal("EnsureSchedule counted the Studio as a song")
	}
	// The legacy portfolio lists the songs only.
	portfolio := NewAssistantPortfolioService(fixture.store)
	rows, err := portfolio.List(fixture.home.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("portfolio rows = %+v, %v", rows, err)
	}
	for _, row := range rows {
		if row.ProjectWorkspaceID == studio.ID {
			t.Fatal("the Studio is a portfolio row")
		}
	}
	// An accepted Studio task advances nothing.
	if _, _, err := fixture.programs.RecordAcceptedCompletion(studio.ID, "task:catalog-tidy"); !errors.Is(err, ErrAssistantProgramUnavailable) {
		t.Fatalf("Studio completion err = %v", err)
	}
	// A third song does arm reflection: the threshold still works for songs.
	third := fixture.child(t, "song-three", fixture.songOwner)
	if _, _, err := fixture.programs.EnsureProjectStation(third.ID); err != nil {
		t.Fatal(err)
	}
	if home, _ = fixture.store.Get(fixture.home.ID); home.GetAssistantProgramState().Reflection.ScheduleTaskID == "" {
		t.Fatal("three songs did not arm reflection")
	}
}

func TestResourceWorkspace_ProvenanceRefusesAnUnknownKind(t *testing.T) {
	owner := resourceProviderOwner()
	if !owner.Valid() {
		t.Fatal("resource provenance is not valid")
	}
	owner.WorkspaceKind = "library"
	if owner.Valid() {
		t.Fatal("an unknown workspace kind is valid provenance")
	}
}

func ptr[T any](value T) *T { return &value }
