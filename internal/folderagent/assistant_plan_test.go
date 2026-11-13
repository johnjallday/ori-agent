package folderagent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// assistant-reads-projects FR1 to FR6, FR12: the plan card names the Home's
// shared assistant as one more hire, and the Home step installs the app plugin
// its songs' team comes from when the plan's own tools step does not.

func assistantOf(state string) *folderlook.HomeAssistant {
	return &folderlook.HomeAssistant{Label: "REAPER Assistant", Name: "REAPER Assistant",
		Job: "Handles everything REAPER-related for this project.", State: state}
}

// assistantRecord is planRecord with a Home that does not exist yet, both
// plugins to install, the Home's facts as the look recorded them, and the
// Home's songs' team coming from the tools candidate.
func assistantRecord(a *folderlook.HomeAssistant) Record {
	rec := planRecord()
	rec.HomeHireModel = testModel
	rec.Candidates[0].Action = folderlook.ActionInstall
	rec.Candidates[0].ProjectIntegrationKey = "ori_reaper"
	rec.Candidates[0].Home = newHomeFacts(folderlook.HomeHire{Name: "Portfolio Manager", Job: "Plans the work."})
	rec.Candidates[0].Home.Assistant = a
	rec.Candidates[1].Action = folderlook.ActionInstall
	return rec
}

func linesOfKind(plan Plan, kinds ...string) []string {
	var out []string
	for _, l := range plan.Lines {
		for _, kind := range kinds {
			if l.Kind == kind {
				out = append(out, l.Name)
			}
		}
	}
	return out
}

func assuranceOf(plan Plan) string { return plan.Lines[len(plan.Lines)-1].Name }

func TestPlan_HiresTheSharedAssistantAsOneMoreRow(t *testing.T) {
	rec := assistantRecord(assistantOf(folderlook.AssistantHire))
	plan := BuildPlan(rec, []string{"music-production-home", "reaper-tools"})

	want := []HireEntry{
		{Step: "music-production-home", Name: "Portfolio Manager", Job: "Plans the work.", Model: testModel},
		{Step: "music-production-home", Name: "REAPER Assistant", Job: "Handles everything REAPER-related for this project.",
			Model: testModel, Note: AssistantJoins},
	}
	if !reflect.DeepEqual(plan.Hires, want) || plan.HiresNote != "" {
		t.Fatalf("hires = %+v note %q", plan.Hires, plan.HiresNote)
	}
	// Its own line, after the Home's roles, never a Home-role hire line.
	if got := linesOfKind(plan, FolderPlanHire, FolderPlanAssistant); !reflect.DeepEqual(got,
		[]string{"Hires Portfolio Manager", "Hires REAPER Assistant"}) {
		t.Fatalf("hire lines = %q", got)
	}
	for _, l := range plan.Lines {
		if l.Kind == FolderPlanAssistant && (l.Outcome != "Hires 2 agents" || l.Detail != AssistantJoins) {
			t.Fatalf("assistant line = %+v", l)
		}
		if l.Kind == FolderPlanHire && l.Outcome != "Hires 2 agents" {
			t.Fatalf("the Home role's outcome does not count the assistant: %+v", l)
		}
	}
	// FR2: the count follows.
	if got, want := assuranceOf(plan), "Nothing in Albums is moved, copied or read. Ori adds 2 plugins, Music Production Home, and 2 agents."; got != want {
		t.Fatalf("assurance = %q, want %q", got, want)
	}
	// The tools step installs the plugin first: the Home step does not again.
	if got := linesOfKind(plan, FolderPlanHomeTools); len(got) != 0 || len(plan.Installs) != 2 {
		t.Fatalf("the Home step installed the tools a second time: %q, installs %+v", got, plan.Installs)
	}
}

// A name one of the owner's agents holds: the hire takes the next one, and the
// row says what happens to theirs.
func TestPlan_SharedAssistantNameTaken(t *testing.T) {
	a := assistantOf(folderlook.AssistantHire)
	a.Name, a.Taken = "REAPER Assistant 2", "REAPER Assistant"
	plan := BuildPlan(assistantRecord(a), []string{"music-production-home", "reaper-tools"})
	last := plan.Hires[len(plan.Hires)-1]
	if last.Name != "REAPER Assistant 2" || last.Note != AssistantJoins+" Your existing REAPER Assistant stays as it is." {
		t.Fatalf("row = %+v", last)
	}
}

