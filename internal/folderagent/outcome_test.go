package folderagent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// setup-card-clarity FR1, FR2, FR5: the host marks every plan line an outcome
// or a detail, so the card never reads text to decide.

// outcomesOf are a step's outcome items in the order the card draws them: the
// distinct non-empty Outcome of its lines, first appearance first.
func outcomesOf(plan Plan, step string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range plan.Lines {
		if l.Step != step || l.Outcome == "" || seen[l.Outcome] {
			continue
		}
		seen[l.Outcome] = true
		out = append(out, l.Outcome)
	}
	return out
}

// stepHandleOfKind is the handle of the catalog's step of one kind.
func stepHandleOfKind(t *testing.T, rec Record, kind string) string {
	t.Helper()
	for _, s := range folderlook.Steps(rec.FolderName, rec.Facts, rec.Candidates) {
		if s.Kind == kind {
			return s.Handle
		}
	}
	t.Fatalf("no %s step offered", kind)
	return ""
}

func TestOutcomes_TheHomeStepSaysWhatItCreatesHiresAndLists(t *testing.T) {
	provider := "Music Project Management"
	cases := []struct {
		name  string
		facts *folderlook.HomeFacts
		want  []string
	}{
		{"no provider, no Home", &folderlook.HomeFacts{ProviderName: provider, ProviderVersion: "0.5.0"},
			[]string{"Creates Music Production Home", "Hires the Home's agents", "Lists 6 songs"}},
		{"provider present, no Home", &folderlook.HomeFacts{ProviderName: provider, ProviderReady: true},
			[]string{"Creates Music Production Home", "Hires the Home's agents", "Lists 6 songs"}},
		{"Home exists, every role filled: nothing created, nothing hired",
			&folderlook.HomeFacts{ProviderName: provider, ProviderReady: true, HomeExists: true, RolesKnown: true},
			[]string{"Lists 6 songs"}},
		{"Home exists, two roles missing: one item for both hires",
			&folderlook.HomeFacts{ProviderName: provider, ProviderReady: true, HomeExists: true, RolesKnown: true,
				MissingRoles: []string{"Portfolio Manager", "Library Manager"}},
			[]string{"Hires 2 agents", "Lists 6 songs"}},
		{"Home exists, one role missing",
			&folderlook.HomeFacts{ProviderName: provider, ProviderReady: true, HomeExists: true, RolesKnown: true,
				MissingRoles: []string{"Portfolio Manager"}},
			[]string{"Hires 1 agent", "Lists 6 songs"}},
		{"Home exists, provider needs an update: the install is the block's, not the outcome's",
			&folderlook.HomeFacts{ProviderName: provider, ProviderVersion: "0.6.0", HomeExists: true, RolesKnown: true},
			[]string{"Lists 6 songs"}},
		{"a record without host facts keeps the generic creation", nil,
			[]string{"Creates Music Production Home", "Lists 6 songs"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := homeStageRecord(tc.facts)
			plan := homePlan(t, rec)
			step := stepHandleOfKind(t, rec, folderlook.StepHome)
			if got := outcomesOf(plan, step); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("outcomes = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOutcomes_AddsLooksIntoAndPlacesAreDetails(t *testing.T) {
	// planRecord's songs sit two folders down, so the Home step also looks
	// into a folder for them.
	rec := planRecord()
	plan := BuildPlan(rec, []string{"music-production-home"})
	details := map[string]bool{}
	for _, l := range plan.Lines {
		if l.Kind == personalassistant.FolderPlanFolder {
			continue
		}
		if l.Outcome == "" {
			details[l.Kind] = true
		}
	}
	for _, kind := range []string{personalassistant.FolderPlanLibrary, FolderPlanScopes, FolderPlanPlace} {
		if !details[kind] {
			t.Fatalf("the %s line is not a detail: %+v", kind, plan.Lines)
		}
	}
	if len(details) != 3 {
		t.Fatalf("details = %v, want only library, scopes and place", details)
	}
	if got := outcomesOf(plan, "music-production-home"); !reflect.DeepEqual(got, []string{"Lists 6 songs"}) {
		t.Fatalf("outcomes = %q", got)
	}
}

func TestOutcomes_AToolsStepSaysWhatItConnectsAndLeavesTheInstallToItsBlock(t *testing.T) {
	rec := planRecord()
	rec.Candidates[1].Action = folderlook.ActionInstall
	rec.Candidates[1].Release = &folderlook.ReleaseFacts{Version: "0.9.1", Publisher: "Ori"}
	plan := BuildPlan(rec, []string{"reaper-tools"})
	if got := outcomesOf(plan, "reaper-tools"); !reflect.DeepEqual(got, []string{"Connects REAPER tools to Albums"}) {
		t.Fatalf("outcomes = %q", got)
	}
	var install *personalassistant.FolderPlanLine
	for i, l := range plan.Lines {
		if l.Kind == personalassistant.FolderPlanIntegration {
			install = &plan.Lines[i]
		}
	}
	if install == nil || install.Outcome != "" {
		t.Fatalf("the install line must be a detail the install block covers: %+v", install)
	}
}

func TestOutcomes_TheGroupStepSaysItTurnsTheFolderIntoAGroupAndHowManyItCreates(t *testing.T) {
	rec := homeStageRecord(&folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderReady: true})
	group := stepHandleOfKind(t, rec, folderlook.StepGroup)
	plan := BuildPlan(rec, []string{group})
	if got := outcomesOf(plan, group); !reflect.DeepEqual(got, []string{"Turns Albums into a group", "Creates 6 workspaces"}) {
		t.Fatalf("outcomes = %q", got)
	}
	// A member that is already a workspace is included, not created: its line is
	// a detail and the count drops by one.
	rec.ExistingMembers = map[string]WorkspaceRef{"Album-1": {ID: "w1", Name: "Album One"}}
	plan = BuildPlan(rec, []string{group})
	if got := outcomesOf(plan, group); !reflect.DeepEqual(got, []string{"Turns Albums into a group", "Creates 5 workspaces"}) {
		t.Fatalf("outcomes with an existing member = %q", got)
	}
	for _, l := range plan.Lines {
		if l.Kind == FolderPlanExisting && l.Outcome != "" {
			t.Fatalf("an existing member is a detail: %+v", l)
		}
	}
}

func TestOutcomes_FileCuratorAndAddOnsSayWhatTheyAdd(t *testing.T) {
	rec := Record{FolderName: "Loose Files", WorkspaceName: "Loose Files", AgentName: "Loose Files Manager", Candidates: []folderlook.Candidate{
		{ID: "curator:file", Kind: folderlook.KindFileCurator, Name: "File Curator", Action: folderlook.ActionAdd, Required: true},
		{ID: "skill:mix", Kind: folderlook.KindSkill, Name: "Mix Notes", Action: folderlook.ActionAdd},
		{ID: "plugin:kit", Kind: folderlook.KindPlugin, Name: "Mix Kit", Action: folderlook.ActionAdd},
	}}
	curator := stepHandleOfKind(t, rec, folderlook.StepFileCurator)
	plan := BuildPlan(rec, nil)
	if got := outcomesOf(plan, curator); !reflect.DeepEqual(got, []string{"Adds File Janitor to Loose Files", "Loose Files Manager explains proposed filing"}) {
		t.Fatalf("file curator outcomes = %q", got)
	}
	var skill, kit string
	for _, s := range folderlook.Steps(rec.FolderName, rec.Facts, rec.Candidates) {
		switch s.Name {
		case "Mix Notes":
			skill = s.Handle
		case "Mix Kit":
			kit = s.Handle
		}
	}
	plan = BuildPlan(rec, []string{skill, kit})
	if got := outcomesOf(plan, skill); !reflect.DeepEqual(got, []string{"Adds skill Mix Notes"}) {
		t.Fatalf("skill outcomes = %q", got)
	}
	// A plugin add-on installs, so the install block carries it.
	if got := outcomesOf(plan, kit); len(got) != 0 {
		t.Fatalf("an installing add-on repeats itself in the outcome: %q", got)
	}
}

// consequential kinds may be a detail only when they change nothing: they link
// a folder, list what the walk found or say what was left for later. Any
// other kind, including one added later, must carry an outcome unless the
// install block lists it.
var detailOnlyKinds = map[string]bool{
	personalassistant.FolderPlanLibrary: true,
	FolderPlanScopes:                    true,
	FolderPlanPlace:                     true,
	FolderPlanExisting:                  true,
	FolderPlanSoftware:                  true,
	FolderPlanMore:                      true,
	personalassistant.FolderPlanFolder:  true,
}

// hiddenConsequences are the plan's lines that create, install or hire yet
// would not appear in a step's outcome line, and no install row covers
// (FR5): the owner who never opens the details has still confirmed them.
func hiddenConsequences(plan Plan) []string {
	covered := func(l personalassistant.FolderPlanLine) bool {
		for _, e := range plan.Installs {
			if e.Step == l.Step && e.Line == l.Kind {
				return true
			}
		}
		return false
	}
	var out []string
	for _, l := range plan.Lines {
		if l.Outcome != "" || detailOnlyKinds[l.Kind] || covered(l) {
			continue
		}
		out = append(out, l.Kind+": "+l.Name)
	}
	return out
}

func TestOutcomes_NoLineThatCreatesInstallsOrHiresHidesBehindTheDetails(t *testing.T) {
	reviewed := &folderlook.ReleaseFacts{Version: "0.9.1", Publisher: "Ori"}
	tools := planRecord()
	tools.Candidates[1].Action, tools.Candidates[1].Release = folderlook.ActionInstall, reviewed
	home := func(facts *folderlook.HomeFacts) Record { return homeStageRecord(facts) }
	curator := Record{FolderName: "Loose Files", WorkspaceName: "Loose Files", AgentName: "Loose Files Manager",
		Candidates: []folderlook.Candidate{
			{ID: "curator:file", Kind: folderlook.KindFileCurator, Name: "File Curator", Action: folderlook.ActionAdd, Required: true},
			{ID: "plugin:kit", Kind: folderlook.KindPlugin, Name: "Mix Kit", Action: folderlook.ActionAdd, Ranked: true},
			{ID: "mcp:notes", Kind: folderlook.KindMCP, Name: "Notes Server", Action: folderlook.ActionAdd, Ranked: true},
		}}
	fixtures := map[string]Plan{
		"tools installing":          BuildPlan(tools, []string{"reaper-tools"}),
		"tools and the Home":        BuildPlan(tools, []string{"reaper-tools", "music-production-home"}),
		"new Home, provider absent": homePlan(t, home(&folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.5.0"})),
		"new Home, provider here":   homePlan(t, home(&folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderReady: true})),
		"Home, two roles missing": homePlan(t, home(&folderlook.HomeFacts{ProviderReady: true, HomeExists: true, RolesKnown: true,
			MissingRoles: []string{"Portfolio Manager", "Library Manager"}})),
		"Home provider update":   homePlan(t, home(&folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.6.0", HomeExists: true, RolesKnown: true})),
		"legacy Home, no facts":  homePlan(t, home(nil)),
		"group":                  BuildPlan(home(nil), []string{"group"}),
		"file curator and addon": BuildPlan(curator, nil),
	}
	for name, plan := range fixtures {
		t.Run(name, func(t *testing.T) {
			if len(plan.Lines) < 2 {
				t.Fatalf("fixture builds no plan: %+v", plan.Lines)
			}
			if hidden := hiddenConsequences(plan); len(hidden) > 0 {
				t.Fatalf("lines that create, install or hire are only details: %q", hidden)
			}
			for _, step := range plan.Selected {
				if n := len(outcomesOf(plan, step)); n > 3 {
					t.Fatalf("step %s has %d outcome items, at most three: %q", step, n, outcomesOf(plan, step))
				}
			}
		})
	}

	// The guard can fail: a hire line marked a detail is found.
	plan := homePlan(t, home(&folderlook.HomeFacts{ProviderReady: true, HomeExists: true, RolesKnown: true,
		MissingRoles: []string{"Portfolio Manager"}}))
	for i := range plan.Lines {
		if plan.Lines[i].Kind == FolderPlanHire {
			plan.Lines[i].Outcome = ""
		}
	}
	if hidden := hiddenConsequences(plan); len(hidden) != 1 || !strings.HasPrefix(hidden[0], "hire: ") {
		t.Fatalf("the guard missed a hidden hire: %q", hidden)
	}
}

// The mark decides where a line is drawn, never what it does: it stays out of
// the digest, which still covers every line (FR5).
func TestOutcomes_TheMarkIsNotInTheDigestButEveryLineIs(t *testing.T) {
	facts := &folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.5.0"}
	plan := homePlan(t, homeStageRecord(facts))
	marked := 0
	for _, l := range plan.Lines {
		if l.Outcome != "" {
			marked++
		}
	}
	if marked == 0 {
		t.Fatal("no line is marked")
	}
	// The same plan with every mark taken off has the same digest.
	bare := plan
	bare.Lines = append([]personalassistant.FolderPlanLine(nil), plan.Lines...)
	for i := range bare.Lines {
		bare.Lines[i].Outcome = ""
	}
	if planDigest(bare) != plan.Digest {
		t.Fatalf("the marks changed the digest: %s != %s", planDigest(bare), plan.Digest)
	}
	// A change to a line that is only ever a detail still changes it.
	for i, l := range plan.Lines {
		if l.Outcome != "" || l.Kind == personalassistant.FolderPlanFolder {
			continue
		}
		changed := plan
		changed.Lines = append([]personalassistant.FolderPlanLine(nil), plan.Lines...)
		changed.Lines[i].Name += " elsewhere"
		if planDigest(changed) == plan.Digest {
			t.Fatalf("a changed %s detail kept the digest", l.Kind)
		}
		changed.Lines[i] = l
		changed.Lines[i].Detail += " and more"
		if planDigest(changed) == plan.Digest {
			t.Fatalf("a changed %s detail text kept the digest", l.Kind)
		}
	}
	// And a plan rebuilt from the same facts keeps it.
	if again := homePlan(t, homeStageRecord(facts)); again.Digest != plan.Digest {
		t.Fatalf("rebuilt digest %s != %s", again.Digest, plan.Digest)
	}
}

func TestOutcomes_TheViewCarriesTheMarkAndDetailsOmitIt(t *testing.T) {
	plan := homePlan(t, homeStageRecord(&folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.5.0"}))
	raw, err := json.Marshal(planViewOf(plan))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Lines []map[string]any `json:"lines"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, l := range decoded.Lines {
		_, has := l["outcome"]
		switch l["kind"] {
		case "home", "hire", "songs":
			if !has {
				t.Fatalf("%v carries no outcome in the view", l["kind"])
			}
		case "provider", "library", "place", "folder":
			if has {
				t.Fatalf("%v is a detail and must omit outcome: %v", l["kind"], l)
			}
		}
	}
}

func TestOutcomes_AnOutcomeIsPlainTextLikeTheRest(t *testing.T) {
	line := personalassistant.FolderPlanLine{Kind: "home", Name: "Creates a Home", Outcome: "Creates a Home"}
	if err := personalassistant.ValidateFolderPlanLine(line); err != nil {
		t.Fatalf("a plain outcome is refused: %v", err)
	}
	for _, bad := range []string{"Creates /Users/someone/Music", "two\nlines", strings.Repeat("x", 600)} {
		line.Outcome = bad
		if err := personalassistant.ValidateFolderPlanLine(line); err == nil {
			t.Fatalf("outcome %q was accepted", bad)
		}
	}
}
