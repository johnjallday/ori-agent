package folderagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
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
		p.Confirm != "Manage them as songs" || p.Other != "It's something else" {
		t.Fatalf("premise words = %+v", p)
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

// Confirming is remembered with the kind and time, read back on every view,
// shown as one plain line, and lets Set it up run (FR5, FR9).
func TestPremise_ConfirmedIsRememberedAndSetUpRuns(t *testing.T) {
	h := newHarness(t, demo(t))
	start := h.start(t, "")
	answered, err := h.svc.AnswerPremise(context.Background(), "u1", start.WorkspaceID, PremiseConfirmed)
	if err != nil {
		t.Fatal(err)
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
