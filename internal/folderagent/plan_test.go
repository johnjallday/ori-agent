package folderagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

// planRecord is a looked-around portfolio: six songs two levels down, the
// Home already there, REAPER installed.
func planRecord() Record {
	var members []foldermap.Project
	for i := 1; i <= 6; i++ {
		members = append(members, foldermap.Project{Path: fmt.Sprintf("Albums/Album-%d", i), Depth: 2, Shape: "audio", Format: "reaper", Marker: "*.rpp"})
	}
	return Record{FolderName: "Albums", WorkspaceName: "Albums", AgentName: "Albums Manager", Stage: foldermap.StageHome,
		Facts: foldermap.Facts{Members: members, Portfolio: &foldermap.Portfolio{Shape: "audio", Count: 6, HomeProviderKey: "music_project_management", HomeName: "Music Production Home"}},
		Candidates: []folderlook.Candidate{
			{ID: "home:music_project_management", Kind: folderlook.KindHomeProvider, Name: "Music Production Home", Action: folderlook.ActionAlreadyHere, Required: true, HomeProviderKey: "music_project_management"},
			{ID: "integration:ori_reaper", Kind: folderlook.KindIntegration, Name: "REAPER tools", Action: folderlook.ActionConnect, Required: true, App: "REAPER", IntegrationKey: "ori_reaper"},
		}}
}

func lineNames(plan Plan) []string {
	var names []string
	for _, l := range plan.Lines {
		names = append(names, l.Name)
	}
	return names
}

func chosenHandles(plan Plan) []string {
	var out []string
	for _, s := range plan.Steps {
		if s.Chosen {
			out = append(out, s.Handle)
		}
	}
	return out
}

func TestBuildPlan_TheAgentsOrderWinsAndTheSecondOfAPairIsSetAside(t *testing.T) {
	rec := planRecord()
	rec.Sections = &folderlook.Sections{Plan: "Connect REAPER first, then make a group. Leave the Home.",
		PlanSteps: []string{"reaper-tools", "group", "music-production-home"}}
	plan := BuildPlan(rec, nil)
	if plan.Source != PlanByAgent || plan.Stage != foldermap.StageGroup || !plan.Runnable {
		t.Fatalf("plan source=%q stage=%q runnable=%v", plan.Source, plan.Stage, plan.Runnable)
	}
	if strings.Join(plan.Selected, ",") != "group,reaper-tools" || strings.Join(chosenHandles(plan), ",") != "reaper-tools,group" {
		t.Fatalf("selected = %v chosen = %v", plan.Selected, chosenHandles(plan))
	}
	if !strings.Contains(plan.Excluded, "Set aside: Add Albums to your Music Production Home") {
		t.Fatalf("excluded = %q", plan.Excluded)
	}
	names := lineNames(plan)
	// The agent's order: REAPER before the group; the assurance line last.
	if names[0] != "Installs REAPER tools" || names[1] != "Connects REAPER tools to Albums" || names[2] != "Turns Albums into a group" ||
		names[3] != "Creates Album-1, linked to its own folder" || names[len(names)-1] != "Nothing in Albums is moved, copied or read. Ori adds 1 plugin." {
		t.Fatalf("lines = %v", names)
	}
	if plan.Words != rec.Sections.Plan || plan.DefaultReason != "" {
		t.Fatalf("words=%q reason=%q", plan.Words, plan.DefaultReason)
	}
	// Every action carries its step; the assurance note carries none.
	var stamps []string
	for _, l := range plan.Lines {
		stamps = append(stamps, l.Step)
	}
	if stamps[0] != "reaper-tools" || stamps[1] != "reaper-tools" || stamps[2] != "group" || stamps[3] != "group" || stamps[len(stamps)-1] != "" {
		t.Fatalf("step stamps = %v", stamps)
	}
	if len(plan.Candidates) != 1 || plan.Candidates[0].ID != "integration:ori_reaper" {
		t.Fatalf("candidates = %+v", plan.Candidates)
	}
}

