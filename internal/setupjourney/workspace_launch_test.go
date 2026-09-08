package setupjourney

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestWorkspaceLaunchGroupReviewAcknowledgementAndPrerequisiteBoundaries(t *testing.T) {
	ctx := context.Background()
	reads := defaultCanonicalReads()
	reads[specialist.SetupStepIntegrationInstall] = CanonicalStepRead{Complete: true, Result: CanonicalResult{IntegrationPluginID: "neutral", IntegrationVersion: "1.0.0"}}
	service, _ := serviceFixture(t, reads)
	adapter, _ := setupJourneyProjectAdapter(t)
	service.readers.readers[specialist.SetupStepProjectConnect] = adapter
	if err := service.SetActionAdapter(specialist.SetupStepProjectConnect, adapter); err != nil {
		t.Fatal(err)
	}
	calls := 0
	adapter.CheckPrerequisites = func(context.Context, projecttemplates.Template) (bool, error) { calls++; return true, nil }
	projection, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	revision := projection.StateRevision
	input := json.RawMessage(`{"name":"My Studio"}`)
	review, err := service.Mutate(ctx, "local", projection.RunID, ActionReviewCreateGroup, ActionMutation{IfRevision: revision, IdempotencyKey: "group-review", Input: input})
	if err != nil || review.Review.Group.Name != "My Studio" || review.Journey.Receipts.HomeWorkspaceID != "" {
		t.Fatalf("review: %+v %v", review, err)
	}
	if _, err := service.CheckPreparation(ctx, "local", projection.RunID); err == nil || calls != 0 {
		t.Fatal("checked prerequisites before the group existed")
	}
	if _, err := service.Mutate(ctx, "local", projection.RunID, ActionCreateGroup, ActionMutation{IfRevision: revision, IdempotencyKey: "without-review", Input: input}); err == nil {
		t.Fatal("group created without review")
	}
	commit := ActionMutation{IfRevision: revision, IdempotencyKey: "create-reviewed-group", ReviewToken: review.Review.Token, Input: input}
	created, err := service.Mutate(ctx, "local", projection.RunID, ActionCreateGroup, commit)
	if err != nil || created.Journey.Receipts.HomeWorkspaceID == "" || created.Journey.Receipts.ProjectWorkspaceID != "" || created.Journey.Receipts.SelectedModeID != "" {
		t.Fatalf("create: %+v %v", created, err)
	}
	repeated, err := service.Mutate(ctx, "local", projection.RunID, ActionCreateGroup, commit)
	if err != nil || repeated.Journey.StateRevision != created.Journey.StateRevision {
		t.Fatalf("replay: %+v %v", repeated, err)
	}
	check, err := service.CheckPreparation(ctx, "local", projection.RunID)
	if err != nil || !check.Ready || calls != 1 {
		t.Fatalf("check: %+v %v", check, err)
	}
	if _, err := service.Mutate(ctx, "local", projection.RunID, ActionReviewNewProject, ActionMutation{IfRevision: created.Journey.StateRevision, IdempotencyKey: "premature-project", Input: json.RawMessage(`{"mode_id":"new_project","workspace_name":"Song","project_name":"Song"}`)}); err == nil {
		t.Fatal("project allowed before preparation decision")
	}
	ack := ActionMutation{IfRevision: created.Journey.StateRevision, IdempotencyKey: "continue-without-live", Input: json.RawMessage(`{}`)}
	acknowledged, err := service.Mutate(ctx, "local", projection.RunID, ActionAcknowledgePreparation, ack)
	if err != nil || acknowledged.Journey.Receipts.ProjectWorkspaceID != "" || acknowledged.Journey.Receipts.SelectedModeID != "" {
		t.Fatalf("acknowledgement created permissions/project: %+v %v", acknowledged, err)
	}
	project := acknowledged.Journey.Steps[1]
	if project.Preparation == nil || !project.Preparation.Acknowledged || project.Status == StepComplete {
		t.Fatalf("preparation implied project readiness: %+v", project)
	}
	reads[specialist.SetupStepIntegrationInstall] = CanonicalStepRead{BlockedReason: ReasonIntegrationDisabled}
	if _, err := service.CheckPreparation(ctx, "local", projection.RunID); err == nil || calls != 1 {
		t.Fatal("disabled plugin still invoked")
	}
}

func TestWorkspaceLaunchDoesNotReplaceAnUnverifiedHistoricalHome(t *testing.T) {
	ctx := context.Background()
	adapter, _ := setupJourneyProjectAdapter(t)
	folders, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = folders.Close() })
	store := workspace.NewSyncStore(workspace.NewInMemoryStore(), folders)
	adapter.owner = projectconnection.NewService(store, nil)
	scope := ReadScope{OwnerUserID: "local", RunID: "old-project-run", RunKind: RunKindRoot}
	raw := json.RawMessage(`{"mode_id":"new_project","workspace_name":"Existing Song","project_name":"Existing Song"}`)
	material, err := adapter.PrepareCommit(ctx, scope, ActionCreateNewProject, raw)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Commit(ctx, scope, ActionCreateNewProject, raw, material)
	if err != nil {
		t.Fatal(err)
	}
	scope.WorkspaceLaunch, scope.HomeWorkspaceID, scope.ProjectWorkspaceID = true, result.HomeWorkspaceID, result.ProjectWorkspaceID
	before, err := adapter.Read(ctx, scope)
	if err != nil || !before.Complete || before.Preparation == nil || !before.Preparation.Exists {
		t.Fatalf("before: %+v %v", before, err)
	}
	if err := store.Update(result.HomeWorkspaceID, func(home *workspace.Workspace) error { home.AssistantProgramState = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	after, err := adapter.Read(ctx, scope)
	if err != nil || after.Complete || after.Preparation != nil || len(after.AvailableActions) != 0 || after.BlockedReason != ReasonOwnerUnavailable {
		t.Fatalf("historical project authorized a replacement: %+v %v", after, err)
	}
	ids, _ := store.List()
	if len(ids) != 2 {
		t.Fatalf("read created another resource: %v", ids)
	}
}

func TestGroupInputRejectsUnknownFieldsAndPaths(t *testing.T) {
	for _, input := range []string{`{"name":"Group","owner":"other"}`, `{"name":"/private/home"}`, `{"name":"Group"} {}`, `{"name":""}`} {
		if _, err := preparationInputDigest(ActionCreateGroup, json.RawMessage(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}
