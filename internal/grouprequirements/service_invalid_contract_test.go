package grouprequirements

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// invalidRequiredSnapshot is the shape a half-finished creation left behind on
// real workspaces: a Required, grouped contract with a Home provider but no
// project provider and no link ever written. It declares Required, so ordinary
// deletion fails closed, yet StructurallyValid rejects it.
func invalidRequiredSnapshot(t *testing.T, key *workspace.AssistantProgramKey, homeID, projectID string) *workspace.GroupRequirementSnapshot {
	t.Helper()
	digest := strings.Repeat("a", 64)
	snapshot := &workspace.GroupRequirementSnapshot{
		SchemaVersion: workspace.GroupRequirementSnapshotSchemaVersion, Policy: "required",
		SelectedComposition: workspace.GroupRequirementCompositionGrouped,
		TemplateID:          "plugin:neutral:project", TemplateRevision: digest, DefinitionDigest: digest,
		HomeProvider: &workspace.AssistantProgramHomeOwner{
			PluginID: "neutral", PluginVersion: "0.3.0", ProgramID: "neutral-program",
			HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: digest,
			PluginGeneration: 1, ComponentFingerprint: digest,
		},
		ProgramKey: key, HomeWorkspaceID: homeID,
		ProjectLinkID: workspace.AssistantProjectLinkID(homeID, projectID),
		ReviewDigest:  digest, OperationDigest: digest,
		AppliedAt: time.Date(2026, time.October, 19, 12, 38, 52, 0, time.UTC),
	}
	if snapshot.StructurallyValid() {
		t.Fatal("fixture snapshot must be structurally invalid")
	}
	return snapshot
}

func invalidContractProject(t *testing.T, store workspace.Store, key *workspace.AssistantProgramKey, homeID string) *workspace.Workspace {
	t.Helper()
	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Ori Music Demo"})
	project.ID = "invalid-contract"
	project.OwnerUserID = "owner-1"
	project.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID:       "plugin:neutral:project",
		GroupRequirement: invalidRequiredSnapshot(t, key, homeID, project.ID),
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	return project
}

func TestRequiredDeletionReviewAcceptsDetachedInvalidContract(t *testing.T) {
	ctx := context.Background()
	store := workspace.NewInMemoryStore()
	key := workspace.AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "neutral", ProgramID: "neutral-program"}.Normalize()
	project := invalidContractProject(t, store, &key, "music-home")
	service := NewService(store, NewMemoryStore())
	input := DeletionInput{OwnerUserID: "owner-1", WorkspaceID: project.ID, TrashWorkspace: true}

	review, err := service.ReviewDeletion(ctx, input)
	if err != nil {
		t.Fatalf("detached invalid contract review = %v, want a review", err)
	}
	if review.Token == "" || !review.Trashes || !strings.Contains(review.Summary, "invalid") {
		t.Fatalf("review = %#v", review)
	}
	if !strings.Contains(strings.Join(review.Impact, "\n"), "cannot be repaired or reconnected") {
		t.Fatalf("impact does not explain the invalid record: %#v", review.Impact)
	}
	claim, err := service.ClaimDeletion(ctx, input, review.Token)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.WorkspaceID != project.ID || claim.ContractOperationDigest != strings.Repeat("a", 64) || !claim.TrashWorkspace {
		t.Fatalf("claim = %#v", claim)
	}
	if _, err := service.ClaimDeletion(ctx, input, review.Token); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("reused review = %v, want stale", err)
	}
}

func TestRequiredDeletionReviewStillRefusesInvalidContractThatIsConnected(t *testing.T) {
	ctx := context.Background()
	key := workspace.AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "neutral", ProgramID: "neutral-program"}.Normalize()

	t.Run("home lists the project", func(t *testing.T) {
		store := workspace.NewInMemoryStore()
		home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Music Home"})
		home.ID = "music-home"
		home.OwnerUserID = "owner-1"
		home.Kind = "group"
		home.SetAssistantProgramState(&workspace.AssistantProgramState{Key: key, LinkedProjectIDs: []string{"invalid-contract"}})
		if err := store.Save(home); err != nil {
			t.Fatal(err)
		}
		project := invalidContractProject(t, store, &key, home.ID)
		service := NewService(store, NewMemoryStore())
		_, err := service.ReviewDeletion(ctx, DeletionInput{OwnerUserID: "owner-1", WorkspaceID: project.ID})
		if !errors.Is(err, ErrLifecycleConflict) {
			t.Fatalf("review of a Home-listed project = %v, want lifecycle conflict", err)
		}
	})

	t.Run("project still carries a link", func(t *testing.T) {
		store := workspace.NewInMemoryStore()
		project := invalidContractProject(t, store, &key, "music-home")
		project.SetAssistantProjectLink(&workspace.AssistantProjectLink{
			ID: workspace.AssistantProjectLinkID("music-home", project.ID), StationWorkspaceID: "music-home", Key: key,
		})
		if err := store.Save(project); err != nil {
			t.Fatal(err)
		}
		service := NewService(store, NewMemoryStore())
		_, err := service.ReviewDeletion(ctx, DeletionInput{OwnerUserID: "owner-1", WorkspaceID: project.ID})
		if !errors.Is(err, ErrLifecycleConflict) {
			t.Fatalf("review of a linked project = %v, want lifecycle conflict", err)
		}
	})

	t.Run("record without an operation digest cannot be bound", func(t *testing.T) {
		store := workspace.NewInMemoryStore()
		project := invalidContractProject(t, store, &key, "music-home")
		provenance := project.GetTemplateProvenance()
		provenance.GroupRequirement.OperationDigest = ""
		project.SetTemplateProvenance(provenance)
		if err := store.Save(project); err != nil {
			t.Fatal(err)
		}
		service := NewService(store, NewMemoryStore())
		_, err := service.ReviewDeletion(ctx, DeletionInput{OwnerUserID: "owner-1", WorkspaceID: project.ID})
		if !errors.Is(err, ErrLifecycleUnbindable) {
			t.Fatalf("review of a digest-less record = %v, want unbindable", err)
		}
	})
}
