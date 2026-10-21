package sessionhttp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	musicHomeCarrierID = "plugin-home:music-project-management:music-producer-assistant"
	reaperSongID       = "plugin:reaper-plugin:reaper-song"
)

// songDigest is the song plan's digest, shaped like the host's (sha256 hex).
var songDigest = strings.Repeat("d", 64)

// withMusicCatalog adds the two music templates as a catalog would list them
// before the server filters it: the REAPER plugin's song blueprint and the
// Music plugin's Home carrier the Group creator makes Music Production Homes
// from.
func (f *buildFixture) withMusicCatalog() {
	f.catalog = append(f.catalog,
		readyEntry(projecttemplates.Template{
			ID: reaperSongID, Name: "Reaper Song", Tags: []string{"music", "reaper"},
			PluginOwner:       &agentworkspace.PluginTemplateOwner{PluginID: "reaper-plugin", PluginVersion: "0.10.1", BlueprintID: "reaper-song", BlueprintVersion: 10},
			Agents:            []projecttemplates.AgentSpec{{Name: "Producer"}},
			ProjectConnection: &projecttemplates.ProjectConnectionDeclaration{SupportedModes: []projecttemplates.ProjectConnectionMode{"existing_project"}},
			GroupRequirement:  &projecttemplates.GroupRequirement{SchemaVersion: 1, Policy: projecttemplates.GroupPolicyRequired, DefaultHomeName: "Music Production Home"},
		}),
		readyEntry(projecttemplates.Template{
			ID: musicHomeCarrierID, Name: "Music Production Home", Tags: []string{"music"},
			ProgramHomeOwner: &agentworkspace.AssistantProgramHomeOwner{PluginID: "music-project-management", ProgramID: "music-producer-assistant"},
		}),
	)
}

// carrierReply is the 2026-10-19 reply: the model picked the Home carrier as
// the blueprint for a song folder.
const carrierReply = `{"say":"Music Production Home fits your demos. I named it Ori Music Demo.",
"ask":{"question":"","choices":[],"allow_free_text":true},
"patch":{"set":["blueprint_id","name"],"blueprint_id":"` + musicHomeCarrierID + `","name":"Ori Music Demo","description":"",
"inputs":[],"parent_id":"","ask_folder":false,"team":{"mode":"","agents":[],"roles":[],"saved_agents":[]},"tags":[],"color":""},
"why":[],"alternatives":[],"ready":false,"create_now":false}`

// songReply is the answer the recognition asks for.
const songReply = `{"say":"This is a REAPER project, so I chose REAPER song. It goes inside Music Production Home, which Ori sets up for it.",
"ask":{"question":"","choices":[],"allow_free_text":true},
"patch":{"set":["blueprint_id","name","description"],"blueprint_id":"` + reaperSongID + `","name":"Ori Music Demo","description":"Demos for the next release.",
"inputs":[],"parent_id":"","ask_folder":false,"team":{"mode":"","agents":[],"roles":[],"saved_agents":[]},"tags":[],"color":""},
"why":[],"alternatives":[],"ready":false,"create_now":false}`

func TestWorkspaceBuildPrompt_ARecognizedFolderSuggestsItsBlueprintAndNeverAHomeCarrier(t *testing.T) {
	f := newBuildFixture(t, true, carrierReply, songReply)
	f.withMusicCatalog()
	f.withFolders(t, &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition()})
	build := f.start(t)
	_, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
	_, body = f.turn(t, sessionOf(t, body), map[string]any{"text": "music production demos"})
	got := sessionOf(t, body)

	// The carrier is refused as not offered, the retry hears that, and the
	// suggested song blueprint is what the draft ends up with.
	if len(f.provider.systems) != 2 || !strings.Contains(f.provider.systems[1], `{"field":"blueprint_id","reason":"not_available"}`) {
		t.Fatalf("the retry hears the carrier was refused as not_available: %d calls", len(f.provider.systems))
	}
	if got.Draft.TemplateID != reaperSongID || got.NeedsHome == nil || got.NeedsHome.Label != "Music Production Home" {
		t.Fatalf("draft = %+v, needs_home = %+v", got.Draft, got.NeedsHome)
	}

	// The table's "reaper-song" is offered as the plugin's catalog id.
	system := f.provider.systems[0]
	for _, want := range []string{
		"<folder_recognition>", `"kind":"project"`, `"label":"REAPER project"`, `"suggested_blueprint_id":"` + reaperSongID + `"`,
		`"home_provider_key":"music_project_management"`, `"home_name":"Music Production Home"`, `"home_exists":false`,
		"untrusted reference data", "Choose suggested_blueprint_id", "never a blueprint", "Say what will be set up, not that anything is created",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt lacks %q:\n%s", want, system)
		}
	}
	if strings.Contains(system, musicHomeCarrierID) {
		t.Fatalf("the Home carrier is in the model's catalog:\n%s", system)
	}
	for _, private := range []string{"candidate-0", "obs-reaper", `"offer_id"`} {
		if strings.Contains(system, private) {
			t.Fatalf("ids never reach the model (%q):\n%s", private, system)
		}
	}
}

