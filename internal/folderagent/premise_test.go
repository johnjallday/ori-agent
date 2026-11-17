package folderagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// fakeHomes says which workspace ids are Homes of which provider.
type fakeHomes map[string]string

func (f fakeHomes) HomeProviderOf(_ context.Context, _ string, workspaceID string) string {
	return f[workspaceID]
}

// startFrom feeds the folder as the page would, from a workspace page.
func (h *harness) startFrom(t *testing.T, from string) View {
	t.Helper()
	chosen, err := h.svc.ChooseFrom(context.Background(), "u1", personalassistant.FolderChoice{Picker: true}, from)
	if err != nil || chosen == nil {
		t.Fatalf("choose: %v %v", chosen, err)
	}
	view, err := h.svc.Start(context.Background(), "u1", chosen.Token, "", Hire{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	h.svc.Wait()
	return view
}

func (h *harness) view(t *testing.T, id string) View {
	t.Helper()
	v, err := h.svc.View(context.Background(), "u1", id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// A recognised portfolio opens with the premise, the host's sentence and two
// answers; the plan is prepared but Set it up waits on the answer (FR1–FR4,
// FR10). Keep stays allowed.
func TestPremise_AskedForAPortfolioAndSetUpWaitsOnIt(t *testing.T) {
	h := newHarness(t, demo(t))
	start := h.start(t, "")
	report := h.view(t, start.WorkspaceID)
	p := report.Premise
	if p == nil || p.State != PremiseAsked {
		t.Fatalf("premise = %+v", p)
	}
	if p.Sentence != "This looks like a music production folder: 7 REAPER songs and 1 Logic Pro song." ||
		p.Confirm != "Yes, show me the plan" || p.Other != "It's something else" {
		t.Fatalf("premise words = %+v", p)
	}
	// The question shows three of the eight songs by name and says how many
	// more there are, in the order the folder was walked.
	if len(p.Names) != 3 || p.NamesMore != 5 {
		t.Fatalf("names = %v and %d more, want 3 and 5 more", p.Names, p.NamesMore)
	}
	// It says what the plan under it holds: with no model the default plan is
	// the group (a new Home is only offered), so the Home is not promised.
	if p.NothingYet != "Nothing is installed or created until you approve the plan." {
		t.Fatalf("nothing yet = %q", p.NothingYet)
	}
	if p.Outcome != "I'll plan turning Ori Music Demo into a group: a workspace for each song." {
		t.Fatalf("outcome = %q", p.Outcome)
	}
	if strings.Contains(p.Outcome, "Home") {
		t.Fatalf("the outcome names a Home the plan does not choose: %q", p.Outcome)
	}
	if report.Plan == nil || !report.Plan.Runnable {
		t.Fatalf("the plan is prepared while asked: %+v", report.Plan)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", start.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); !errors.Is(err, ErrPremiseOwed) {
		t.Fatalf("set up before the answer = %v", err)
	}
	if len(h.setups.runs) != 0 {
		t.Fatal("a run started before the premise was answered")
	}
	if _, err := h.svc.AnswerPremise(context.Background(), "u1", start.WorkspaceID, "maybe"); !ErrPremiseAnswer(err) {
		t.Fatalf("a third answer = %v", err)
	}
	kept, err := h.svc.Keep(context.Background(), "u1", start.WorkspaceID)
	if err != nil || kept.State != StateKept || kept.Premise == nil || kept.Premise.State != PremiseAsked {
		t.Fatalf("keep while asked = %+v %v", kept.Premise, err)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", start.WorkspaceID, SetUpRequest{PlanDigest: kept.Plan.Digest}); !errors.Is(err, ErrPremiseOwed) {
		t.Fatalf("set up from kept before the answer = %v", err)
	}
}

// songs builds n audio projects named by the given names, as the map lists them.
func songs(names ...string) foldermap.Facts {
	facts := foldermap.Facts{Portfolio: &foldermap.Portfolio{Shape: folderdigest.ShapeAudio, Count: len(names)}}
	for _, name := range names {
		facts.Members = append(facts.Members, foldermap.Project{Name: name, Shape: folderdigest.ShapeAudio, Path: "Albums/" + name, Depth: 2})
	}
	return facts
}

// The names line shows the first three songs in walk order and counts the
// rest; a name is the folder that holds the project file (FR2).
func TestPremiseNames(t *testing.T) {
	long40 := strings.Repeat("a", 40)
	cases := []struct {
		name  string
		facts foldermap.Facts
		want  []string
		more  int
	}{
		{"more than three", songs("Album-1", "Album-2", "Album-3", "Album-4", "Album-5", "Album-6"), []string{"Album-1", "Album-2", "Album-3"}, 3},
		{"exactly three leaves out the count", songs("A", "B", "C"), []string{"A", "B", "C"}, 0},
		{"one song", songs("Only"), []string{"Only"}, 0},
		{"two songs", songs("One", "Two"), []string{"One", "Two"}, 0},
		{"a name of forty characters is kept", songs(long40, "B", "C"), []string{long40, "B", "C"}, 0},
		{"a longer name is cut with an ellipsis", songs(strings.Repeat("b", 41), "B"), []string{strings.Repeat("b", 39) + "…", "B"}, 0},
		{"a long name of wide characters is cut by characters, not bytes", songs(strings.Repeat("曲", 50)), []string{strings.Repeat("曲", 39) + "…"}, 0},
		{"html stays plain text, not escaped or removed", songs(`<b>Hi</b> & "you"`, "B"), []string{`<b>Hi</b> & "you"`, "B"}, 0},
		{"control and bidi characters are dropped", songs("Na\nme\t1", "Evil"+string(rune(0x202e))+"File", "C"), []string{"Name1", "EvilFile", "C"}, 0},
		{"a name with nothing left is skipped, not counted", songs("\n\t", "B", "C", "D", "E"), []string{"B", "C", "D"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			names, more := premiseNames(tc.facts)
			if strings.Join(names, "|") != strings.Join(tc.want, "|") || more != tc.more {
				t.Fatalf("names = %q and %d more, want %q and %d more", names, more, tc.want, tc.more)
			}
		})
	}
	t.Run("only projects of the portfolio's shape count", func(t *testing.T) {
		facts := songs("S1", "S2", "S3", "S4")
		facts.Members = append(facts.Members, foldermap.Project{Name: "Draft", Shape: folderdigest.ShapeManuscript})
		facts.Members = append([]foldermap.Project{{Name: "Notes", Shape: folderdigest.ShapeNotes}}, facts.Members...)
		names, more := premiseNames(facts)
		if strings.Join(names, "|") != "S1|S2|S3" || more != 1 {
			t.Fatalf("names = %q and %d more", names, more)
		}
	})
	t.Run("no portfolio, no names", func(t *testing.T) {
		if names, more := premiseNames(foldermap.Facts{}); names != nil || more != 0 {
			t.Fatalf("names = %q and %d more", names, more)
		}
	})
}

// chosenKinds are the kinds of the steps the plan has chosen.
func chosenKinds(plan Plan) map[string]bool {
	out := map[string]bool{}
	for _, s := range plan.Steps {
		if s.Chosen {
			out[s.Kind] = true
		}
	}
	return out
}

// toolsRecord is an Albums folder whose only candidate is a plugin that
// connects to the songs, so a plan can choose a step that is neither the Home
// nor the group.
func toolsRecord() Record {
	rec := homeStageRecord(nil)
	rec.Candidates = []folderlook.Candidate{{ID: "plugin:reaper", Kind: folderlook.KindIntegration, Name: "REAPER tools",
		Action: folderlook.ActionInstall, Required: true}}
	return rec
}

// The outcome sentence is written from the plan the card shows after the
// answer, so it names the Home only when the plan chose it, the group only
// when the plan chose that, and the agents only when the plan hires them
// (FR3, FR4).
func TestPremiseOutcome_FollowsTheChosenSteps(t *testing.T) {
	const homeName = "Music Production Home"
	chosenGroup := func(rec Record) Plan {
		rec.Sections = &folderlook.Sections{PlanSteps: []string{folderlook.HandleGroup}}
		return BuildPlan(rec, nil)
	}
	toolsOnly := func() (Record, Plan) {
		rec := toolsRecord()
		for _, step := range folderlook.Steps(rec.FolderName, rec.Facts, rec.Candidates) {
			if step.Kind == folderlook.StepIntegration {
				return rec, BuildPlan(rec, []string{step.Handle})
			}
		}
		t.Fatal("no tools step offered")
		return rec, Plan{}
	}
	cases := []struct {
		name string
		rec  Record
		plan Plan
		want string
	}{
		{"a new Home with named roles to hire", homeStageRecord(&folderlook.HomeFacts{RolesKnown: true, MissingRoles: []string{"Library Manager"}}),
			Plan{}, "I'll plan a Music Production Home for Albums: a card for each song, and agents to keep track of them."},
		{"a new Home whose roles the host cannot name yet", homeStageRecord(&folderlook.HomeFacts{}),
			Plan{}, "I'll plan a Music Production Home for Albums: a card for each song, and agents to keep track of them."},
		{"a new Home with no facts reported", homeStageRecord(nil),
			Plan{}, "I'll plan a Music Production Home for Albums: a card for each song, and agents to keep track of them."},
		{"a new Home that hires nobody", homeStageRecord(&folderlook.HomeFacts{RolesKnown: true}),
			Plan{}, "I'll plan a Music Production Home for Albums: a card for each song."},
		{"the Home exists and nobody is hired", homeStageRecord(&folderlook.HomeFacts{HomeExists: true, RolesKnown: true}),
			Plan{}, "I'll plan adding Albums to your Music Production Home: a card for each song."},
		{"the Home exists and a role is missing", homeStageRecord(&folderlook.HomeFacts{HomeExists: true, RolesKnown: true, MissingRoles: []string{"Library Manager"}}),
			Plan{}, "I'll plan adding Albums to your Music Production Home: a card for each song, and agents to keep track of them."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := tc.plan
			if len(plan.Steps) == 0 {
				plan = homePlan(t, tc.rec)
			}
			got := premiseOutcome(&tc.rec, plan)
			if got != tc.want {
				t.Fatalf("outcome = %q, want %q", got, tc.want)
			}
			kinds := chosenKinds(plan)
			if !kinds[folderlook.StepHome] || kinds[folderlook.StepGroup] {
				t.Fatalf("fixture error: chosen kinds = %v", kinds)
			}
			if strings.Contains(got, "agents") != (len(plan.Hires) > 0 || plan.HiresNote != "") {
				t.Fatalf("the agents clause must follow the plan's hires (%d, %q): %q", len(plan.Hires), plan.HiresNote, got)
			}
		})
	}

	t.Run("a new Home that is offered but not chosen is never named (the live case)", func(t *testing.T) {
		rec := homeStageRecord(&folderlook.HomeFacts{RolesKnown: true, MissingRoles: []string{"Library Manager"}})
		plan := BuildPlan(rec, nil)
		if kinds := chosenKinds(plan); kinds[folderlook.StepHome] || !kinds[folderlook.StepGroup] {
			t.Fatalf("fixture error: a new Home is offered, not chosen, by default: %v", kinds)
		}
		got := premiseOutcome(&rec, plan)
		if got != "I'll plan turning Albums into a group: a workspace for each song." {
			t.Fatalf("outcome = %q", got)
		}
		if strings.Contains(got, homeName) || strings.Contains(got, "agents") {
			t.Fatalf("the sentence names what the plan does not hold: %q", got)
		}
	})
	t.Run("the agent chose the group over an existing Home", func(t *testing.T) {
		rec := homeStageRecord(&folderlook.HomeFacts{HomeExists: true, RolesKnown: true})
		plan := chosenGroup(rec)
		if kinds := chosenKinds(plan); kinds[folderlook.StepHome] || !kinds[folderlook.StepGroup] {
			t.Fatalf("fixture error: %v", kinds)
		}
		if got := premiseOutcome(&rec, plan); got != "I'll plan turning Albums into a group: a workspace for each song." {
			t.Fatalf("outcome = %q", got)
		}
	})
	t.Run("only tools chosen leaves it out", func(t *testing.T) {
		rec, plan := toolsOnly()
		if kinds := chosenKinds(plan); kinds[folderlook.StepHome] || kinds[folderlook.StepGroup] || !kinds[folderlook.StepIntegration] {
			t.Fatalf("fixture error: %v", kinds)
		}
		if got := premiseOutcome(&rec, plan); got != "" {
			t.Fatalf("outcome = %q, want none", got)
		}
	})
	t.Run("a plan that cannot run, or no plan, leaves it out", func(t *testing.T) {
		rec := homeStageRecord(&folderlook.HomeFacts{HomeExists: true, RolesKnown: true})
		if got := premiseOutcome(&rec, Plan{}); got != "" {
			t.Fatalf("empty plan: %q", got)
		}
		plan := homePlan(t, rec)
		plan.Runnable = false
		if got := premiseOutcome(&rec, plan); got != "" {
			t.Fatalf("plan that cannot run: %q", got)
		}
		if got := premiseOutcome(nil, plan); got != "" {
			t.Fatalf("no record: %q", got)
		}
	})
	t.Run("a name that begins with a vowel takes an", func(t *testing.T) {
		rec := homeStageRecord(&folderlook.HomeFacts{RolesKnown: true})
		rec.Candidates[0].Name = "Audio Home"
		if got := premiseOutcome(&rec, homePlan(t, rec)); got != "I'll plan an Audio Home for Albums: a card for each song." {
			t.Fatalf("outcome = %q", got)
		}
	})
	t.Run("the folder's name is drawn as plain text, controls dropped", func(t *testing.T) {
		rec := homeStageRecord(&folderlook.HomeFacts{HomeExists: true, RolesKnown: true})
		rec.FolderName = "My\nAlbums" + string(rune(0x202e)) + " <b>"
		got := premiseOutcome(&rec, homePlan(t, rec))
		if got != "I'll plan adding MyAlbums <b> to your Music Production Home: a card for each song." {
			t.Fatalf("outcome = %q", got)
		}
	})
}

// The wording comes from the tables, so another kind reads right with no
// change: a manuscript portfolio with a Writing Home speaks of manuscripts.
func TestPremiseOutcome_AnotherKindReadsRight(t *testing.T) {
	rec := homeStageRecord(&folderlook.HomeFacts{HomeExists: true, RolesKnown: true})
	rec.Facts.Portfolio.Shape = folderdigest.ShapeManuscript
	rec.Facts.Portfolio.HomeName = "Writing Home"
	rec.Facts.Portfolio.HomeProviderKey = "manuscript_home"
	for i := range rec.Facts.Members {
		rec.Facts.Members[i].Shape = folderdigest.ShapeManuscript
	}
	rec.Candidates[0].Name = "Writing Home"
	rec.Candidates[0].HomeProviderKey = "manuscript_home"
	if got := premiseOutcome(&rec, homePlan(t, rec)); got != "I'll plan adding Albums to your Writing Home: a card for each manuscript." {
		t.Fatalf("home outcome = %q", got)
	}
	if got := premiseOutcome(&rec, BuildPlan(rec, []string{folderlook.HandleGroup})); got != "I'll plan turning Albums into a group: a workspace for each manuscript." {
		t.Fatalf("group outcome = %q", got)
	}
}

// Confirming is remembered with the kind and time, read back on every view,
// shown as one plain line, and lets Set it up run (FR5, FR9).
func TestPremise_ConfirmedIsRememberedAndSetUpRuns(t *testing.T) {
	h := newHarness(t, demo(t))
	start := h.start(t, "")
	asked := h.view(t, start.WorkspaceID)
	answered, err := h.svc.AnswerPremise(context.Background(), "u1", start.WorkspaceID, PremiseConfirmed)
	if err != nil {
		t.Fatal(err)
	}
	// The press changes nothing but the premise: the plan it showed is the
	// plan the sentence was written from (FR4), and the sentence is gone.
	if answered.Plan == nil || asked.Plan == nil ||
		answered.Plan.Digest != asked.Plan.Digest || strings.Join(answered.Plan.Selected, ",") != strings.Join(asked.Plan.Selected, ",") {
		t.Fatalf("the plan changed on confirming: asked=%+v answered=%+v", asked.Plan, answered.Plan)
	}
	if answered.Premise.Outcome != "" || answered.Premise.NothingYet != "" || len(answered.Premise.Names) != 0 || answered.Premise.NamesMore != 0 {
		t.Fatalf("a confirmed premise keeps only its line: %+v", answered.Premise)
	}
	if answered.Premise == nil || answered.Premise.State != PremiseConfirmed || answered.Premise.Confirm != "" ||
		answered.Premise.Sentence != "A music production folder: 7 REAPER songs and 1 Logic Pro song." {
		t.Fatalf("confirmed premise = %+v", answered.Premise)
	}
	rec, _ := h.svc.Record("u1", start.WorkspaceID)
	if rec.Premise == nil || rec.Premise.Answer != PremiseConfirmed || rec.Premise.Provider != "music_project_management" || rec.Premise.At.IsZero() {
		t.Fatalf("stored answer = %+v", rec.Premise)
	}
	// The same answer again (a second tab) is not an error; the other is.
	if _, err := h.svc.AnswerPremise(context.Background(), "u1", start.WorkspaceID, PremiseConfirmed); err != nil {
		t.Fatalf("same answer again = %v", err)
	}
	if _, err := h.svc.AnswerPremise(context.Background(), "u1", start.WorkspaceID, PremiseSomethingElse); !errors.Is(err, ErrWrongState) {
		t.Fatalf("a different answer later = %v", err)
	}
	// A reload, a list (Needs you, the map) and a Look further that keeps the
	// kind do not ask again.
	for _, v := range h.svc.List(context.Background(), "u1") {
		if v.Premise == nil || v.Premise.State != PremiseConfirmed {
			t.Fatalf("listed premise = %+v", v.Premise)
		}
	}
	if _, err := h.svc.LookFurther(context.Background(), "u1", start.WorkspaceID, nil, ""); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	again := h.view(t, start.WorkspaceID)
	if again.Premise == nil || again.Premise.State != PremiseConfirmed {
		t.Fatalf("after Look further = %+v", again.Premise)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", start.WorkspaceID, SetUpRequest{PlanDigest: again.Plan.Digest}); err != nil {
		t.Fatalf("set up after confirming = %v", err)
	}
	h.svc.Wait()
}

// A look that recognises a different kind (another Home provider) asks again.
func TestPremise_AskedAgainWhenTheKindChanges(t *testing.T) {
	h := newHarness(t, demo(t))
	start := h.start(t, "")
	if _, err := h.svc.AnswerPremise(context.Background(), "u1", start.WorkspaceID, PremiseConfirmed); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.deps.Store.Update(start.WorkspaceID, func(r *Record) error {
		r.Facts.Portfolio.HomeProviderKey, r.Facts.Portfolio.HomeName = "manuscript_home", "Writing Home"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	v := h.view(t, start.WorkspaceID)
	if v.Premise == nil || v.Premise.State != PremiseAsked || !strings.HasPrefix(v.Premise.Sentence, "This looks like a writing folder: ") {
		t.Fatalf("premise after the kind changed = %+v", v.Premise)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", start.WorkspaceID, SetUpRequest{PlanDigest: v.Plan.Digest}); !errors.Is(err, ErrPremiseOwed) {
		t.Fatalf("set up on a changed kind = %v", err)
	}
}

// Not asked: a single project, loose files, a record made before the premise
// shipped, a folder fed from inside the Home that would hold it. Asked: a
// folder fed from inside a Home of another kind (FR8, FR11).
func TestPremise_WhenItIsNotAsked(t *testing.T) {
	single := newHarness(t, build(t, "Song.rpp"))
	if v := single.view(t, single.start(t, "").WorkspaceID); v.Premise != nil {
		t.Fatalf("single project: %+v", v.Premise)
	}
	dump := newHarness(t, build(t, "a.pdf", "b.jpg", "c.zip", "d.docx", "e.dmg", "f.txt", "g.pdf", "h.jpg", "i.zip", "j.docx", "k.dmg", "l.txt"))
	if v := dump.view(t, dump.start(t, "").WorkspaceID); v.Premise != nil {
		t.Fatalf("loose files: %+v", v.Premise)
	}

	inside := newHarness(t, demo(t))
	inside.svc.deps.Homes = fakeHomes{"music-home": "music_project_management", "writing-home": "manuscript_home"}
	fed := inside.startFrom(t, "music-home")
	v := inside.view(t, fed.WorkspaceID)
	if v.Premise != nil {
		t.Fatalf("fed from inside the matching Home: %+v", v.Premise)
	}
	if rec, _ := inside.svc.Record("u1", fed.WorkspaceID); rec.FedFromHome != "music_project_management" {
		t.Fatalf("feed origin = %q", rec.FedFromHome)
	}
	if _, err := inside.svc.SetUp(context.Background(), "u1", fed.WorkspaceID, SetUpRequest{PlanDigest: v.Plan.Digest}); err != nil {
		t.Fatalf("set up fed from inside the Home = %v", err)
	}
	inside.svc.Wait()

	other := newHarness(t, demo(t))
	other.svc.deps.Homes = fakeHomes{"writing-home": "manuscript_home"}
	if v := other.view(t, other.startFrom(t, "writing-home").WorkspaceID); v.Premise == nil || v.Premise.State != PremiseAsked {
		t.Fatalf("fed from a Home of another kind: %+v", v.Premise)
	}
	// A page that is not a Home is an ordinary feed.
	plain := newHarness(t, demo(t))
	plain.svc.deps.Homes = fakeHomes{}
	if v := plain.view(t, plain.startFrom(t, "some-workspace").WorkspaceID); v.Premise == nil {
		t.Fatal("fed from a plain workspace page must ask")
	}

	legacy := newHarness(t, demo(t))
	lv := legacy.start(t, "")
	if _, err := legacy.svc.deps.Store.Update(lv.WorkspaceID, func(r *Record) error {
		r.AsksPremise = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	report := legacy.view(t, lv.WorkspaceID)
	if report.Premise != nil {
		t.Fatalf("a record from before the premise: %+v", report.Premise)
	}
	if _, err := legacy.svc.SetUp(context.Background(), "u1", lv.WorkspaceID, SetUpRequest{PlanDigest: report.Plan.Digest}); err != nil {
		t.Fatalf("legacy set up = %v", err)
	}
	legacy.svc.Wait()
}

// "It's something else" withdraws the recommendation: no plan, no Set it up,
// only Keep and Look further (FR6). A look with the owner's words brings the
// report back with nothing ticked that installs or makes the Home; a tick of
// the owner's own brings a step and its install back (FR7).
func TestPremise_SomethingElseWithdrawsThenALookOffersNoInstalls(t *testing.T) {
	h := newHarness(t, demo(t))
	start := h.start(t, "")
	shown := h.view(t, start.WorkspaceID)
	withdrawn, err := h.svc.AnswerPremise(context.Background(), "u1", start.WorkspaceID, PremiseSomethingElse)
	if err != nil {
		t.Fatal(err)
	}
	if withdrawn.Premise == nil || withdrawn.Premise.State != PremiseSomethingElse || !withdrawn.Premise.Withdrawn ||
		withdrawn.Premise.Sentence != "Nothing is installed or created." {
		t.Fatalf("withdrawn premise = %+v", withdrawn.Premise)
	}
	if withdrawn.Premise.Outcome != "" || withdrawn.Premise.NothingYet != "" || len(withdrawn.Premise.Names) != 0 {
		t.Fatalf("a withdrawn premise carries no outcome: %+v", withdrawn.Premise)
	}
	if withdrawn.Plan != nil || strings.Join(withdrawn.Actions, ",") != "keep,look_further" {
		t.Fatalf("withdrawn plan=%+v actions=%v", withdrawn.Plan, withdrawn.Actions)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", start.WorkspaceID, SetUpRequest{PlanDigest: shown.Plan.Digest}); err == nil {
		t.Fatal("set up after something else must be refused")
	}
	// Keep it as a plain workspace: the existing Keep, nothing run.
	kept, err := h.svc.Keep(context.Background(), "u1", start.WorkspaceID)
	if err != nil || strings.Join(kept.Actions, ",") != "look_further" {
		t.Fatalf("kept = %v %v", kept.Actions, err)
	}
	if len(h.setups.runs) != 0 {
		t.Fatal("something else ran a setup")
	}

	// Look again with the owner's words.
	if _, err := h.svc.LookFurther(context.Background(), "u1", start.WorkspaceID, nil, "These are lecture recordings"); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	after := h.view(t, start.WorkspaceID)
	if after.Premise == nil || after.Premise.Withdrawn || after.Plan == nil {
		t.Fatalf("after the look: premise=%+v plan=%+v", after.Premise, after.Plan)
	}
	if strings.Join(after.Actions, ",") != "set_up,look_further" && strings.Join(after.Actions, ",") != "set_up,keep,look_further" {
		t.Fatalf("actions after the look = %v", after.Actions)
	}
	for _, s := range after.Plan.Steps {
		installs := s.Kind == folderlook.StepIntegration
		if (s.Kind == folderlook.StepHome || installs) && s.Chosen {
			t.Fatalf("step %s is ticked after something else", s.Handle)
		}
	}
	if len(after.Plan.Installs) != 0 {
		t.Fatalf("installs after something else = %+v", after.Plan.Installs)
	}
	// The owner ticks the tools themselves: the step and its install return.
	ticked, err := h.svc.Plan("u1", start.WorkspaceID, append(append([]string(nil), after.Plan.Selected...), "reaper-tools"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ticked.Installs) != 1 || ticked.Installs[0].Step != "reaper-tools" {
		t.Fatalf("installs after the owner's tick = %+v", ticked.Installs)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", start.WorkspaceID, SetUpRequest{PlanDigest: ticked.Digest, Selected: ticked.Selected}); err != nil {
		t.Fatalf("set up the owner's own choice = %v", err)
	}
	h.svc.Wait()
}
