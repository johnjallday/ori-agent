package personalassistant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

func TestFolderDefaultBrief_EveryShapeHasOneAndTheBlueprintGoalWins(t *testing.T) {
	want := map[folderdigest.Shape]string{
		folderdigest.ShapeCode:       "Understand this project, find evidence-backed risks, and propose improvements before anything changes.",
		folderdigest.ShapeManuscript: "Understand this manuscript's structure and state, and suggest the next thing to write or fix.",
		folderdigest.ShapeNotes:      "Learn how these notes are organized and suggest one way to make them more useful.",
		folderdigest.ShapeCorpus:     "Catalogue what this collection contains and suggest the first thing worth doing with it.",
		folderdigest.ShapeAudio:      "Understand this song's project and suggest the next production step.",
	}
	for shape, brief := range want {
		if got := FolderDefaultBrief(shape, ""); got != brief {
			t.Errorf("%s: brief = %q", shape, got)
		}
		if got := FolderDefaultBrief(shape, "  Ship the\n next release.  "); got != "Ship the next release." {
			t.Errorf("%s: a blueprint goal must win, got %q", shape, got)
		}
	}
	if got := FolderDefaultBrief(folderdigest.Shape("unknown"), ""); got != "" {
		t.Errorf("an unknown shape has no default, got %q", got)
	}
}

