package sessionhttp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// neutralProjectReply picks the policy fixture's blueprint, which must be
// created inside Neutral Program Home.
const neutralProjectReply = `{"say":"Neutral Project fits. It goes inside Neutral Program Home.",
"ask":{"question":"","choices":[],"allow_free_text":true},
"patch":{"set":["blueprint_id","name","description"],"blueprint_id":"plugin:neutral:project","name":"Field Notes","description":"Notes from the field.",
"inputs":[],"parent_id":"","ask_folder":false,"team":{"mode":"","agents":[],"roles":[],"saved_agents":[]},"tags":[],"color":""},
"why":[],"alternatives":[],"ready":false,"create_now":false}`

func groupPrerequisiteOf(t *testing.T, body map[string]any) *templateGroupRequirementPlan {
	t.Helper()
	session, _ := body["session"].(map[string]any)
	raw, ok := session["group_prerequisite"]
	if !ok {
		return nil
	}
	data, _ := json.Marshal(raw)
	var plan templateGroupRequirementPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatalf("decode group_prerequisite: %v", err)
	}
	return &plan
}

func TestWorkspaceBuildGroupPrerequisite_SaysTheGroupStillToPrepareThenTheOneThatIsReady(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	handler, _, cleanup := newPolicyHandler(t, &template)
	t.Cleanup(cleanup)
	installPlanAgentStore(t, handler)
	f := newBuildFixtureOn(t, handler, true, neutralProjectReply)
	f.catalog = append(f.catalog, readyEntry(template))

	build := f.start(t)
	if _, body := f.do(t, http.MethodGet, "/api/workspaces/build-sessions/"+build.ID, nil); groupPrerequisiteOf(t, body) != nil {
		t.Fatalf("a draft with no blueprint has no group prerequisite: %v", body["session"])
	}
	status, body := f.turn(t, build, map[string]any{"text": "notes from the field"})
	if status != http.StatusOK {
		t.Fatalf("turn status %d: %v", status, body)
	}
	if got := sessionOf(t, body); got.NeedsHome == nil || got.Draft.TemplateID != template.ID {
		t.Fatalf("draft = %+v, needs_home = %+v", got.Draft, got.NeedsHome)
	}
	prerequisite := groupPrerequisiteOf(t, body)
	if prerequisite == nil || prerequisite.State != grouprequirements.StateHomeCreationReviewRequired ||
		prerequisite.Home == nil || prerequisite.Home.Exists || prerequisite.Home.ProposedName != "Neutral Program Home" ||
		!slicesContainAction(prerequisite.Actions, grouprequirements.ActionReviewCreateHome) {
		t.Fatalf("before the Home exists, group_prerequisite = %+v", prerequisite)
	}

	// The form's own Home review and commit prepare the group; the build's
	// next response names the Home that now exists.
	homeID := preparePolicyHome(t, handler)
	_, body = f.do(t, http.MethodGet, "/api/workspaces/build-sessions/"+build.ID, nil)
	prerequisite = groupPrerequisiteOf(t, body)
	if prerequisite == nil || prerequisite.State != grouprequirements.StateReadyGrouped ||
		prerequisite.Home == nil || !prerequisite.Home.Exists || prerequisite.Home.WorkspaceID != homeID ||
		prerequisite.Home.Name != "Neutral Program Home" {
		t.Fatalf("after the Home exists, group_prerequisite = %+v (home %s)", prerequisite, homeID)
	}
}

// The rule the model hears says what is true: the group is prepared first,
// with the user's confirmation, and the workspace is created inside it.
func TestWorkspaceBuildPrompt_TheGroupIsPreparedFirstNotSetUpByTheWorkspace(t *testing.T) {
	f := newBuildFixture(t, true, describeReply)
	build := f.start(t)
	f.turn(t, build, map[string]any{"text": "a newsletter"})
	if len(f.provider.systems) == 0 {
		t.Fatal("no model turn ran")
	}
	system := f.provider.systems[0]
	if strings.Contains(system, "lives inside a group it sets up") {
		t.Fatalf("the prompt still says the workspace sets up its group:\n%s", system)
	}
	for _, want := range []string{
		"Ori prepares it first, only after the user confirms, and then creates the workspace inside it",
		"never ask whether to create the group and never offer it as a choice",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("the prompt lacks %q:\n%s", want, system)
		}
	}
}

// A recognized folder one install away from its plan does not also ask for the
// group: once its Home's plugin is installed, the plan sets the group up.
func TestWorkspaceBuildGroupPrerequisite_WaitsWhileTheInstallIsOffered(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	handler, _, cleanup := newPolicyHandler(t, &template)
	t.Cleanup(cleanup)
	installPlanAgentStore(t, handler)
	f := newBuildFixtureOn(t, handler, true, neutralProjectReply)
	f.catalog = append(f.catalog, readyEntry(template))
	f.withFolders(t, &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: waitingPlan()})
	f.handler.workspaceBuild.HomeProvider = &fakeHomeProvider{release: "0.3.0"}

	build := f.start(t)
	_, attached := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
	status, body := f.turn(t, sessionOf(t, attached), map[string]any{"text": "notes from the field"})
	if status != http.StatusOK || sessionOf(t, body).NeedsHome == nil {
		t.Fatalf("turn %d: %v", status, body["session"])
	}
	if installProviderOf(body) == nil || groupPrerequisiteOf(t, body) != nil {
		t.Fatalf("while the install is offered, the group waits: %v", body["session"])
	}
	// Without an install to offer, the group is said as before.
	f.handler.workspaceBuild.HomeProvider = nil
	_, body = f.do(t, http.MethodGet, "/api/workspaces/build-sessions/"+build.ID, nil)
	if groupPrerequisiteOf(t, body) == nil {
		t.Fatalf("with no install offered the group prerequisite is said: %v", body["session"])
	}
}

func TestWorkspaceBuildGroupPrerequisite_AbsentWhenTheDraftNeedsNoGroup(t *testing.T) {
	f := newBuildFixture(t, true, describeReply)
	build := f.start(t)
	_, body := f.turn(t, build, map[string]any{"text": "a newsletter"})
	if got := sessionOf(t, body); got.Draft.TemplateID != "content-production" || got.NeedsHome != nil {
		t.Fatalf("draft = %+v", got.Draft)
	}
	if prerequisite := groupPrerequisiteOf(t, body); prerequisite != nil {
		t.Fatalf("a blueprint without a group requirement has no prerequisite: %+v", prerequisite)
	}
}
