package projectlibrary

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestPortfolioBridge_NewNormalCreatorLinkIsPendingNotAnImplicitCatalogRecord(t *testing.T) {
	file, scope, existing := legacyMusicHome(t)
	library := NewStore(file)
	initializeLibrary(t, library, scope)
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID,
		PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	child := &workspace.Workspace{ID: "normal-creator-after-init", Name: "Unreviewed creator child",
		OwnerUserID: scope.OwnerUserID, Status: workspace.StatusActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	child.SetAssistantProjectLink(&workspace.AssistantProjectLink{ID: workspace.AssistantProjectLinkID(scope.HomeID, child.ID),
		Key: key, StationWorkspaceID: scope.HomeID, StateRevision: 1})
	if err := file.Save(child); err != nil {
		t.Fatal(err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.LinkedProjectIDs = append(state.LinkedProjectIDs, child.ID)
		state.StateRevision++
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bridge := NewManagedPortfolioBridge(file)
	legacy := workspace.NewAssistantPortfolioService(file).WithManagedLibrary(bridge)
	list, err := legacy.List(scope.HomeID)
	if err != nil || len(list) != 1 || list[0].ProjectWorkspaceID != existing.ID {
		t.Fatalf("pending creator link hid Home or became an implicit portfolio entry: %+v %v", list, err)
	}
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Review(scope.HomeID, workspace.AssistantProjectLinkID(scope.HomeID, child.ID), doc.Revision,
		list[0].Fields); !errors.Is(err, workspace.ErrAssistantPortfolioLinkNotFound) {
		t.Fatalf("unassociated child gained metadata edit authority: %v", err)
	}
	if len(doc.Entries) != 1 || doc.Entries[0].Link.WorkspaceID != existing.ID {
		t.Fatalf("read imported an unreviewed child: %+v %v", doc, err)
	}
}

func TestPortfolioBridge_ProviderLossBetweenReviewAndCommitBlocksDirectServiceWrite(t *testing.T) {
	file, scope, _ := legacyMusicHome(t)
	initializeLibrary(t, NewStore(file), scope)
	available := true
	bridge := NewManagedPortfolioBridge(file).WithProviderEvidence(func(candidate Scope, home *workspace.Workspace) bool {
		return available && candidate == scope && home.ID == scope.HomeID
	})
	service := workspace.NewAssistantPortfolioService(file).WithManagedLibrary(bridge)
	list, err := service.List(scope.HomeID)
	if err != nil || len(list) != 1 {
		t.Fatalf("historical fields unavailable: %+v %v", list, err)
	}
	update := list[0].Fields
	update.Status = workspace.AssistantPortfolioStatusComplete
	review, err := service.Review(scope.HomeID, list[0].LinkID, list[0].StateRevision, update)
	if err != nil {
		t.Fatal(err)
	}
	available = false
	if _, err := service.Commit(scope.HomeID, review.Token, "disabled-bridge", update); err == nil {
		t.Fatal("direct managed portfolio service bypassed provider revocation")
	}
	if list, err := service.List(scope.HomeID); err != nil || list[0].Fields.Status == workspace.AssistantPortfolioStatusComplete {
		t.Fatalf("provider loss hid history or wrote unapproved fields: %+v %v", list, err)
	}
}

func TestPortfolioBridge_ExistingHomeEditorAndNewFieldEditorShareOneOwner(t *testing.T) {
	file, scope, project := legacyMusicHome(t)
	s := NewStore(file)
	initReview, err := s.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CommitInitialize(scope, initReview.Token, "bridge-init"); err != nil {
		t.Fatal(err)
	}
	old := workspace.NewAssistantPortfolioService(file).WithManagedLibrary(NewManagedPortfolioBridge(file))
	homeBeforeRead, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	list, err := old.List(scope.HomeID)
	if err != nil || len(list) != 1 || list[0].Fields.Status != workspace.AssistantPortfolioStatusActive ||
		list[0].Fields.Milestones[0].ID != "mix" || list[0].ProjectWorkspaceID != project.ID {
		t.Fatalf("old surface did not project canonical fields: %+v %v", list, err)
	}
	homeAfterRead, err := file.Get(scope.HomeID)
	if err != nil || homeAfterRead.Version != homeBeforeRead.Version ||
		string(homeAfterRead.GetAssistantProgramState().ProjectLibrary) !=
			string(homeBeforeRead.GetAssistantProgramState().ProjectLibrary) {
		t.Fatalf("legacy projection silently rewrote Home/provenance: %v", err)
	}
	doc, err := s.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	entry := doc.Entries[0]
	newReview, err := s.ReviewFields(scope, entry.ID, entry.Fields.Revision,
		FieldsPatch{Stage: text("mixing"), NextAction: text("Print stems")}, scope.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CommitFields(scope, entry.ID, newReview.Token, "new-field-editor",
		entry.Fields.Revision, FieldsPatch{Stage: text("mixing"), NextAction: text("Print stems")}, scope.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	list, err = old.List(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	update := list[0].Fields
	update.Status = workspace.AssistantPortfolioStatusComplete
	update.Priority = 3
	legacyReview, err := old.Review(scope.HomeID, list[0].LinkID, list[0].StateRevision, update)
	if err != nil || legacyReview.Project.Fields.Status != workspace.AssistantPortfolioStatusComplete {
		t.Fatalf("legacy review: %+v %v", legacyReview, err)
	}
	receipt, err := old.Commit(scope.HomeID, legacyReview.Token, "legacy-field-editor", update)
	if err != nil || receipt.Replayed || receipt.ProjectWorkspaceID != project.ID {
		t.Fatalf("legacy commit: %+v %v", receipt, err)
	}
	if replay, err := old.Commit(scope.HomeID, legacyReview.Token, "legacy-field-editor", update); err != nil || !replay.Replayed || replay.ProjectWorkspaceID != project.ID {
		t.Fatalf("legacy idempotency replay: %+v %v", replay, err)
	}
	doc, err = s.Read(scope)
	if err != nil || len(doc.Entries) != 1 || doc.Entries[0].Fields.Stage != "mixing" ||
		doc.Entries[0].Fields.NextAction != "Print stems" ||
		doc.Entries[0].Fields.Status != workspace.AssistantPortfolioStatusComplete ||
		*doc.Entries[0].Fields.Priority != 3 || doc.Entries[0].Fields.Milestones[0].ID != "mix" {
		t.Fatalf("old/new editors diverged or lost sparse metadata: %+v %v", doc, err)
	}
	home, err := file.Get(scope.HomeID)
	if err != nil || home.GetAssistantProgramState().Portfolio.Projects[0].Status != workspace.AssistantPortfolioStatusActive {
		t.Fatalf("legacy history was rewritten as a second metadata owner: %+v %v", home, err)
	}
	if list, err := old.List(scope.HomeID); err != nil || list[0].Fields.Status != workspace.AssistantPortfolioStatusComplete ||
		list[0].StateRevision != receipt.StateRevision {
		t.Fatalf("old API did not read new canonical metadata: %+v %v", list, err)
	}
	reopened, err := workspace.NewFileStore(filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID))))
	if err != nil {
		t.Fatal(err)
	}
	afterRestart := workspace.NewAssistantPortfolioService(reopened).
		WithManagedLibrary(NewManagedPortfolioBridge(reopened))
	if list, err := afterRestart.List(scope.HomeID); err != nil || list[0].Fields.Status != workspace.AssistantPortfolioStatusComplete {
		t.Fatalf("old surface lost canonical metadata on restart: %+v %v", list, err)
	}
	if _, err := workspace.NewAssistantPortfolioService(file).List(scope.HomeID); !errors.Is(err, workspace.ErrAssistantPortfolioLibraryOwned) {
		t.Fatalf("adapterless old service read stale data: %v", err)
	}
}

func TestPortfolioBridge_Over32EditedLinkedProjectsUseManagedFields(t *testing.T) {
	file, scope, _ := legacyMusicHome(t)
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID,
		PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	for i := 1; i < 34; i++ {
		child := &workspace.Workspace{ID: fmt.Sprintf("linked-%d", i),
			Name: fmt.Sprintf("Production %02d", i), OwnerUserID: scope.OwnerUserID,
			Status: workspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
		child.SetAssistantProjectLink(&workspace.AssistantProjectLink{
			ID: workspace.AssistantProjectLinkID(scope.HomeID, child.ID), Key: key,
			StationWorkspaceID: scope.HomeID, StateRevision: 1})
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
	}
	s := NewStore(file)
	initReview, err := s.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CommitInitialize(scope, initReview.Token, "bulk-bridge-init"); err != nil {
		t.Fatal(err)
	}
	old := workspace.NewAssistantPortfolioService(file).WithManagedLibrary(NewManagedPortfolioBridge(file))
	for i := 1; i < 34; i++ {
		doc, err := s.Read(scope)
		if err != nil {
			t.Fatal(err)
		}
		linkID := workspace.AssistantProjectLinkID(scope.HomeID, fmt.Sprintf("linked-%d", i))
		update := workspace.AssistantPortfolioUpdate{Status: workspace.AssistantPortfolioStatusActive,
			ArchiveReviewState: workspace.AssistantArchiveReviewNotReady}
		review, err := old.Review(scope.HomeID, linkID, doc.Revision, update)
		if err != nil {
			t.Fatalf("linked project %d review: %v", i, err)
		}
		if _, err := old.Commit(scope.HomeID, review.Token, fmt.Sprintf("bulk-edit-%d", i), update); err != nil {
			t.Fatalf("linked project %d edit (former cap 32): %v", i, err)
		}
	}
	doc, err := s.Read(scope)
	if err != nil || len(doc.Entries) != 34 || doc.Entries[33].Fields.Status != workspace.AssistantPortfolioStatusActive {
		t.Fatalf("33 new linked edits not saved: %+v %v", doc, err)
	}
	home, err := file.Get(scope.HomeID)
	if err != nil || len(home.GetAssistantProgramState().Portfolio.Projects) != 1 {
		t.Fatalf("legacy 32-item array was rewritten: %+v %v", home, err)
	}
}

func TestPortfolioBridge_StaleLinkAndConcurrentLibraryEditFailClosed(t *testing.T) {
	file, scope, project := legacyMusicHome(t)
	s := NewStore(file)
	initReview, err := s.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CommitInitialize(scope, initReview.Token, "bridge-stale-init"); err != nil {
		t.Fatal(err)
	}
	old := workspace.NewAssistantPortfolioService(file).WithManagedLibrary(NewManagedPortfolioBridge(file))
	list, err := old.List(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	update := list[0].Fields
	update.Status = workspace.AssistantPortfolioStatusComplete
	review, err := old.Review(scope.HomeID, list[0].LinkID, list[0].StateRevision, update)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	newReview, err := s.ReviewFields(scope, doc.Entries[0].ID, doc.Entries[0].Fields.Revision,
		FieldsPatch{Status: text("on_hold")}, scope.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CommitFields(scope, doc.Entries[0].ID, newReview.Token, "intervening-edit",
		doc.Entries[0].Fields.Revision, FieldsPatch{Status: text("on_hold")}, scope.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Commit(scope.HomeID, review.Token, "stale-legacy", update); !errors.Is(err, workspace.ErrAssistantPortfolioConflict) {
		t.Fatalf("old review overwrote a new edit: %v", err)
	}
	if err := file.Update(project.ID, func(current *workspace.Workspace) error {
		link := current.GetAssistantProjectLink()
		link.StateRevision++
		current.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := old.List(scope.HomeID); !errors.Is(err, workspace.ErrAssistantPortfolioConflict) {
		t.Fatalf("inconsistent link was silently adopted: %v", err)
	}
	if _, err := old.Review(scope.HomeID, list[0].LinkID, list[0].StateRevision, update); !errors.Is(err, workspace.ErrAssistantPortfolioConflict) {
		t.Fatalf("stale link was offered for edit: %v", err)
	}
}