// songPlan is the host plan for the REAPER song folder: the integration is in
// place, the Home provider is to be installed, and a new Home is made.
func songPlan() *personalassistant.BuildFolderPlan {
	return &personalassistant.BuildFolderPlan{
		Lines: []personalassistant.FolderPlanLine{
			{Kind: "provider", Name: "Installs the Music Project Management plugin"},
			{Kind: "home", Name: "Creates Home “Music Production Home”", Detail: "Your later projects of this kind join it."},
			{Kind: "workspace", Name: "Creates a REAPER song workspace named Ori Music Demo"},
			{Kind: "folder", Name: "Links Ori Music Demo where it is", Detail: "Nothing is moved or copied."},
		},
		Digest: songDigest,
		Needs: []personalassistant.BuildFolderNeed{
			{Kind: "integration", Name: "REAPER integration", State: personalassistant.FolderInstallReady},
			{Kind: "provider", Name: "Music Project Management", State: personalassistant.FolderInstallInstall},
		},
		Home:      &personalassistant.BuildFolderHome{Name: "Music Production Home", Status: "new"},
		Placement: "grouped", Evidence: "Has a REAPER session, edited yesterday", Ready: true,
		Hire:         &personalassistant.FolderHire{Mode: personalassistant.FolderHirePending, RoleLabel: "Song"},
		BriefDefault: "Keep the session tidy and say what is unfinished.",
	}
}

// plannedReply narrates the plan and names the workspace, as the planned
// rules ask.
const plannedReply = `{"say":"This is a REAPER project. Set it up adds the Music Project Management plugin, then makes Music Production Home with Ori Music Demo inside it.",
"ask":{"question":"","choices":[],"allow_free_text":true},
"patch":{"set":["name","description"],"blueprint_id":"","name":"Ori Music Demo","description":"Demos for the next release.",
"inputs":[],"parent_id":"","ask_folder":false,"team":{"mode":"","agents":[],"roles":[],"saved_agents":[]},"tags":[],"color":""},
"why":[],"alternatives":[],"ready":true,"create_now":false}`

// planOf is the plan a response carries on its session, or nil.
func planOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	session, _ := body["session"].(map[string]any)
	plan, _ := session["plan"].(map[string]any)
	return plan
}

func hostLines(session personalassistant.WorkspaceBuildSession) []string {
	var lines []string
	for _, entry := range session.Transcript {
		if entry.Host {
			lines = append(lines, entry.Text)
		}
	}
	return lines
}

