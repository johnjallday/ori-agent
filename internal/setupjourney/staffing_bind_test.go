package setupjourney

import (
	"context"
	"errors"
	"testing"

	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// existingAgentName is the saved agent these tests bind to. It is a constant
// rather than a parameter because every test wants the same one — the point is
// always "an agent the user already had", never which one.
const existingAgentName = "My Reviewer"

// saveExistingAgent creates a saved agent definition the way the user's own
// /agents page would, so bind mode has something real to attach.
func saveExistingAgent(t *testing.T, adapter *AssistantStaffingAdapter) {
	t.Helper()
	if err := adapter.profiles.CreateAgent(existingAgentName, &agentstore.CreateAgentConfig{
		Type: "tool-calling", Role: "specialist", LLMProvider: "openai", Model: "gpt-4o-mini",
		SystemPrompt: "the user's own prompt",
	}); err != nil {
		t.Fatal(err)
	}
}

// TestAssistantStaffingAdapter_BindAttachesExistingAgentWithoutCreating is the
// case that was impossible before this feature: staffing_adapter.go rejected
// any requested name a profile already owned, so "use the agent I already
// have" could not be expressed at the domain level at all.
func TestAssistantStaffingAdapter_BindAttachesExistingAgentWithoutCreating(t *testing.T) {
	adapter, workspaces, scope, grants := staffingFixture(t)
	saveExistingAgent(t, adapter)
	before := len(adapter.profiles.ListAgents())

	input := []byte(`{"roles":[{"role_id":"project_lead","name":"Alex Lead"},{"role_id":"project_reviewer","name":"My Reviewer","mode":"bind"}]}`)
	review, err := adapter.Review(context.Background(), scope, ActionReviewProjectStaffing, input)
	if err != nil {
		t.Fatal(err)
	}
	var reviewed StaffingRoleProjection
	for _, role := range review.Staffing.Scopes[0].Roles {
		if role.RoleID == "project_reviewer" {
			reviewed = role
		}
	}
	if !reviewed.Bound || reviewed.ProfileName != "My Reviewer" || reviewed.Model != "gpt-4o-mini" {
		t.Fatalf("bound role review = %#v", reviewed)
	}
	if _, err := adapter.Commit(context.Background(), scope, ActionAddProjectStaffing, input, review); err != nil {
		t.Fatal(err)
	}

	// Exactly one definition was created: the create-mode role. Binding added
	// none, and left the user's agent untouched.
	if after := len(adapter.profiles.ListAgents()); after != before+1 {
		t.Fatalf("agent definitions after staffing = %d, want %d", after, before+1)
	}
	bound, found := adapter.profiles.GetAgent("My Reviewer")
	if !found || bound.Settings.SystemPrompt != "the user's own prompt" {
		t.Fatalf("bound agent was mutated: found=%v agent=%#v", found, bound)
	}
	if grants.granted["My Reviewer"] != nil {
		t.Fatalf("binding granted skills to the user's own agent: %#v", grants.granted["My Reviewer"])
	}

	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	var attached workspace.AgentInstance
	for _, instance := range project.GetAgentInstances() {
		if instance.Name == "My Reviewer" {
			attached = instance
		}
	}
	if attached.RoleID != "project_reviewer" || attached.RoleSource != workspace.RoleSourceAssigned {
		t.Fatalf("bound attachment = %#v", attached)
	}
	if snapshot, found, _ := workspaces.GetWorkspaceAgent(project.ID, "My Reviewer"); !found || snapshot == nil {
		t.Fatal("bound agent was not attached to the workspace")
	}
}

// TestAssistantStaffingAdapter_CreateStillRejectsAnExistingProfileName pins
// FR50: relaxing the rule for bind must not relax it for create, or a create
// would silently adopt the user's existing agent instead of making a new one.
func TestAssistantStaffingAdapter_CreateStillRejectsAnExistingProfileName(t *testing.T) {
	adapter, _, scope, _ := staffingFixture(t)
	saveExistingAgent(t, adapter)

	input := []byte(`{"roles":[{"role_id":"project_lead","name":"Alex Lead"},{"role_id":"project_reviewer","name":"My Reviewer"}]}`)
	if _, err := adapter.Review(context.Background(), scope, ActionReviewProjectStaffing, input); err == nil {
		t.Fatal("create mode accepted a name an existing profile already owns")
	}
}

func TestAssistantStaffingAdapter_BindRejectsAnAgentThatDoesNotExist(t *testing.T) {
	adapter, _, scope, _ := staffingFixture(t)
	input := []byte(`{"roles":[{"role_id":"project_lead","name":"Alex Lead"},{"role_id":"project_reviewer","name":"Nobody","mode":"bind"}]}`)
	if _, err := adapter.Review(context.Background(), scope, ActionReviewProjectStaffing, input); err == nil {
		t.Fatal("bind accepted a name with no saved agent behind it")
	}
}

// TestAssistantStaffingAdapter_BindFailsClearlyWhenTheAgentIsDeletedMidFlight
// covers FR52: the review said "attach My Reviewer", the user deleted it on
// /agents, and the commit must say so rather than binding a name with nothing
// behind it.
func TestAssistantStaffingAdapter_BindFailsClearlyWhenTheAgentIsDeletedMidFlight(t *testing.T) {
	adapter, workspaces, scope, _ := staffingFixture(t)
	saveExistingAgent(t, adapter)

	input := []byte(`{"roles":[{"role_id":"project_lead","name":"Alex Lead"},{"role_id":"project_reviewer","name":"My Reviewer","mode":"bind"}]}`)
	review, err := adapter.Review(context.Background(), scope, ActionReviewProjectStaffing, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.profiles.DeleteAgent("My Reviewer"); err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Commit(context.Background(), scope, ActionAddProjectStaffing, input, review)
	if !errors.Is(err, ErrBoundAgentMissing) {
		t.Fatalf("commit error = %v, want ErrBoundAgentMissing", err)
	}
	// The create-mode role in the same request is rolled back, so a retry sees
	// a clean slate rather than a half-staffed workspace.
	if _, found := adapter.profiles.GetAgent("Alex Lead"); found {
		t.Fatal("the created agent survived a failed commit")
	}
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	if len(project.GetAgentInstances()) != 0 {
		t.Fatalf("failed commit attached instances: %#v", project.GetAgentInstances())
	}
}

// TestAssistantStaffingAdapter_FailedCreateLeavesABoundAgentAlive is the
// highest-risk defect in this feature stated as a test: rollback must delete
// only what the request created. An assigned agent is the user's own, and
// deleting it is unrecoverable (FR51).
func TestAssistantStaffingAdapter_FailedCreateLeavesABoundAgentAlive(t *testing.T) {
	adapter, workspaces, scope, grants := staffingFixture(t)
	saveExistingAgent(t, adapter)

	input := []byte(`{"roles":[{"role_id":"project_lead","name":"Alex Lead"},{"role_id":"project_reviewer","name":"My Reviewer","mode":"bind"}]}`)
	review, err := adapter.Review(context.Background(), scope, ActionReviewProjectStaffing, input)
	if err != nil {
		t.Fatal(err)
	}
	// Make the create half of the request fail at its very last step, after
	// binding has already attached the user's agent.
	grants.available["project-skill"] = true
	grants.grantErr = map[string]error{"Alex Lead": errors.New("grant refused")}

	if _, err := adapter.Commit(context.Background(), scope, ActionAddProjectStaffing, input, review); err == nil {
		t.Fatal("commit succeeded despite a failing grant")
	}
	if _, found := adapter.profiles.GetAgent("My Reviewer"); !found {
		t.Fatal("rollback deleted the agent the user assigned to the role")
	}
	if _, found := adapter.profiles.GetAgent("Alex Lead"); found {
		t.Fatal("rollback kept the agent this request created")
	}
	project, _ := workspaces.Get(scope.ProjectWorkspaceID)
	if len(project.GetAgentInstances()) != 0 {
		t.Fatalf("failed commit attached instances: %#v", project.GetAgentInstances())
	}
}

func TestDecodeStaffingInput_ModeIsBackwardCompatible(t *testing.T) {
	omitted, err := decodeStaffingInput([]byte(`{"roles":[{"role_id":"a","name":"A"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := decodeStaffingInput([]byte(`{"roles":[{"role_id":"a","name":"A","mode":"create"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if omitted.Roles[0].binds() || explicit.Roles[0].binds() {
		t.Fatal("create mode decoded as bind")
	}
	// The digest is taken over this struct and a review receipt written before
	// modes existed must still match its commit, so naming the default must not
	// change the encoding.
	if string(mustJSON(t, omitted)) != string(mustJSON(t, explicit)) {
		t.Fatalf("explicit create re-encoded differently: %s vs %s", mustJSON(t, omitted), mustJSON(t, explicit))
	}

	if _, err := decodeStaffingInput([]byte(`{"roles":[{"role_id":"a","name":"A","mode":"delete"}]}`)); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
	if _, err := decodeStaffingInput([]byte(`{"roles":[{"role_id":"a","name":"A","mode":"bind","model":"gpt-4o"}]}`)); err == nil {
		t.Fatal("bind accepted a model it cannot apply")
	}
}
