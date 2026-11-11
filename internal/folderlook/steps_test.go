package folderlook

import (
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldermap"
)

func portfolioFacts() foldermap.Facts {
	var members []foldermap.Project
	for i := 1; i <= 6; i++ {
		members = append(members, foldermap.Project{Path: fmt.Sprintf("Albums/Album-%d", i), Depth: 2, Shape: "audio", Format: "reaper", Marker: "*.rpp"})
	}
	return foldermap.Facts{Members: members, Portfolio: &foldermap.Portfolio{Shape: "audio", Count: 6,
		HomeProviderKey: "music_project_management", HomeName: "Music Production Home"}}
}

func TestSteps_CatalogAcrossStagesWithReadableHandles(t *testing.T) {
	candidates := []Candidate{
		{ID: "home:music_project_management", Kind: KindHomeProvider, Name: "Music Production Home", Action: ActionAlreadyHere, Required: true, Why: "Keeps every song here on one shelf, at any depth."},
		{ID: "integration:ori_reaper", Kind: KindIntegration, Name: "REAPER tools", Action: ActionConnect, Required: true, App: "REAPER", Why: "Lets the agent work with the REAPER songs here."},
		{ID: "search:mix", Kind: KindSkill, Name: "Mix helper", Source: SourceSearch, Marketplace: "Skills", Action: ActionAdd, Ranked: true, Why: "Ranked."},
		{ID: "search:mix2", Kind: KindSkill, Name: "Mix Helper!", Source: SourceSearch, Marketplace: "Skills", Action: ActionAdd},
		{ID: "search:old", Kind: KindSkill, Name: "Old", Action: ActionAlreadyHere},
	}
	steps := Steps("Albums", portfolioFacts(), candidates)
	var handles, defaults []string
	for _, s := range steps {
		handles = append(handles, s.Handle)
		if s.Default {
			defaults = append(defaults, s.Handle)
		}
	}
	if strings.Join(handles, ",") != "music-production-home,reaper-tools,mix-helper,mix-helper-2,group" {
		t.Fatalf("handles = %v", handles)
	}
	// Ori's default plan is the Home stage's: the Home, its tools, the ranked
	// add-on; the group is offered but not default.
	if strings.Join(defaults, ",") != "music-production-home,reaper-tools,mix-helper" {
		t.Fatalf("defaults = %v", defaults)
	}
	songs := foldermap.ShapeNoun("audio", 6)
	if steps[0].Title != "Add Albums to your Music Production Home and list its 6 "+songs+" on the shelf" || steps[0].Excludes != "group" {
		t.Fatalf("home step = %+v", steps[0])
	}
	if steps[0].Name != "Music Production Home" || steps[4].Name != "a group" {
		t.Fatalf("names = %q %q", steps[0].Name, steps[4].Name)
	}
	// The application is on this Mac but its plugin is not: still an install.
	if steps[1].Title != "Install REAPER tools and connect them to Albums" || steps[1].Why != "Lets the agent work with the REAPER songs here." {
		t.Fatalf("integration step = %+v", steps[1])
	}
	if steps[2].Label != "Found by search · Skills" || steps[2].Title != "Add the skill Mix helper" {
		t.Fatalf("add-on step = %+v", steps[2])
	}
	group := steps[4]
	song := foldermap.ShapeNoun("audio", 1)
	if group.Kind != StepGroup || group.Excludes != "music-production-home" || group.Default ||
		group.Title != "Turn Albums into a group with one workspace per "+song+" (6)" || group.Why != "6 "+songs+" sit in their own folders here." {
		t.Fatalf("group step = %+v", group)
	}
	if got := StepHandles(steps); len(got) != 5 || !got["group"] {
		t.Fatalf("handles set = %v", got)
	}
	if line := DefaultLine(steps); line != "Set it up: "+steps[0].Title+" · "+steps[1].Title+" · "+steps[2].Title {
		t.Fatalf("default line = %q", line)
	}

	// Without the Home yet, the folder becomes a group by default and the Home
	// is offered for the agent to recommend.
	fresh := append([]Candidate(nil), candidates...)
	fresh[0].Action = ActionInstall
	offered := Steps("Albums", portfolioFacts(), fresh)
	// The group leads the plan; the Home is offered after it.
	if offered[0].Handle != "group" || !offered[0].Default || offered[1].Default ||
		offered[1].Title != "Create your Music Production Home and put Albums on its shelf (6 "+songs+")" {
		t.Fatalf("no-home steps = %+v / %+v", offered[0], offered[1])
	}
	if line := DefaultLine(offered); line != "Set it up: "+offered[0].Title+" · "+offered[2].Title+" · "+offered[3].Title {
		t.Fatalf("no-home default line = %q", line)
	}
	if line := DefaultLine(nil); line != "Nothing to set up yet: Ori found no project it knows here." {
		t.Fatalf("empty default line = %q", line)
	}
}

// The title rule (PRD FR23) follows the plugin's readiness, never the app check.
func TestSteps_IntegrationTitleFollowsThePlugin(t *testing.T) {
	song := foldermap.Facts{RootProject: &foldermap.Project{Shape: "audio", Format: "reaper", Marker: "*.rpp"}}
	cases := []struct {
		action, title, sentence string
	}{
		{ActionInstall, "Install REAPER tools and connect them to One Song", "REAPER is not on this Mac yet; the tools install anyway and work once it is."},
		{ActionConnect, "Install REAPER tools and connect them to One Song", "REAPER is on this Mac; its Ori tools are not yet."},
		{ActionAlreadyHere, "Connect REAPER tools to One Song", ""},
	}
	for _, tc := range cases {
		c := Candidate{ID: "integration:ori_reaper", Kind: KindIntegration, Name: "REAPER tools", Action: tc.action, Required: true, App: "REAPER"}
		steps := Steps("One Song", song, []Candidate{c})
		if len(steps) != 1 || steps[0].Title != tc.title {
			t.Fatalf("%s: steps = %+v", tc.action, steps)
		}
		if got := AppToolsSentence(c); got != tc.sentence {
			t.Fatalf("%s: sentence = %q", tc.action, got)
		}
	}
}

func TestSteps_DumpAndSingleProject(t *testing.T) {
	dump := Steps("Messy", foldermap.Facts{Dump: true, RootLooseFiles: 36, RootLooseKinds: 6},
		RequiredCandidates(foldermap.Facts{Dump: true}, Readiness{}))
	if len(dump) != 1 || dump[0].Handle != "file-curator" || !dump[0].Default || dump[0].Title != "Add File Janitor to Messy, with File Curator" {
		t.Fatalf("dump steps = %+v", dump)
	}
	song := foldermap.Facts{RootProject: &foldermap.Project{Shape: "audio", Format: "reaper", Marker: "*.rpp"}}
	install := []Candidate{{ID: "integration:ori_reaper", Kind: KindIntegration, Name: "REAPER tools", Action: ActionInstall, Required: true, App: "REAPER", Why: "Lets the agent work with the REAPER songs here."}}
	single := Steps("One Song", song, install)
	if len(single) != 1 || single[0].Title != "Install REAPER tools and connect them to One Song" ||
		single[0].Why != "Lets the agent work with the REAPER songs here. REAPER is not on this Mac yet." {
		t.Fatalf("single steps = %+v", single)
	}
	if got := AppToolsSentence(install[0]); got != "REAPER is not on this Mac yet; the tools install anyway and work once it is." {
		t.Fatalf("install sentence = %q", got)
	}
	if none := Steps("Notes", foldermap.Facts{}, nil); len(none) != 0 {
		t.Fatalf("nothing recognized offers steps: %+v", none)
	}
}