func TestBuildPlan_OrisDefaultWhenTheAgentNamesNothingAndSaysWhy(t *testing.T) {
	rec := planRecord()
	rec.Sections = &folderlook.Sections{Plan: "Nothing needed."}
	plan := BuildPlan(rec, nil)
	if plan.Source != PlanByOri || plan.Stage != foldermap.StageHome || plan.Words != "Nothing needed." ||
		plan.DefaultReason != "The agent named no step Ori can run, so this is Ori's default plan." {
		t.Fatalf("plan = %+v", plan)
	}
	// The tools come before the Home's listing (see TheToolsRunBeforeTheHome…).
	if names := lineNames(plan); names[0] != "Installs REAPER tools" || names[2] != "Adds Albums to your Music Production Home" {
		t.Fatalf("default lines = %v", names)
	}
	if strings.Join(plan.Selected, ",") != "music-production-home,reaper-tools" {
		t.Fatalf("selected = %v", plan.Selected)
	}
	rec.Sections = nil
	if noModel := BuildPlan(rec, nil); noModel.DefaultReason != "" || noModel.Source != PlanByOri {
		t.Fatalf("no model = %+v", noModel)
	}
	rec.Sections = &folderlook.Sections{Dropped: []string{folderlook.SectionPlan}}
	if dropped := BuildPlan(rec, nil); !strings.HasPrefix(dropped.DefaultReason, "The agent's plan could not be shown") {
		t.Fatalf("dropped = %q", dropped.DefaultReason)
	}
}

// The Home step lists the songs and records which of them can be set up; that
// depends on the project tools being installed. In one run the tools step must
// therefore run before the Home step, whatever order the agent or the catalog
// gave, or the listing records "no project tools" and the "to set up" count
// stays at zero after the run.
func TestBuildPlan_TheToolsRunBeforeTheHomeListsTheSongs(t *testing.T) {
	cases := map[string]struct {
		sections *folderlook.Sections
		selected []string
		homeHere bool
	}{
		"Ori's default, joining a Home": {homeHere: true},
		"the agent's Home first":        {sections: &folderlook.Sections{Plan: "Home, then REAPER.", PlanSteps: []string{"music-production-home", "reaper-tools"}}},
		"the owner's ticks":             {selected: []string{"music-production-home", "reaper-tools"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := planRecord()
			if !tc.homeHere {
				rec.Candidates[0].Action = folderlook.ActionInstall
			}
			rec.Candidates[1].Action = folderlook.ActionInstall
			rec.Sections = tc.sections
			plan := BuildPlan(rec, tc.selected)
			if plan.Stage != foldermap.StageHome || len(plan.Candidates) != 2 {
				t.Fatalf("stage=%q candidates=%+v", plan.Stage, plan.Candidates)
			}
			if plan.Candidates[0].Kind != folderlook.KindIntegration || plan.Candidates[1].Kind != folderlook.KindHomeProvider {
				t.Fatalf("run order = %s, %s; the tools must come first", plan.Candidates[0].ID, plan.Candidates[1].ID)
			}
			names := lineNames(plan)
			if names[0] != "Installs REAPER tools" || names[1] != "Connects REAPER tools to Albums" {
				t.Fatalf("lines = %v", names)
			}
		})
	}
}

// The assurance says what Ori adds as well as what it leaves alone (PRD FR25).
func TestBuildPlan_TheAssuranceNamesWhatOriAdds(t *testing.T) {
	const kept = "Nothing in Albums is moved, copied or read"
	cases := map[string]struct {
		integration string
		home        *folderlook.HomeFacts
		homeAction  string
		selected    []string
		want        string
	}{
		"nothing added": {integration: folderlook.ActionAlreadyHere, homeAction: folderlook.ActionAlreadyHere,
			home: &folderlook.HomeFacts{ProviderReady: true, HomeExists: true, RolesKnown: true}, want: kept},
		"one plugin, the app on this Mac": {integration: folderlook.ActionConnect, homeAction: folderlook.ActionAlreadyHere,
			home: &folderlook.HomeFacts{ProviderReady: true, HomeExists: true, RolesKnown: true}, want: kept + ". Ori adds 1 plugin."},
		"two plugins, a new Home and its agents": {integration: folderlook.ActionInstall, homeAction: folderlook.ActionInstall,
			home: &folderlook.HomeFacts{ProviderName: "Music Project Management"}, selected: []string{"music-production-home", "reaper-tools"},
			want: kept + ". Ori adds 2 plugins, Music Production Home, and its agents."},
		"a new Home only": {integration: folderlook.ActionAlreadyHere, homeAction: folderlook.ActionInstall,
			home: &folderlook.HomeFacts{ProviderReady: true, RolesKnown: true}, selected: []string{"music-production-home"},
			want: kept + ". Ori adds Music Production Home."},
		"the Home left unticked": {integration: folderlook.ActionInstall, homeAction: folderlook.ActionInstall,
			home: &folderlook.HomeFacts{}, selected: []string{"reaper-tools"}, want: kept + ". Ori adds 1 plugin."},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := planRecord()
			rec.Candidates[0].Action, rec.Candidates[0].Home = tc.homeAction, tc.home
			rec.Candidates[1].Action = tc.integration
			plan := BuildPlan(rec, tc.selected)
			if got := plan.Lines[len(plan.Lines)-1].Name; got != tc.want {
				t.Fatalf("assurance = %q, want %q", got, tc.want)
			}
		})
	}
}

