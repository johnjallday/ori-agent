package setupjourney

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/specialist"
)

func TestProjectChoiceLabelsNameTheUserOutcomeWithoutChangingReviewSemantics(t *testing.T) {
	definitions := actionDefinitionsByKind[specialist.SetupStepProjectConnect]
	labels := make(map[ActionID]string, len(definitions))
	for _, definition := range definitions {
		if definition.ID == ActionReviewExistingProject || definition.ID == ActionReviewNewProject {
			if definition.Effect != ActionEffectReview || definition.RequiresReview {
				t.Fatalf("project choice changed review semantics: %#v", definition)
			}
			labels[definition.ID] = definition.Label
		}
	}
	if labels[ActionReviewExistingProject] != "Import Existing Project" ||
		labels[ActionReviewNewProject] != "Create New Project" {
		t.Fatalf("project choice labels = %#v", labels)
	}
}

func TestServiceOpenAndDismissAreRevisionedPresentationOnlyMutations(t *testing.T) {
	service, _ := serviceFixture(t, defaultCanonicalReads())
	ctx := context.Background()
	initial, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatalf("initial read: %v", err)
	}
	opened, err := service.Open(ctx, "local", "", PresentationMutation{
		IfRevision: initial.StateRevision, IdempotencyKey: "open-request-1",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if opened.FirstOpenedAt == nil || opened.Dismissed || opened.Lifecycle != LifecycleInProgress ||
		opened.StateRevision != initial.StateRevision+1 {
		t.Fatalf("unexpected opened state: %#v", opened)
	}
	openedAt := *opened.FirstOpenedAt

	// The accepted key replays even though its original if_revision is now old.
	replayedOpen, err := service.Open(ctx, "local", "", PresentationMutation{
		IfRevision: initial.StateRevision, IdempotencyKey: "open-request-1",
	})
	if err != nil || replayedOpen.StateRevision != opened.StateRevision ||
		replayedOpen.FirstOpenedAt == nil || !replayedOpen.FirstOpenedAt.Equal(openedAt) {
		t.Fatalf("open replay: %#v err=%v", replayedOpen, err)
	}

	dismissed, err := service.Dismiss(ctx, "local", "", PresentationMutation{
		IfRevision: opened.StateRevision, IdempotencyKey: "dismiss-request-1",
	})
	if err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if !dismissed.Dismissed || dismissed.LastDismissedAt == nil || dismissed.FirstOpenedAt == nil ||
		dismissed.Lifecycle != LifecycleInProgress {
		t.Fatalf("dismiss changed readiness or opening history: %#v", dismissed)
	}

	_, err = service.Open(ctx, "local", "", PresentationMutation{
		IfRevision: opened.StateRevision, IdempotencyKey: "different-stale-request",
	})
	var publicFailure *Failure
	if !errors.As(err, &publicFailure) || publicFailure.ReasonCode != ReasonRevisionConflict ||
		publicFailure.StateRevision != dismissed.StateRevision {
		t.Fatalf("stale open error = %#v, %v", publicFailure, err)
	}
}

func TestServiceCreateOrResumeChildIsIdempotentAndCreatesNoCanonicalResource(t *testing.T) {
	_, store := openTestStore(t)
	readers := make(map[specialist.SetupStepKind]CanonicalReader, len(actionDefinitionsByKind))
	for kind := range actionDefinitionsByKind {
		kind := kind
		readers[kind] = CanonicalReaderFunc(func(_ context.Context, scope ReadScope) (CanonicalStepRead, error) {
			if scope.RunKind == RunKindChild {
				switch kind {
				case specialist.SetupStepIntegrationInstall:
					return CanonicalStepRead{Complete: true}, nil
				case specialist.SetupStepProjectConnect:
					return CanonicalStepRead{AvailableActions: []ActionID{ActionReviewExistingProject, ActionReviewNewProject}}, nil
				default:
					return CanonicalStepRead{}, nil
				}
			}
			switch kind {
			case specialist.SetupStepIntegrationInstall:
				return CanonicalStepRead{Complete: true, Result: CanonicalResult{
					IntegrationPluginID: "com.ori.reaper", IntegrationVersion: "0.5.0",
				}}, nil
			case specialist.SetupStepProjectConnect:
				return CanonicalStepRead{Complete: true, Result: CanonicalResult{
					HomeWorkspaceID: "workspace-home", ProjectWorkspaceID: "workspace-first",
				}}, nil
			case specialist.SetupStepWorkspaceSetup, specialist.SetupStepAssistantProgramStaffing:
				return CanonicalStepRead{Complete: true}, nil
			case specialist.SetupStepSummary:
				return CanonicalStepRead{AvailableActions: []ActionID{ActionConnectAnotherProject}}, nil
			default:
				return CanonicalStepRead{}, nil
			}
		})
	}
	registry, err := NewReaderRegistry(readers)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	service, err := NewService(store, &relationshipStub{state: acceptedRelationship()}, registry)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()
	root, err := service.Read(ctx, "local", "")
	if err != nil || root.Lifecycle != LifecycleReady {
		t.Fatalf("ready root: %#v err=%v", root, err)
	}

	child, err := service.CreateOrResumeChild(ctx, "local", PresentationMutation{
		IfRevision: root.StateRevision, IdempotencyKey: "child-request-1",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if child.RunKind != RunKindChild || child.RootRunID != root.RunID ||
		child.Receipts.ProjectWorkspaceID != "" || child.Lifecycle == LifecycleReady {
		t.Fatalf("child creation performed a canonical setup consequence: %#v", child)
	}
	firstChildID := child.RunID
	storedChild, err := store.GetRun(ctx, firstChildID)
	if err != nil || storedChild.OwnerUserID != "" || storedChild.RelationshipID != "" {
		t.Fatalf("child copied root identity: %+v %v", storedChild, err)
	}
	scope, err := service.authorizedActionScope(ctx, "local", firstChildID)
	if err != nil || scope.OwnerUserID != "local" || scope.RootRunID != root.RunID || scope.RunID != firstChildID || scope.ProjectWorkspaceID != "" || scope.SelectedModeID != "" {
		t.Fatalf("child action authority/scope: %+v %v", scope, err)
	}
	for name, change := range map[string]func(*RootSpec){
		"owner":        func(spec *RootSpec) { spec.OwnerUserID = "foreign" },
		"relationship": func(spec *RootSpec) { spec.RelationshipID = "other" },
		"specialist":   func(spec *RootSpec) { spec.SpecialistSlug = "other" },
		"journey":      func(spec *RootSpec) { spec.JourneyID = "other" },
	} {
		t.Run("reject foreign "+name, func(t *testing.T) {
			spec := RootSpec{
				OwnerUserID: "local", RelationshipID: acceptedRelationship().AssistantID,
				SpecialistSlug: "music_production", JourneyID: root.Journey.ID,
				DeclarationSchemaVersion: 1, DeclarationVersion: 1,
				StepIDs: []string{"integration", "project", "workspace", "staffing", "summary"},
			}
			change(&spec)
			foreignRoot, _, err := store.CreateOrGetRoot(ctx, spec)
			if err != nil {
				t.Fatal(err)
			}
			foreignChild, _, err := store.CreateOrGetChild(ctx, foreignRoot.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.authorizedActionScope(ctx, "local", foreignChild.ID); err == nil {
				t.Fatal("authorized a foreign root's child")
			}
		})
	}

	replayed, err := service.CreateOrResumeChild(ctx, "local", PresentationMutation{
		IfRevision: root.StateRevision, IdempotencyKey: "child-request-1",
	})
	if err != nil || replayed.RunID != firstChildID {
		t.Fatalf("child replay: %#v err=%v", replayed, err)
	}

	currentRoot, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatalf("read root after child: %v", err)
	}
	resumed, err := service.CreateOrResumeChild(ctx, "local", PresentationMutation{
		IfRevision: currentRoot.StateRevision, IdempotencyKey: "child-request-2",
	})
	if err != nil || resumed.RunID != firstChildID {
		t.Fatalf("second request did not converge on unbound child: %#v err=%v", resumed, err)
	}
	children, err := store.ListChildRuns(ctx, root.RunID)
	if err != nil || len(children) != 1 {
		t.Fatalf("child rows = %d, err=%v; want one", len(children), err)
	}
}