func TestWorkspaceBuildPlan_EveryResponseCarriesTheHostPlanAndSaysItsNeedsOnce(t *testing.T) {
	f := newBuildFixture(t, true, plannedReply, plannedReply, plannedReply)
	f.withMusicCatalog()
	folders := &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan()}
	f.withFolders(t, folders)
	build := f.start(t)

	status, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
	if status != http.StatusOK {
		t.Fatalf("attach: %d %v", status, body)
	}
	plan := planOf(t, body)
	if plan == nil || plan["digest"] != songDigest || plan["ready"] != true || plan["placement"] != "grouped" {
		t.Fatalf("the attach response carries the plan: %v", body["session"])
	}
	attached := sessionOf(t, body)
	needs := "Needs: REAPER integration (installed) · Music Project Management (install)"
	if got := hostLines(attached); len(got) != 1 || got[0] != needs {
		t.Fatalf("host lines after attach = %q", got)
	}
	if last := lastEntry(attached); last.Role != personalassistant.BuildRoleForm || !last.Host || last.Text != needs {
		t.Fatalf("the Needs line follows the attach as a host form line: %+v", last)
	}
	// The draft has no name yet: the plan is read for the folder's name.
	if len(folders.planned) == 0 || folders.planned[0] != "" {
		t.Fatalf("planned names %q", folders.planned)
	}

	_, body = f.turn(t, attached, map[string]any{"text": "music production demos"})
	turned := sessionOf(t, body)
	if planOf(t, body) == nil || turned.Draft.Name != "Ori Music Demo" || turned.Draft.Description == "" {
		t.Fatalf("the turn names the workspace and keeps the plan: %+v", turned.Draft)
	}
	if got := hostLines(turned); len(got) != 1 {
		t.Fatalf("the same needs are never said twice: %q", got)
	}
	heard := false
	for _, message := range f.provider.messages[0] {
		heard = heard || message.Content == "[Ori noted] "+needs
	}
	if !heard {
		t.Fatalf("the model hears the Needs line as Ori's note, not the user's edit: %+v", f.provider.messages[0])
	}
	system := f.provider.systems[0]
	// The plan column answers the setup request (FR27): the reply never says
	// what it says.
	for _, want := range []string{"<setup_plan>", `"name":"Creates Home “Music Production Home”"`, `"state":"install"`, "You may set only name, description, goal", "Its setup plan fixes the blueprint and the group",
		"The plan column beside the conversation answers the setup request",
		"never name the hired assistant or its role, count or list the plan's steps, or describe its controls"} {
		if !strings.Contains(system, want) {
			t.Fatalf("planned prompt lacks %q:\n%s", want, system)
		}
	}
	for _, absent := range []string{"Blueprints (id", songDigest, "Choose suggested_blueprint_id", "Use Set it up to run the plan", `"hire"`, `"brief_default"`} {
		if strings.Contains(system, absent) {
			t.Fatalf("planned prompt still has %q:\n%s", absent, system)
		}
	}

	// The provider gets installed elsewhere: the next turn says the new states.
	folders.plan.Needs[1].State = personalassistant.FolderInstallReady
	_, body = f.turn(t, turned, map[string]any{"text": "sounds good"})
	again := sessionOf(t, body)
	if got := hostLines(again); len(got) != 2 || got[1] != "Needs: REAPER integration (installed) · Music Project Management (installed)" {
		t.Fatalf("changed needs are said again: %q", got)
	}
	// From now on the plan is read with the draft's own name.
	if got := folders.planned[len(folders.planned)-1]; got != "Ori Music Demo" {
		t.Fatalf("planned with %q, want the draft's name", got)
	}

	// A folder without a plan carries none.
	_, body = f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+again.ID+"/folder/detach", map[string]any{"version": again.Version})
	if planOf(t, body) != nil {
		t.Fatalf("no folder, no plan: %v", body["session"])
	}
}

// overreachReply sets everything at once, as the 2026-10-19 model would.
const overreachReply = `{"say":"I set it all up.",
"ask":{"question":"","choices":[],"allow_free_text":true},
"patch":{"set":["blueprint_id","name","description","goal","inputs","parent_id","ask_folder","team","tags","color"],
"blueprint_id":"` + reaperSongID + `","name":"Ori Music Demo","description":"Demos for the next release.","goal":"Finish three demos.",
"inputs":[{"id":"tempo","value":"120"}],"parent_id":"group-1","ask_folder":true,
"team":{"mode":"agentless","agents":[],"roles":[{"role_id":"producer","mode":"create","agent_name":"Producer"}],"saved_agents":["scout"]},
"tags":["music"],"color":"#ef4444"},
"why":[],"alternatives":[],"ready":true,"create_now":false}`

func TestWorkspaceBuildPlan_TheRecognizedPathSetsOnlyWhatThePlanLeavesOpen(t *testing.T) {
	f := newBuildFixture(t, true, overreachReply)
	f.withMusicCatalog()
	f.withFolders(t, &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan()})
	build := f.start(t)
	_, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
	_, body = f.turn(t, sessionOf(t, body), map[string]any{"text": "music production demos"})
	got := sessionOf(t, body)

	if got.Draft.Name != "Ori Music Demo" || got.Draft.Description != "Demos for the next release." || got.Draft.Mission != "Finish three demos." {
		t.Fatalf("name, description and goal are the assistant's to set: %+v", got.Draft)
	}
	if got.Draft.TemplateID != "" || got.Draft.ParentID != "" || len(got.Draft.BlueprintInputs) != 0 || len(got.Draft.Tags) != 0 || got.Draft.Color != "" || got.AskFolder {
		t.Fatalf("the plan's fields stay the plan's: %+v ask_folder=%v", got.Draft, got.AskFolder)
	}
	if got.TeamPatch == nil || len(got.TeamPatch.SavedAgents) != 1 || got.TeamPatch.SavedAgents[0] != "Scout" || len(got.TeamPatch.Roles) != 0 {
		t.Fatalf("one extra saved teammate, by their saved name: %+v", got.TeamPatch)
	}
	refused := map[string]string{}
	for _, rejection := range got.Rejections {
		refused[rejection.Field] = rejection.Reason
	}
	for _, field := range []string{"blueprint_id", "inputs", "parent_id", "ask_folder", "tags", "color", "team.roles", "team.mode"} {
		if refused[field] != buildReasonSetByPlan {
			t.Fatalf("%s refused as %q, want %q (all: %v)", field, refused[field], buildReasonSetByPlan, refused)
		}
	}
	if _, ok := refused["name"]; ok {
		t.Fatalf("the name is never refused here: %v", refused)
	}
}

