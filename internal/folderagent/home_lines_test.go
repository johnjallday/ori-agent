package folderagent

import (
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// homeStageRecord is an Albums folder of six songs, one folder down, fed with
// the Home provider's facts as the host reported them.
func homeStageRecord(facts *folderlook.HomeFacts) Record {
	action := folderlook.ActionInstall
	if facts != nil && facts.HomeExists {
		action = folderlook.ActionAlreadyHere
	}
	members := []foldermap.Project{}
	for _, p := range []string{"Album-1", "Album-2", "Album-3", "Album-4", "Album-5", "Logic Sketch"} {
		members = append(members, foldermap.Project{Path: p, Depth: 1, Shape: "audio", Format: "reaper", Marker: "*.rpp"})
	}
	return Record{FolderName: "Albums", WorkspaceName: "Albums", AgentName: "Albums Manager", Stage: foldermap.StageHome,
		Facts: foldermap.Facts{Members: members, Portfolio: &foldermap.Portfolio{Shape: "audio", Count: 6,
			HomeProviderKey: "music_project_management", HomeName: "Music Production Home"}},
		Candidates: []folderlook.Candidate{{ID: "home:music_project_management", Kind: folderlook.KindHomeProvider,
			Name: "Music Production Home", Action: action, Required: true, HomeProviderKey: "music_project_management", Home: facts}}}
}

// homePlan is the plan with the Home step chosen (a new Home is offered, not
// chosen by default).
func homePlan(t *testing.T, rec Record) Plan {
	t.Helper()
	for _, step := range folderlook.Steps(rec.FolderName, rec.Facts, rec.Candidates) {
		if step.Kind == folderlook.StepHome {
			return BuildPlan(rec, []string{step.Handle})
		}
	}
	t.Fatal("no Home step offered")
	return Plan{}
}

func homeLineNames(plan Plan) []string {
	var names []string
	for _, l := range plan.Lines {
		if l.Kind == personalassistant.FolderPlanFolder {
			continue
		}
		names = append(names, l.Kind+": "+l.Name)
	}
	return names
}

func TestHomeLines_OneLinePerActionForEveryHomeState(t *testing.T) {
	tail := []string{
		"library: Adds Albums to your Music Production Home",
		"songs: Lists 6 songs on its shelf",
		"place: Places Albums inside your Music Production Home",
	}
	cases := []struct {
		name  string
		facts *folderlook.HomeFacts
		head  []string
	}{{
		name:  "no provider, no Home: install, create, roles named as they are hired",
		facts: &folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.5.0"},
		head: []string{
			"provider: Installs Music Project Management",
			"home: Creates your Music Production Home",
			"hire: Fills the Home's roles",
		},
	}, {
		name:  "provider present, no Home",
		facts: &folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.5.0", ProviderReady: true},
		head: []string{
			"home: Creates your Music Production Home",
			"hire: Fills the Home's roles",
		},
	}, {
		name: "Home exists with every role filled",
		facts: &folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderReady: true, HomeExists: true,
			RolesKnown: true},
	}, {
		name: "Home exists with roles partly filled",
		facts: &folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderReady: true, HomeExists: true,
			RolesKnown: true, MissingRoles: []string{"Portfolio Manager", "Library Manager"}},
		head: []string{"hire: Hires Portfolio Manager", "hire: Hires Library Manager"},
	}, {
		name: "Home exists, provider needs an update",
		facts: &folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.6.0", HomeExists: true,
			RolesKnown: true},
		head: []string{"provider: Installs Music Project Management"},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := homePlan(t, homeStageRecord(tc.facts))
			want := append(append([]string(nil), tc.head...), tail...)
			if got := homeLineNames(plan); !reflect.DeepEqual(got, want) {
				t.Fatalf("lines =\n%v\nwant\n%v", got, want)
			}
			if !plan.Runnable {
				t.Fatal("the plan does not run")
			}
		})
	}
}

func TestHomeLines_ARecordWithoutHostFactsKeepsTheGenericCreation(t *testing.T) {
	plan := homePlan(t, homeStageRecord(nil))
	want := []string{
		"home: Creates your Music Production Home",
		"library: Adds Albums to your Music Production Home",
		"songs: Lists 6 songs on its shelf",
		"place: Places Albums inside your Music Production Home",
	}
	if got := homeLineNames(plan); !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %v", got)
	}
}

func TestHomeLines_TheShownPlanIsThePlanThatRuns(t *testing.T) {
	facts := &folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.5.0"}
	shown := homePlan(t, homeStageRecord(facts))
	again := BuildPlan(homeStageRecord(facts), shown.Selected)
	if shown.Digest == "" || shown.Digest != again.Digest {
		t.Fatalf("digest of the shown plan %q != digest of the run plan %q", shown.Digest, again.Digest)
	}
	// The host learning that a role was filled meanwhile is a different plan.
	changed := homePlan(t, homeStageRecord(&folderlook.HomeFacts{ProviderName: "Music Project Management",
		ProviderVersion: "0.5.0", ProviderReady: true}))
	if changed.Digest == shown.Digest {
		t.Fatal("a plan without the install line kept the digest")
	}
}
