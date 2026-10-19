package sessionhttp

import (
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