func TestWorkspaceBuildPlan_MoreThanOneTeammateOrAPlanThatIsNotReady(t *testing.T) {
	twoTeammates := strings.Replace(plannedReply, `"set":["name","description"]`, `"set":["name","description","team"]`, 1)
	twoTeammates = strings.Replace(twoTeammates, `"saved_agents":[]`, `"saved_agents":["Scout","Luna"]`, 1)
	f := newBuildFixture(t, true, twoTeammates)
	f.withFolders(t, &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: songPlan()})
	build := f.start(t)
	_, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
	_, body = f.turn(t, sessionOf(t, body), map[string]any{"text": "music production demos"})
	got := sessionOf(t, body)
	if got.TeamPatch != nil || len(got.Rejections) != 1 || got.Rejections[0] != (personalassistant.BuildRejection{Field: "team.saved_agents", Reason: buildReasonOneTeammate}) {
		t.Fatalf("two teammates: team %+v rejections %+v", got.TeamPatch, got.Rejections)
	}

	// A plan that cannot run now leaves the build to choose a blueprint.
	blocked := songPlan()
	blocked.Ready, blocked.Digest, blocked.Unavailable = false, "", "integration_unavailable"
	f = newBuildFixture(t, true, songReply)
	f.withMusicCatalog()
	f.withFolders(t, &fakeBuildFolders{observation: reaperObservation(), recognition: reaperRecognition(), plan: blocked})
	build = f.start(t)
	_, body = f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
	_, body = f.turn(t, sessionOf(t, body), map[string]any{"text": "music production demos"})
	if got := sessionOf(t, body); got.Draft.TemplateID != reaperSongID {
		t.Fatalf("an unavailable plan does not fix the blueprint: %+v", got.Draft)
	}
	if strings.Contains(f.provider.systems[0], "<setup_plan>") || !strings.Contains(f.provider.systems[0], "Blueprints (id") {
		t.Fatal("an unavailable plan is not given to the model")
	}
}

func TestWorkspaceBuildPrompt_WhatIsNotOneProjectIsSaidAsSuch(t *testing.T) {
	cases := []struct {
		name        string
		recognition *personalassistant.BuildFolderRecognition
		want        []string
		block       bool
	}{
		{"unrecognized", &personalassistant.BuildFolderRecognition{Verdict: personalassistant.BuildFolderUnrecognized}, []string{"Ori does not recognize a kind of project"}, false},
		{"a collection", &personalassistant.BuildFolderRecognition{Verdict: personalassistant.BuildFolderCollection, ProjectCount: 7, Shape: "audio"}, []string{"holding 7 separate projects", "Explore a folder on Home", `"kind":"collection"`}, true},
		// No REAPER plugin, so no song blueprint in the catalog.
		{"an uninstalled blueprint", reaperRecognition(), []string{"is not available here", `"suggested_blueprint_id":""`, `"suggested_blueprint":"REAPER song"`}, true},
		{"an existing Home", func() *personalassistant.BuildFolderRecognition {
			r := reaperRecognition()
			r.HomeExists = true
			return r
		}(), []string{"The user already has Music Production Home", "no second group is made", `"home_exists":true`}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newBuildFixture(t, true, describeReply)
			if tc.name == "an existing Home" {
				f.withMusicCatalog()
			}
			f.withFolders(t, &fakeBuildFolders{observation: reaperObservation(), recognition: tc.recognition})
			build := f.start(t)
			_, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
			f.turn(t, sessionOf(t, body), map[string]any{"text": "music demos"})
			system := f.provider.systems[0]
			for _, want := range tc.want {
				if !strings.Contains(system, want) {
					t.Fatalf("system prompt lacks %q:\n%s", want, system)
				}
			}
			if strings.Contains(system, "<folder_recognition>") != tc.block {
				t.Fatalf("recognition block present=%v, want %v", !tc.block, tc.block)
			}
		})
	}
}