func TestNormalizeFolderBrief_KeepsOneBoundedLineOfPlainText(t *testing.T) {
	if got, err := NormalizeFolderBrief("  Fix the\n\tflaky tests  first "); err != nil || got != "Fix the flaky tests first" {
		t.Fatalf("brief = %q, %v", got, err)
	}
	if got, err := NormalizeFolderBrief("   "); err != nil || got != "" {
		t.Fatalf("an empty brief stays empty for the default: %q, %v", got, err)
	}
	if _, err := NormalizeFolderBrief(strings.Repeat("é", WorkspaceGoalMaxRunes)); err != nil {
		t.Fatalf("a brief at the limit is accepted: %v", err)
	}
	for name, text := range map[string]string{
		"too long":          strings.Repeat("a", WorkspaceGoalMaxRunes+1),
		"control character": "Fix it\x07 now",
		"nul":               "Fix\x00it",
	} {
		if _, err := NormalizeFolderBrief(text); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func folderHireFixture(t *testing.T) *oneCardFixture {
	t.Helper()
	f := newOneCardFixture(t)
	f.setup.hire = &FolderHire{Mode: FolderHireCreate, AgentName: "Song Assistant", RoleLabel: "Producer", Model: "some-model"}
	f.setup.briefDefault = "Understand this song's project and suggest the next production step."
	current, err := f.service.Current(context.Background(), "local")
	if err != nil || current.Offer == nil {
		t.Fatalf("current = %+v, %v", current.Offer, err)
	}
	f.offer = *current.Offer
	return f
}

func TestFolderSetup_ThePlanNamesTheHireOutsideTheDigest(t *testing.T) {
	f := folderHireFixture(t)
	plan := f.offer.Plan
	if plan == nil || plan.Hire == nil || plan.Hire.AgentName != "Song Assistant" || plan.Hire.Mode != FolderHireCreate ||
		plan.BriefDefault == "" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Digest != FolderPlanDigest(oneCardLines()) {
		t.Fatal("the hire and the brief are not part of what the digest covers")
	}
	// Another name on the card is not another consent: the lines say what runs.
	f.setup.hire = &FolderHire{Mode: FolderHireBind, AgentName: "Studio Assistant"}
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID,
		FolderSetupInput{RequestID: "r1", PlanDigest: plan.Digest}); err != nil {
		t.Fatalf("a renamed hire must not make the plan stale: %v", err)
	}
}

func TestFolderSetup_HireAndStartHandsTheTypedBriefToTheRun(t *testing.T) {
	f := folderHireFixture(t)
	f.setup.hold = make(chan struct{})
	ctx := context.Background()
	view, err := f.service.StartSetup(ctx, "local", f.offer.ID, FolderSetupInput{
		RequestID: "r1", PlanDigest: f.offer.Plan.Digest, Brief: "  Get the mix\nready for mastering. ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Setup == nil || view.Setup.Brief != "Get the mix ready for mastering." || view.Setup.Hire == nil ||
		view.Setup.Hire.AgentName != "Song Assistant" {
		t.Fatalf("the running card carries the brief and the hire: %+v", view.Setup)
	}
	waitFor(t, "the run to start", func() bool { return f.setup.runCount() == 1 })
	f.setup.mu.Lock()
	ran := f.setup.ran[0]
	f.setup.mu.Unlock()
	close(f.setup.hold)
	if ran.Brief != "Get the mix ready for mastering." {
		t.Fatalf("run brief = %q", ran.Brief)
	}
	if stored := f.stored(t).Setup; stored == nil || stored.Brief != "Get the mix ready for mastering." || stored.Hire == nil {
		t.Fatalf("stored run = %+v", stored)
	}
}

func TestFolderSetup_AClearedBriefTakesTheDefault(t *testing.T) {
	f := folderHireFixture(t)
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID,
		FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest, Brief: " \n "}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run", func() bool { return f.setup.runCount() == 1 })
	f.setup.mu.Lock()
	defer f.setup.mu.Unlock()
	if got := f.setup.ran[0].Brief; got != f.setup.briefDefault {
		t.Fatalf("a workspace is never created without a goal: brief = %q", got)
	}
}

func TestFolderSetup_APlanWithoutAQuestionTakesNoBrief(t *testing.T) {
	f := newOneCardFixture(t)
	if _, err := f.service.StartSetup(context.Background(), "local", f.offer.ID,
		FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest, Brief: "Something the card never asked"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run", func() bool { return f.setup.runCount() == 1 })
	f.setup.mu.Lock()
	brief := f.setup.ran[0].Brief
	f.setup.mu.Unlock()
	if brief != "" || f.stored(t).Setup.Brief != "" {
		t.Fatalf("brief = %q", brief)
	}
}

func TestFolderSetup_AnInvalidBriefIsRefusedAndChangesNothing(t *testing.T) {
	f := folderHireFixture(t)
	for i, brief := range []string{strings.Repeat("a", WorkspaceGoalMaxRunes+1), "Fix\x00it"} {
		_, err := f.service.StartSetup(context.Background(), "local", f.offer.ID,
			FolderSetupInput{RequestID: "r" + string(rune('1'+i)), PlanDigest: f.offer.Plan.Digest, Brief: brief})
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("brief %d: err = %v", i, err)
		}
	}
	if f.setup.runCount() != 0 || f.stored(t).Status != FolderOfferPending || f.stored(t).Setup != nil {
		t.Fatal("a refused brief must change nothing")
	}
}

func TestFolderSetup_AResumeCarriesOnWithTheBriefItStartedWith(t *testing.T) {
	f := folderHireFixture(t)
	f.setup.finish = func() FolderSetupUpdate {
		return FolderSetupUpdate{Status: FolderSetupStopped, StopReason: FolderStopNeedsModel, RunID: "new-run"}
	}
	ctx := context.Background()
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID,
		FolderSetupInput{RequestID: "r1", PlanDigest: f.offer.Plan.Digest, Brief: "Finish the bridge."}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to stop", func() bool {
		s := f.stored(t).Setup
		return s != nil && s.Status == FolderSetupStopped && !f.service.isRunning(f.offer.ID)
	})
	f.setup.finish = nil
	if _, err := f.service.StartSetup(ctx, "local", f.offer.ID,
		FolderSetupInput{RequestID: "r2", PlanDigest: f.stored(t).Setup.PlanDigest, Brief: "Something else"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the resumed run", func() bool { return f.setup.runCount() == 2 })
	f.setup.mu.Lock()
	defer f.setup.mu.Unlock()
	if got := f.setup.ran[1].Brief; got != "Finish the bridge." {
		t.Fatalf("resumed brief = %q", got)
	}
}

func TestFolderSetup_AStoredRunKeepsItsHireBoundedAndPathFree(t *testing.T) {
	run := FolderSetupRun{Status: FolderSetupRunning, Lines: oneCardLines(), Brief: "Finish the bridge.",
		Hire: &FolderHire{Mode: FolderHireCreate, AgentName: "Song Assistant"}}
	if err := validateFolderSetupRun(run); err != nil {
		t.Fatalf("a well-formed run: %v", err)
	}
	for name, change := range map[string]func(*FolderSetupRun){
		"unknown mode":     func(r *FolderSetupRun) { r.Hire.Mode = "promote" },
		"path-like name":   func(r *FolderSetupRun) { r.Hire.AgentName = "/Users/me/agent" },
		"unbounded brief":  func(r *FolderSetupRun) { r.Brief = strings.Repeat("a", WorkspaceGoalMaxRunes+1) },
		"multi-line brief": func(r *FolderSetupRun) { r.Brief = "one\ntwo" },
	} {
		bad := run
		hire := *run.Hire
		bad.Hire = &hire
		change(&bad)
		if err := validateFolderSetupRun(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