// FR3, FR4: a Home that has it hires nothing for it; a Home whose approval is
// off, for an older team, or held by somebody else's agent gets one line and
// no hire. The card changes in nothing else.
func TestPlan_SharedAssistantStatesThatHireNobody(t *testing.T) {
	without := BuildPlan(assistantRecord(nil), []string{"music-production-home", "reaper-tools"})
	for _, tc := range []struct {
		state, line, detail string
	}{
		{folderlook.AssistantHas, "", ""},
		{folderlook.AssistantOff, "Your REAPER Assistant is not added", "\"Add my REAPER Assistant to songs I open\" switch is off"},
		{folderlook.AssistantStale, "Your REAPER Assistant is not added", "older version of its team"},
		{folderlook.AssistantHeld, "Your REAPER Assistant is not added", "Decide in the Home"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			plan := BuildPlan(assistantRecord(assistantOf(tc.state)), []string{"music-production-home", "reaper-tools"})
			if len(plan.Hires) != 1 || plan.Hires[0].Name != "Portfolio Manager" || len(linesOfKind(plan, FolderPlanAssistant)) != 0 {
				t.Fatalf("hires = %+v, lines %q", plan.Hires, lineNames(plan))
			}
			if assuranceOf(plan) != assuranceOf(without) {
				t.Fatalf("assurance = %q, want %q", assuranceOf(plan), assuranceOf(without))
			}
			notes := []personalassistant.FolderPlanLine{}
			for _, l := range plan.Lines {
				if l.Kind == FolderPlanAssistantNote {
					notes = append(notes, l)
				}
			}
			if tc.line == "" {
				if len(notes) != 0 || !reflect.DeepEqual(lineNames(plan), lineNames(without)) {
					t.Fatalf("a Home that has it changed the card: %q", lineNames(plan))
				}
				return
			}
			if len(notes) != 1 || notes[0].Name != tc.line || !strings.Contains(notes[0].Detail, tc.detail) {
				t.Fatalf("note lines = %+v", notes)
			}
		})
	}
}

// FR12: a plan that ticks only the Home step installs the app plugin under it,
// after the Home's provider, and counts it. With the tools ticked, or already
// installed, it does not.
func TestPlan_HomeStepInstallsTheSongsAppPlugin(t *testing.T) {
	rec := assistantRecord(assistantOf(folderlook.AssistantHire))
	plan := BuildPlan(rec, []string{"music-production-home"})
	kinds := []string{}
	for _, l := range plan.Lines {
		if l.Kind == personalassistant.FolderPlanProvider || l.Kind == FolderPlanHomeTools || l.Kind == personalassistant.FolderPlanHome {
			kinds = append(kinds, l.Kind+": "+l.Name)
		}
	}
	if want := []string{"provider: Installs Music Project Management", "home_tools: Installs REAPER tools",
		"home: Creates your Music Production Home"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("lines = %q, want %q", kinds, want)
	}
	if len(plan.Installs) != 2 || plan.Installs[1].Name != "REAPER tools" || plan.Installs[1].Line != FolderPlanHomeTools ||
		plan.Installs[1].Step != "music-production-home" {
		t.Fatalf("installs = %+v", plan.Installs)
	}
	if got, want := assuranceOf(plan), "Nothing in Albums is moved, copied or read. Ori adds 2 plugins, Music Production Home, and 2 agents."; got != want {
		t.Fatalf("assurance = %q, want %q", got, want)
	}
	// The run acts only on the Home: the tools are not a second candidate.
	for _, c := range plan.Candidates {
		if c.Kind == folderlook.KindIntegration {
			t.Fatalf("the Home step's install made the tools a run candidate: %+v", plan.Candidates)
		}
	}

	rec.Candidates[1].Action = folderlook.ActionAlreadyHere
	plan = BuildPlan(rec, []string{"music-production-home"})
	if got := linesOfKind(plan, FolderPlanHomeTools); len(got) != 0 || len(plan.Installs) != 1 {
		t.Fatalf("installed tools were installed again: %q, installs %+v", got, plan.Installs)
	}
}

// What the card names is what the press approves: the assistant's row and its
// state are in the digest.
func TestPlan_SharedAssistantIsInTheDigest(t *testing.T) {
	digest := func(a *folderlook.HomeAssistant) string {
		return BuildPlan(assistantRecord(a), []string{"music-production-home"}).Digest
	}
	seen := map[string]string{}
	for name, a := range map[string]*folderlook.HomeAssistant{
		"none":      nil,
		"hire":      assistantOf(folderlook.AssistantHire),
		"off":       assistantOf(folderlook.AssistantOff),
		"other job": {Label: "REAPER Assistant", Name: "REAPER Assistant", Job: "Something else.", State: folderlook.AssistantHire},
		"other name": {Label: "REAPER Assistant", Name: "REAPER Assistant 2", Taken: "REAPER Assistant",
			Job: "Handles everything REAPER-related for this project.", State: folderlook.AssistantHire},
	} {
		got := digest(a)
		if other, dup := seen[got]; dup {
			t.Fatalf("%s and %s share a digest", name, other)
		}
		seen[got] = name
	}
}
