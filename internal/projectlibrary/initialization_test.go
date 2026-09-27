package projectlibrary

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func legacyMusicHome(t *testing.T) (*workspace.FileStore, Scope, *workspace.Workspace) {
	t.Helper()
	file, scope := libraryHome(t)
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID,
		PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	project := &workspace.Workspace{ID: "legacy-project", Name: "Song managed in Ori",
		OwnerUserID: scope.OwnerUserID, Status: workspace.StatusActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	link := &workspace.AssistantProjectLink{ID: workspace.AssistantProjectLinkID(scope.HomeID, project.ID),
		SchemaVersion: workspace.AssistantProjectLinkSchemaVersion, StationWorkspaceID: scope.HomeID,
		Key: key, StateRevision: 1, LinkedAt: time.Now().UTC()}
	project.SetAssistantProjectLink(link)
	if err := file.Save(project); err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.StateRevision = 2
		state.PluginAvailable = true
		state.LinkedProjectIDs = []string{project.ID}
		state.Portfolio.StateRevision = 1
		state.Portfolio.Projects = []workspace.AssistantPortfolioProject{{
			ProjectWorkspaceID: project.ID, LinkID: link.ID, Status: workspace.AssistantPortfolioStatusActive,
			Priority: 0, Milestones: []workspace.AssistantPortfolioMilestone{{
				ID: "mix", Label: "Review mix", DueDate: "2026-10-12", Complete: true}},
			Blockers: []string{"Check bass"}, Deliverables: []string{"Bounce"},
			ArchiveReviewState: workspace.AssistantArchiveReviewNotReady, UpdatedAt: time.Now().UTC(),
		}}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return file, scope, project
}

func TestInitialize_ExplicitReviewCopiesOnlySavedFieldsAndLinks(t *testing.T) {
	file, scope, project := legacyMusicHome(t)
	before, err := project.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(file)
	if _, err := s.Read(scope); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("read migrated without consent: %v", err)
	}
	preview, err := s.ReviewInitialize(scope)
	if err != nil || preview.LinkedCount != 1 || len(preview.EditedProjects) != 1 ||
		preview.EditedProjects[0].Fields.Milestones[0].ID != "mix" {
		t.Fatalf("explicit review: %+v %v", preview, err)
	}
	if _, err := s.Read(scope); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("review itself switched metadata authority: %v", err)
	}
	if _, err := workspace.NewAssistantPortfolioService(file).List(scope.HomeID); err != nil {
		t.Fatalf("legacy portfolio blocked before commit: %v", err)
	}
	doc, replay, err := s.CommitInitialize(scope, preview.Token, "initialize-once")
	if err != nil || replay || doc.Revision != 1 || len(doc.Entries) != 1 || len(doc.Operations) != 1 {
		t.Fatalf("initialization: %+v replay=%v err=%v", doc, replay, err)
	}
	entry := doc.Entries[0]
	if entry.Link == nil || entry.Link.WorkspaceID != project.ID || entry.Link.Revision != 1 ||
		len(entry.Observations) != 0 || len(doc.Roots) != 0 || entry.Fields.DisplayName != "" ||
		entry.Fields.Status != workspace.AssistantPortfolioStatusActive || entry.Fields.Priority == nil ||
		*entry.Fields.Priority != 0 || entry.Fields.Milestones[0].DueDate != "2026-10-12" ||
		!entry.Fields.Milestones[0].Complete || entry.Fields.Source != "legacy_portfolio" {
		t.Fatalf("saved legacy fields or root authority changed: %+v", entry)
	}
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(home.GetAssistantProgramState().Portfolio.Projects) != 1 ||
		home.GetAssistantProgramState().Portfolio.Projects[0].Milestones[0].ID != "mix" {
		t.Fatal("initialization erased legacy audit history")
	}
	if _, err := workspace.NewAssistantPortfolioService(file).List(scope.HomeID); !errors.Is(err, workspace.ErrAssistantPortfolioLibraryOwned) {
		t.Fatalf("stale legacy portfolio remained editable: %v", err)
	}
	if _, wasReplay, err := s.CommitInitialize(scope, preview.Token, "initialize-once"); err != nil || !wasReplay {
		t.Fatalf("exact retry was not idempotent: replay=%v err=%v", wasReplay, err)
	}
	if _, _, err := s.CommitInitialize(scope, preview.Token, "different-key"); !errors.Is(err, ErrConflict) {
		t.Fatalf("different key repeated initialization: %v", err)
	}
	projectAfter, err := file.Get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := projectAfter.ToJSON()
	if err != nil || string(before) != string(after) {
		t.Fatalf("project/agent state was changed: %v", err)
	}
	reopened, err := workspace.NewFileStore(filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID))))
	if err != nil {
		t.Fatal(err)
	}
	if saved, err := NewStore(reopened).Read(scope); err != nil ||
		len(saved.Entries) != 1 || saved.Entries[0].ID != entry.ID {
		t.Fatalf("restart lost atomic authority switch: %+v %v", saved, err)
	}
}