// When Ori moves the tools ahead of where the agent named them, the card says
// why under the agent's words (PRD FR26); never for Ori's own plan.
func TestBuildPlan_TheReorderNoteFollowsTheAgentsOrder(t *testing.T) {
	rec := planRecord()
	rec.Sections = &folderlook.Sections{Plan: "Home, then REAPER.", PlanSteps: []string{"music-production-home", "reaper-tools"}}
	if plan := BuildPlan(rec, nil); plan.ReorderNote != ReorderNote || planViewOf(plan).ReorderNote != ReorderNote {
		t.Fatalf("home first: note = %q", plan.ReorderNote)
	}
	rec.Sections = &folderlook.Sections{Plan: "REAPER, then the Home.", PlanSteps: []string{"reaper-tools", "music-production-home"}}
	if plan := BuildPlan(rec, nil); plan.ReorderNote != "" {
		t.Fatalf("tools first: note = %q", plan.ReorderNote)
	}
	rec.Sections = nil
	if plan := BuildPlan(rec, nil); plan.Source != PlanByOri || plan.ReorderNote != "" {
		t.Fatalf("Ori's plan: source=%q note = %q", plan.Source, plan.ReorderNote)
	}
}

// The install block lists only the plugins the ticked steps download and
// install, in run order, with their reviewed release (PRD FR13–FR17).
func TestBuildPlan_InstallsListThePluginsTheTickedStepsInstall(t *testing.T) {
	reviewed := &folderlook.ReleaseFacts{Version: "0.9.1", Publisher: "Ori", SourceLabel: "owner/tools", SourceURL: "https://example.test/owner/tools"}
	withPlugin := func(rec Record) Record {
		rec.Candidates = append(rec.Candidates, folderlook.Candidate{ID: "plugin:mix", Kind: folderlook.KindPlugin, Name: "Mix Kit",
			Source: folderlook.SourceSearch, Marketplace: "Community", Action: folderlook.ActionAdd, Ranked: true, Why: "Mixes faster."})
		return rec
	}
	cases := map[string]struct {
		integration string
		home        *folderlook.HomeFacts
		homeAction  string
		addOn       bool
		selected    []string
		want        []string // step|line|name|version|app sentence
	}{
		"install: the app is not on this Mac": {integration: folderlook.ActionInstall, homeAction: folderlook.ActionAlreadyHere,
			home: &folderlook.HomeFacts{ProviderReady: true, HomeExists: true},
			want: []string{"reaper-tools|integration|REAPER tools|0.9.1|REAPER is not on this Mac yet; the tools install anyway and work once it is."}},
		"connect: the app is here, its tools are not": {integration: folderlook.ActionConnect, homeAction: folderlook.ActionAlreadyHere,
			home: &folderlook.HomeFacts{ProviderReady: true, HomeExists: true},
			want: []string{"reaper-tools|integration|REAPER tools|0.9.1|REAPER is on this Mac; its Ori tools are not yet."}},
		"already here: nothing listed": {integration: folderlook.ActionAlreadyHere, homeAction: folderlook.ActionAlreadyHere,
			home: &folderlook.HomeFacts{ProviderReady: true, HomeExists: true}},
		"the Home provider not ready: listed after the tools": {integration: folderlook.ActionInstall, homeAction: folderlook.ActionInstall,
			home:     &folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.5.0"},
			selected: []string{"music-production-home", "reaper-tools"},
			want: []string{"reaper-tools|integration|REAPER tools|0.9.1|REAPER is not on this Mac yet; the tools install anyway and work once it is.",
				"music-production-home|provider|Music Project Management|0.5.0|"}},
		"the Home step unticked: only the tools": {integration: folderlook.ActionInstall, homeAction: folderlook.ActionInstall,
			home: &folderlook.HomeFacts{ProviderName: "Music Project Management"}, selected: []string{"reaper-tools"},
			want: []string{"reaper-tools|integration|REAPER tools|0.9.1|REAPER is not on this Mac yet; the tools install anyway and work once it is."}},
		"a search add-on": {integration: folderlook.ActionAlreadyHere, homeAction: folderlook.ActionAlreadyHere,
			home: &folderlook.HomeFacts{ProviderReady: true, HomeExists: true}, addOn: true,
			selected: []string{"music-production-home", "mix-kit"}, want: []string{"mix-kit|add_on|Mix Kit||"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := planRecord()
			rec.Candidates[0].Action, rec.Candidates[0].Home = tc.homeAction, tc.home
			rec.Candidates[1].Action, rec.Candidates[1].Release = tc.integration, reviewed
			rec.Candidates[1].Why = "Lets the agent work with the REAPER songs here."
			if tc.addOn {
				rec = withPlugin(rec)
			}
			plan := BuildPlan(rec, tc.selected)
			var got []string
			for _, e := range plan.Installs {
				got = append(got, strings.Join([]string{e.Step, e.Line, e.Name, e.Version, e.AppSentence}, "|"))
			}
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("installs =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
			for _, e := range plan.Installs {
				switch e.Line {
				case "integration":
					if e.Publisher != "Ori" || e.SourceLabel != "owner/tools" || e.SourceURL != reviewed.SourceURL || e.Why == "" {
						t.Fatalf("integration entry = %+v", e)
					}
				case "provider":
					if e.Why != "Provides your Music Production Home." {
						t.Fatalf("provider entry = %+v", e)
					}
				case "add_on":
					if !e.Search || e.Marketplace != "Community" || e.Why != "Mixes faster." {
						t.Fatalf("add-on entry = %+v", e)
					}
				}
				// Each entry names a step whose plan line of that kind the run marks.
				found := false
				for _, l := range plan.Lines {
					found = found || (l.Step == e.Step && l.Kind == e.Line)
				}
				if !found {
					t.Fatalf("no %s line under step %s: %+v", e.Line, e.Step, plan.Lines)
				}
			}
			if view := planViewOf(plan); view.Installs == nil || len(view.Installs) != len(tc.want) {
				t.Fatalf("view installs = %+v", view.Installs)
			}
		})
	}
}

// The digest covers the install block: the same steps installing a different
// release are a different plan (PRD FR21).
func TestBuildPlan_TheDigestCoversTheInstalls(t *testing.T) {
	rec := planRecord()
	rec.Candidates[1].Release = &folderlook.ReleaseFacts{Version: "0.9.0", Publisher: "Ori"}
	before := BuildPlan(rec, nil)
	rec.Candidates[1].Release = &folderlook.ReleaseFacts{Version: "0.9.1", Publisher: "Ori"}
	after := BuildPlan(rec, nil)
	if strings.Join(before.Selected, ",") != strings.Join(after.Selected, ",") || before.Digest == after.Digest {
		t.Fatalf("a changed install must change the digest: %s / %s", before.Digest, after.Digest)
	}
}

func TestBuildPlan_TheOwnersTicksReplaceTheSteps(t *testing.T) {
	rec := planRecord()
	rec.Sections = &folderlook.Sections{Plan: "REAPER, then a group.", PlanSteps: []string{"reaper-tools", "group"}}
	home := BuildPlan(rec, []string{"music-production-home"})
	if home.Source != PlanByAgent || home.Stage != foldermap.StageHome || home.Excluded != "" ||
		strings.Join(home.Selected, ",") != "music-production-home" || lineNames(home)[0] != "Adds Albums to your Music Production Home" {
		t.Fatalf("home only = %+v", home)
	}
	// A candidate id still names its step; the digest follows the choice.
	byID := BuildPlan(rec, []string{"integration:ori_reaper"})
	if byID.Stage != foldermap.StageTools || strings.Join(byID.Selected, ",") != "reaper-tools" || byID.Digest == home.Digest {
		t.Fatalf("by id = %+v", byID)
	}
	none := BuildPlan(rec, []string{})
	if none.Runnable || none.Unavailable != "Nothing is selected to set up." || len(none.Lines) != 1 {
		t.Fatalf("nothing ticked = %+v", none)
	}
	// Ticking both of a pair keeps the agent's first.
	both := BuildPlan(rec, []string{"music-production-home", "group"})
	if both.Stage != foldermap.StageGroup || strings.Join(both.Selected, ",") != "group" || both.Excluded == "" {
		t.Fatalf("both = %+v", both)
	}
}
