package setupjourney

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The fenced repair writer: one claimed intent produces exactly one child
// write through SyncStore (fence, journal, conditional primary), settles an
// earlier uncertain attempt from both mirrors, and never guesses across a
// split. It adds no agent, binding, grant or catalog record.

type repairCommitFixture struct {
	reviewer  *ProjectRoleRepairReviewer
	store     *workspace.SyncStore
	primary   *workspace.InMemoryStore
	folder    *workspace.FileStore
	db        *database.DB
	dbPath    string
	installed []plugin.InstalledPlugin
	owner     string
	homeID    string
	projectID string
}

func newRepairCommitFixture(t *testing.T) *repairCommitFixture {
	t.Helper()
	store, primary, folder, installed, owner, homeID, projectID := oldSplitChildFixture(t)
	path := filepath.Join(t.TempDir(), "repair-commit.db")
	db, err := database.Open(t.Context(), &database.Config{Path: path, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedReviewedConnectionFixture(t, db, primary, folder, owner, homeID, projectID)
	list := func() ([]plugin.InstalledPlugin, error) { return installed, nil }
	return &repairCommitFixture{
		reviewer: NewProjectRoleRepairReviewer(store, db, list), store: store, primary: primary, folder: folder,
		db: db, dbPath: path, installed: installed, owner: owner, homeID: homeID, projectID: projectID,
	}
}

// claim reviews once under the fixture's fixed review key and consumes that
// review into the claimed operation "claim-one".
func (fx *repairCommitFixture) claim(t *testing.T) ProjectRoleRepairOperation {
	t.Helper()
	review, err := fx.reviewer.Review(t.Context(), fx.owner, fx.homeID, fx.projectID, "review-one")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	claim, err := fx.reviewer.ClaimProjectRoleRepairOperation(t.Context(), fx.owner, fx.homeID, fx.projectID, review.Token, "claim-one")
	if err != nil || claim.Status != "claimed" {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	return claim
}

func (fx *repairCommitFixture) expectedRoles(t *testing.T) []workspace.AssistantProgramRoleSpec {
	t.Helper()
	home, err := fx.store.Get(fx.homeID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := fx.store.Get(fx.projectID)
	if err != nil {
		t.Fatal(err)
	}
	roles, ok := plugin.ExactIndependentProjectRoles(fx.installed, home.GetAssistantProgramState().HomeProvider, child.GetAssistantProjectLink().ProjectProvider)
	if !ok || len(roles) != 1 {
		t.Fatalf("installed blueprint roles unavailable: %v %v", roles, ok)
	}
	return roles
}

func (fx *repairCommitFixture) versions(t *testing.T, id string) (int64, int64) {
	t.Helper()
	primary, err := fx.primary.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := fx.folder.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return primary.Version, folder.Version
}

func TestProjectRoleRepairCommitWritesExactRolesThroughTheFenceOnce(t *testing.T) {
	fx := newRepairCommitFixture(t)
	claim := fx.claim(t)
	homePrimaryBefore, homeFolderBefore := fx.versions(t, fx.homeID)
	childPrimaryBefore, childFolderBefore := fx.versions(t, fx.projectID)
	expected := fx.expectedRoles(t)

	if _, err := fx.reviewer.CommitProjectRoleRepairOperation(t.Context(), "foreign", fx.homeID, fx.projectID, "claim-one"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("foreign owner committed a repair: %v", err)
	}
	done, err := fx.reviewer.CommitProjectRoleRepairOperation(t.Context(), fx.owner, fx.homeID, fx.projectID, "claim-one")
	if err != nil || done.Token != claim.Token || done.Status != ProjectRoleRepairSucceeded {
		t.Fatalf("fenced repair commit: %+v %v", done, err)
	}
	for name, store := range map[string]workspace.Store{"primary": fx.primary, "folder": fx.folder} {
		child, getErr := store.Get(fx.projectID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		link := child.GetAssistantProjectLink()
		if len(link.ProjectRoles) != 1 || link.ProjectRoles[0].ID != expected[0].ID || link.ProjectRoles[0].Scope != workspace.AssistantRoleScopeProject ||
			link.StateRevision != 2 || len(link.ProjectBindings.Bindings) != 0 || len(child.GetAgentInstances()) != 0 {
			t.Fatalf("%s mirror after repair: %+v", name, link)
		}
		if provenance := child.GetTemplateProvenance(); provenance == nil || len(provenance.AssistantProjectRoles) != 1 || provenance.AssistantProjectRoles[0].ID != expected[0].ID {
			t.Fatalf("%s mirror provenance after repair: %+v", name, child.GetTemplateProvenance())
		}
	}
	childPrimaryAfter, childFolderAfter := fx.versions(t, fx.projectID)
	if childPrimaryAfter != childPrimaryBefore+1 || childFolderAfter != childFolderBefore+1 {
		t.Fatalf("expected exactly one child write in both mirrors: %d/%d -> %d/%d", childPrimaryBefore, childFolderBefore, childPrimaryAfter, childFolderAfter)
	}
	if hp, hf := fx.versions(t, fx.homeID); hp != homePrimaryBefore || hf != homeFolderBefore {
		t.Fatalf("repair touched the Home: %d/%d -> %d/%d", homePrimaryBefore, homeFolderBefore, hp, hf)
	}
	repaired, err := fx.store.Get(fx.projectID)
	if err != nil || !workspace.AssistantProjectLinkMirrorsAgree(fx.store, repaired) {
		t.Fatalf("repaired child mirrors disagree: %v", err)
	}
	if _, err := InspectMissingSplitProjectRoles(fx.store, fx.installed, fx.owner, fx.homeID, fx.projectID); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("repaired child still inspects as missing roles: %v", err)
	}
	// Replay is read-only.
	again, err := fx.reviewer.CommitProjectRoleRepairOperation(t.Context(), fx.owner, fx.homeID, fx.projectID, "claim-one")
	if err != nil || again.Status != ProjectRoleRepairSucceeded {
		t.Fatalf("replayed terminal receipt: %+v %v", again, err)
	}
	if p, f := fx.versions(t, fx.projectID); p != childPrimaryAfter || f != childFolderAfter {
		t.Fatal("replay wrote the child again")
	}
	if _, err := fx.reviewer.Review(t.Context(), fx.owner, fx.homeID, fx.projectID, "review-after-repair"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("a repaired child offered another repair review: %v", err)
	}
	// The receipt survives a database restart.
	if err := fx.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := database.Open(context.Background(), &database.Config{Path: fx.dbPath, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	restarted := NewProjectRoleRepairReviewer(fx.store, reopened, func() ([]plugin.InstalledPlugin, error) { return fx.installed, nil })
	if saved, err := restarted.InspectProjectRoleRepairOperation(t.Context(), fx.owner, fx.homeID, fx.projectID, "claim-one"); err != nil || saved.Status != ProjectRoleRepairSucceeded {
		t.Fatalf("restart lost the terminal receipt: %+v %v", saved, err)
	}
}

func TestProjectRoleRepairCommitCancelsWhenReviewedEvidenceMoved(t *testing.T) {
	fx := newRepairCommitFixture(t)
	fx.claim(t)
	childPrimaryBefore, childFolderBefore := fx.versions(t, fx.projectID)
	// An unrelated Home write after the claim changes the exact evidence the
	// owner reviewed (Home version), so the spent consent cannot be applied.
	if err := fx.store.Update(fx.homeID, func(home *workspace.Workspace) error {
		home.Description = "edited after the repair claim"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	op, err := fx.reviewer.CommitProjectRoleRepairOperation(t.Context(), fx.owner, fx.homeID, fx.projectID, "claim-one")
	if !errors.Is(err, ErrProjectRoleRepairNotApplied) || op.Status != ProjectRoleRepairCancelled {
		t.Fatalf("moved evidence was not cancelled: %+v %v", op, err)
	}
	if p, f := fx.versions(t, fx.projectID); p != childPrimaryBefore || f != childFolderBefore {
		t.Fatal("cancelled repair wrote the child")
	}
	child, err := fx.store.Get(fx.projectID)
	if err != nil || len(child.GetAssistantProjectLink().ProjectRoles) != 0 {
		t.Fatalf("cancelled repair left roles: %v", err)
	}
	// A cancelled slot is terminal, so a fresh review is possible again.
	if _, err := fx.reviewer.Review(t.Context(), fx.owner, fx.homeID, fx.projectID, "review-two"); err != nil {
		t.Fatalf("fresh review after cancellation: %v", err)
	}
}

func TestProjectRoleRepairCommitSettlesAnAppliedWriteAfterLostReceipt(t *testing.T) {
	fx := newRepairCommitFixture(t)
	claim := fx.claim(t)
	expected := fx.expectedRoles(t)
	// The write landed in both mirrors, then the process died before the
	// receipt was recorded: the operation is still "claimed".
	if err := fx.store.Update(fx.projectID, func(child *workspace.Workspace) error {
		link := child.GetAssistantProjectLink()
		link.ProjectRoles = cloneProjectRoleSpecs(expected)
		link.StateRevision++
		child.SetAssistantProjectLink(link)
		provenance := child.GetTemplateProvenance()
		provenance.AssistantProjectRoles = cloneProjectRoleSpecs(expected)
		child.SetTemplateProvenance(provenance)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	primaryBefore, folderBefore := fx.versions(t, fx.projectID)
	settled, err := fx.reviewer.CommitProjectRoleRepairOperation(t.Context(), fx.owner, fx.homeID, fx.projectID, "claim-one")
	if err != nil || settled.Token != claim.Token || settled.Status != ProjectRoleRepairSucceeded {
		t.Fatalf("applied write was not settled from the mirrors: %+v %v", settled, err)
	}
	if p, f := fx.versions(t, fx.projectID); p != primaryBefore || f != folderBefore {
		t.Fatal("settling an applied write wrote the child again")
	}
}

func TestProjectRoleRepairCommitRefusesSplitMirrorsAsReconcileRequired(t *testing.T) {
	fx := newRepairCommitFixture(t)
	fx.claim(t)
	// Folder-only role change: a split the writer must never resolve by itself.
	if err := fx.folder.Update(fx.projectID, func(child *workspace.Workspace) error {
		link := child.GetAssistantProjectLink()
		link.ProjectRoles = []workspace.AssistantProgramRoleSpec{{ID: "test-only", Scope: workspace.AssistantRoleScopeProject}}
		child.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	primaryBefore, folderBefore := fx.versions(t, fx.projectID)
	op, err := fx.reviewer.CommitProjectRoleRepairOperation(t.Context(), fx.owner, fx.homeID, fx.projectID, "claim-one")
	if !errors.Is(err, ErrProjectRoleRepairNotApplied) || op.Status != ProjectRoleRepairReconcileRequired {
		t.Fatalf("split mirrors were not classified as reconcile_required: %+v %v", op, err)
	}
	if p, f := fx.versions(t, fx.projectID); p != primaryBefore || f != folderBefore {
		t.Fatal("classification wrote a mirror")
	}
	primaryChild, err := fx.primary.Get(fx.projectID)
	if err != nil || len(primaryChild.GetAssistantProjectLink().ProjectRoles) != 0 {
		t.Fatalf("primary changed by a refused repair: %v", err)
	}
	// The slot stays occupied until a separately reviewed reconciliation.
	if _, err := fx.reviewer.Review(t.Context(), fx.owner, fx.homeID, fx.projectID, "review-two"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
		t.Fatalf("reconcile_required child offered a new repair review: %v", err)
	}
	if again, err := fx.reviewer.CommitProjectRoleRepairOperation(t.Context(), fx.owner, fx.homeID, fx.projectID, "claim-one"); !errors.Is(err, ErrProjectRoleRepairNotApplied) || again.Status != ProjectRoleRepairReconcileRequired {
		t.Fatalf("terminal split state was not replayed read-only: %+v %v", again, err)
	}
}

// refusingUpdateStore stands in for the shared fence refusing or failing the
// child write; nothing is written in any case.
type refusingUpdateStore struct {
	*workspace.SyncStore
	err error
}

func (s *refusingUpdateStore) Update(string, func(*workspace.Workspace) error) error { return s.err }

func TestProjectRoleRepairCommitMapsFenceOutcomesToTerminalStates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status string
	}{
		{"stale base is cancelled", workspace.ErrStaleWorkspaceVersion, ProjectRoleRepairCancelled},
		{"split mirrors need reconciliation", workspace.ErrWorkspaceMirrorsDiverged, ProjectRoleRepairReconcileRequired},
		{"unknown journal needs reconciliation", workspace.ErrWorkspaceFenceUnknown, ProjectRoleRepairReconcileRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newRepairCommitFixture(t)
			fx.claim(t)
			refusing := NewProjectRoleRepairReviewer(&refusingUpdateStore{SyncStore: fx.store, err: tc.err}, fx.db,
				func() ([]plugin.InstalledPlugin, error) { return fx.installed, nil })
			op, err := refusing.CommitProjectRoleRepairOperation(t.Context(), fx.owner, fx.homeID, fx.projectID, "claim-one")
			if !errors.Is(err, ErrProjectRoleRepairNotApplied) || op.Status != tc.status {
				t.Fatalf("fence outcome %v mapped to %+v %v", tc.err, op, err)
			}
			child, err := fx.store.Get(fx.projectID)
			if err != nil || len(child.GetAssistantProjectLink().ProjectRoles) != 0 {
				t.Fatalf("refused write changed the child: %v", err)
			}
		})
	}
	t.Run("uncertain failure keeps the slot claimed and settles on retry", func(t *testing.T) {
		fx := newRepairCommitFixture(t)
		fx.claim(t)
		failing := NewProjectRoleRepairReviewer(&refusingUpdateStore{SyncStore: fx.store, err: io.ErrUnexpectedEOF}, fx.db,
			func() ([]plugin.InstalledPlugin, error) { return fx.installed, nil })
		op, err := failing.CommitProjectRoleRepairOperation(t.Context(), fx.owner, fx.homeID, fx.projectID, "claim-one")
		if err == nil || errors.Is(err, ErrProjectRoleRepairNotApplied) || op.Status != "claimed" {
			t.Fatalf("uncertain failure was settled without evidence: %+v %v", op, err)
		}
		if _, err := fx.reviewer.Review(t.Context(), fx.owner, fx.homeID, fx.projectID, "review-two"); !errors.Is(err, ErrProjectRoleRepairUnavailable) {
			t.Fatalf("uncertain slot offered a competing review: %v", err)
		}
		settled, err := fx.reviewer.CommitProjectRoleRepairOperation(t.Context(), fx.owner, fx.homeID, fx.projectID, "claim-one")
		if err != nil || settled.Status != ProjectRoleRepairSucceeded {
			t.Fatalf("retry after an uncertain failure did not complete the single write: %+v %v", settled, err)
		}
		child, err := fx.store.Get(fx.projectID)
		if err != nil || len(child.GetAssistantProjectLink().ProjectRoles) != 1 {
			t.Fatalf("retry did not write the exact roles: %v", err)
		}
	})
}