func TestInitialize_UneditedLinkDoesNotInheritProjectedPlanning(t *testing.T) {
	file, scope, _ := legacyMusicHome(t)
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID,
		PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	child := &workspace.Workspace{ID: "new-unedited-link", Name: "Pending song",
		OwnerUserID: scope.OwnerUserID, Status: workspace.StatusActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	child.SetAssistantProjectLink(&workspace.AssistantProjectLink{
		ID: workspace.AssistantProjectLinkID(scope.HomeID, child.ID), StationWorkspaceID: scope.HomeID,
		SchemaVersion: workspace.AssistantProjectLinkSchemaVersion, StateRevision: 1, Key: key})
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
	s := NewStore(file)
	preview, err := s.ReviewInitialize(scope)
	if err != nil || preview.LinkedCount != 2 || len(preview.EditedProjects) != 1 {
		t.Fatalf("unedited project preview: %+v %v", preview, err)
	}
	doc, replay, err := s.CommitInitialize(scope, preview.Token, "unedited")
	if err != nil || replay || len(doc.Entries) != 2 {
		t.Fatalf("unedited project init: %+v %v %v", doc, replay, err)
	}
	for _, entry := range doc.Entries {
		if entry.Link != nil && entry.Link.WorkspaceID == child.ID {
			if entry.Fields.Status != "" || entry.Fields.Revision != 0 || entry.Fields.Priority != nil {
				t.Fatalf("inferred portfolio planning for unedited link: %+v", entry.Fields)
			}
			return
		}
	}
	t.Fatal("unedited link was lost")
}

func TestInitialize_LegacyEditOrForeignLinkStalesReviewWithoutMarker(t *testing.T) {
	file, scope, project := legacyMusicHome(t)
	s := NewStore(file)
	preview, err := s.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.Portfolio.StateRevision++
		state.Portfolio.Projects[0].Blockers = []string{"New blocker"}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CommitInitialize(scope, preview.Token, "stale"); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent legacy edit was overwritten: %v", err)
	}
	if _, err := s.Read(scope); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("stale review published marker: %v", err)
	}
	if err := file.Update(project.ID, func(child *workspace.Workspace) error {
		link := child.GetAssistantProjectLink()
		link.Key.OwnerUserID = "foreign"
		child.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewInitialize(scope); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign child was adopted by name: %v", err)
	}
}

func TestInitialize_SplitLegacyPortfolioMirrorCannotBeOverwritten(t *testing.T) {
	file, scope, _ := legacyMusicHome(t)
	primary, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oldHome, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Save(oldHome); err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.Portfolio.Projects[0].Status = workspace.AssistantPortfolioStatusComplete
		state.Portfolio.StateRevision++
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mirrored := workspace.NewSyncStore(primary, file)
	if _, err := NewStore(mirrored).ReviewInitialize(scope); !errors.Is(err, ErrMirrorDiverged) {
		t.Fatalf("stale primary erased newer legacy edits: %v", err)
	}
	decorated := workspace.NewAgentSnapshotStore(mirrored, nil)
	if _, err := NewStore(decorated).ReviewInitialize(scope); !errors.Is(err, ErrMirrorDiverged) {
		t.Fatalf("production decorator hid the split legacy mirror: %v", err)
	}
	if _, err := workspace.NewAssistantPortfolioService(decorated).List(scope.HomeID); !errors.Is(err, workspace.ErrAssistantPortfolioLibraryOwned) {
		t.Fatalf("legacy API edited a split Home: %v", err)
	}
	newer, err := file.Get(scope.HomeID)
	if err != nil || newer.GetAssistantProgramState().Portfolio.Projects[0].Status != workspace.AssistantPortfolioStatusComplete {
		t.Fatalf("split legacy mirror was overwritten: %+v %v", newer, err)
	}
}

func TestInitialize_FailedSaveLeavesLegacyAuthorityAndReviewRetryable(t *testing.T) {
	file, scope, _ := legacyMusicHome(t)
	primary, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Save(before); err != nil {
		t.Fatal(err)
	}
	// The primary also needs the child to resolve exact reciprocal links.
	child, err := file.Get("legacy-project")
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Save(child); err != nil {
		t.Fatal(err)
	}
	live := workspace.NewSyncStore(primary, file)
	preview, err := NewStore(live).ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	failed := workspace.NewSyncStore(&failingLibrarySave{Store: primary}, file)
	if _, _, err := NewStore(failed).CommitInitialize(scope, preview.Token, "retry-after-save"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("failed primary save: %v", err)
	}
	if _, err := NewStore(live).Read(scope); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("failed save published marker: %v", err)
	}
	if _, err := workspace.NewAssistantPortfolioService(live).List(scope.HomeID); err != nil {
		t.Fatalf("failed save switched legacy authority: %v", err)
	}
	if result, replay, err := NewStore(live).CommitInitialize(scope, preview.Token, "retry-after-save"); err != nil || replay || len(result.Entries) != 1 {
		t.Fatalf("review could not be retried safely: %+v %v %v", result, replay, err)
	}
}
