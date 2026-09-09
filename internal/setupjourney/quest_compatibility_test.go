package setupjourney

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

func TestQuestDeclarationUpgradePreservesProgressAndBlocksWithoutMigration(t *testing.T) {
	ctx := context.Background()
	service, store := serviceFixture(t, defaultCanonicalReads())
	root, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	opened, err := service.Open(ctx, "local", root.RunID, PresentationMutation{IfRevision: root.StateRevision, IdempotencyKey: "before-upgrade"})
	if err != nil {
		t.Fatal(err)
	}
	p := questPluginFixture(t)
	p.WorkspaceSurfaces.SetupQuests[0].Version++
	service.SetQuestCatalog(NewInstalledQuestCatalog(&questPlugins{items: []plugin.InstalledPlugin{p}}))
	scoped, err := service.ForQuest(ctx, "local", reaperQuestKey.PluginID, reaperQuestKey.ID)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := scoped.Read(ctx, "local", "")
	if err != nil || upgraded.RunID != root.RunID || !upgraded.DeclarationIncompatible {
		t.Fatalf("upgrade=%#v err=%v", upgraded, err)
	}
	for _, step := range upgraded.Steps {
		if len(step.Actions) != 0 {
			t.Fatalf("upgrade offered unreviewed action: %#v", step)
		}
	}
	stored, err := store.GetRun(ctx, root.RunID)
	if err != nil || stored.DeclarationVersion != 1 || stored.FirstOpenedAt == nil || !stored.FirstOpenedAt.Equal(*opened.FirstOpenedAt) {
		t.Fatalf("lost history: %#v err=%v", stored, err)
	}
}

func TestQuestResumesLegacyChildrenWithoutInheritingProjectPermissions(t *testing.T) {
	ctx := context.Background()
	_, store := openTestStore(t)
	readers := make(map[specialist.SetupStepKind]CanonicalReader)
	for kind := range actionDefinitionsByKind {
		readers[kind] = CanonicalReaderFunc(func(_ context.Context, scope ReadScope) (CanonicalStepRead, error) {
			if scope.RunKind == RunKindChild {
				return CanonicalStepRead{}, nil
			}
			switch kind {
			case specialist.SetupStepIntegrationInstall:
				return CanonicalStepRead{Complete: true, Result: CanonicalResult{IntegrationPluginID: reaperQuestKey.PluginID, IntegrationVersion: "0.5.0"}}, nil
			case specialist.SetupStepProjectConnect:
				return CanonicalStepRead{Complete: true, Result: CanonicalResult{HomeWorkspaceID: "original-home", ProjectWorkspaceID: "original-project"}}, nil
			case specialist.SetupStepWorkspaceSetup:
				return CanonicalStepRead{Complete: true, Result: CanonicalResult{SelectedModeID: "file_only"}}, nil
			case specialist.SetupStepSummary:
				return CanonicalStepRead{AvailableActions: []ActionID{ActionConnectAnotherProject}}, nil
			default:
				return CanonicalStepRead{Complete: true}, nil
			}
		})
	}
	registry, err := NewReaderRegistry(readers)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, &relationshipStub{state: acceptedRelationship()}, registry)
	if err != nil {
		t.Fatal(err)
	}
	root, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := service.CreateOrResumeChild(ctx, "local", PresentationMutation{IfRevision: root.StateRevision, IdempotencyKey: "legacy-child"})
	if err != nil {
		t.Fatal(err)
	}
	service.relationships = &relationshipStub{err: errors.New("no accepted assistant")}
	service.SetQuestCatalog(NewInstalledQuestCatalog(&questPlugins{}))
	scoped, err := service.ForQuest(ctx, "local", reaperQuestKey.PluginID, reaperQuestKey.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := scoped.Read(ctx, "local", child.RunID)
	if err != nil || resumed.RootRunID != root.RunID {
		t.Fatalf("child=%#v err=%v", resumed, err)
	}
	scope, err := scoped.authorizedActionScope(ctx, "local", child.RunID)
	if err != nil || scope.HomeWorkspaceID != "original-home" || scope.ProjectWorkspaceID != "" || scope.SelectedModeID != "" {
		t.Fatalf("child inherited project scope: %#v err=%v", scope, err)
	}
	if _, err := scoped.Read(ctx, "another-user", child.RunID); err == nil {
		t.Fatal("foreign child exposed")
	}
}
