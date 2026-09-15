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

// FR 29: the group is reviewed and created, and once it exists a project can
// be reviewed straight away. No acknowledgement or preparation check remains.
func TestWorkspaceLaunchGroupReviewLeadsDirectlyToProjectReview(t *testing.T) {
	ctx := context.Background()
	reads := defaultCanonicalReads()
	service, _ := serviceFixture(t, reads)
	adapter, _ := setupJourneyProjectAdapter(t)
	service.readers.readers[specialist.SetupStepProjectConnect] = adapter
	if err := service.SetActionAdapter(specialist.SetupStepProjectConnect, adapter); err != nil {
		t.Fatal(err)
	}
	projection, err := service.Read(ctx, "local", "")
	if err != nil {
		t.Fatal(err)
	}
	if first := projection.Steps[0]; first.Kind != specialist.SetupStepProjectConnect || len(first.Actions) != 1 || first.Actions[0].ID != ActionReviewCreateGroup {
		t.Fatalf("first launch step = %+v", first)
	}
	revision := projection.StateRevision
	input := json.RawMessage(`{"name":"My Studio"}`)
	review, err := service.Mutate(ctx, "local", projection.RunID, ActionReviewCreateGroup, ActionMutation{IfRevision: revision, IdempotencyKey: "group-review", Input: input})
	if err != nil || review.Review.Group.Name != "My Studio" || review.Journey.Receipts.HomeWorkspaceID != "" {
		t.Fatalf("review: %+v %v", review, err)
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
	project := created.Journey.Steps[0]
	if project.Preparation == nil || !project.Preparation.Exists || project.Status == StepComplete {
		t.Fatalf("group creation implied project readiness: %+v", project)
	}
	offers := map[ActionID]bool{}
	for _, action := range project.Actions {
		offers[action.ID] = true
	}
	if !offers[ActionReviewNewProject] || offers[ActionReviewCreateGroup] {
		t.Fatalf("project review was not offered once the group existed: %+v", project.Actions)
	}
	// The guided Home names the same Group Template the creator lists, so both
	// surfaces describe one program Home rather than two look-alike groups.
	if id := review.Review.Group.GroupTemplateID; !projecttemplates.ValidManagedGroupTemplateID(id) || project.Preparation.GroupTemplateID != id {
		t.Fatalf("group template identity: review %q, preparation %q", id, project.Preparation.GroupTemplateID)
	}
	projectReview, err := service.Mutate(ctx, "local", projection.RunID, ActionReviewNewProject, ActionMutation{
		IfRevision: created.Journey.StateRevision, IdempotencyKey: "project-review",
		Input: json.RawMessage(`{"mode_id":"new_project","workspace_name":"Song","project_name":"Song"}`),
	})
	if err != nil || projectReview.Review == nil || projectReview.Review.ProjectConnection == nil {
		t.Fatalf("project review after the group: %+v %v", projectReview, err)
	}
	if _, known := NormalizeActionID("acknowledge_preparation"); known {
		t.Fatal("the preparation acknowledgement is still a compiled action")
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

func TestHomePreparationRejectsMalformedGroupTemplateIdentity(t *testing.T) {
	valid := projectconnection.HomePreparation{Name: "Studio", TemplateID: "plugin:neutral:song", GroupTemplateID: "group-template:" + "0123456789abcdef0123456789abcdef"}
	if !validHomePreparation(&valid) {
		t.Fatal("rejected a well-formed group template identity")
	}
	for _, id := range []string{"general", "group-template:../../etc", "plugin:neutral:song"} {
		invalid := valid
		invalid.GroupTemplateID = id
		if validHomePreparation(&invalid) {
			t.Fatalf("accepted group template identity %q", id)
		}
	}
}

func TestGroupInputRejectsUnknownFieldsAndPaths(t *testing.T) {
	for _, input := range []string{`{"name":"Group","owner":"other"}`, `{"name":"/private/home"}`, `{"name":"Group"} {}`, `{"name":""}`} {
		if _, err := preparationInputDigest(ActionCreateGroup, json.RawMessage(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}
