package folderagent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// setup-card-clarity FR15-FR20: the main list holds the path being
// recommended; a step that cannot run with a chosen one is an alternative, with
// the host's one line on what taking it gives up.

const (
	groupTradeoff = "One workspace per song inside Albums, and no Music Production Home: nothing lists songs across your other folders."
	homeTradeoff  = "One shelf for every song across your folders; songs get workspaces when you open them."
)

func alternativesRecord() Record {
	return homeStageRecord(&folderlook.HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.5.0"})
}

func TestAlternatives_ChoosingTheHomeOffersTheGroupAsTheOtherWay(t *testing.T) {
	rec := alternativesRecord()
	home, group := stepHandleOfKind(t, rec, folderlook.StepHome), stepHandleOfKind(t, rec, folderlook.StepGroup)
	plan := BuildPlan(rec, []string{home})
	want := []Alternative{{Step: group, Title: "Turn Albums into a group with one workspace per song (6)", Tradeoff: groupTradeoff}}
	if !reflect.DeepEqual(plan.Alternatives, want) {
		t.Fatalf("alternatives = %+v, want %+v", plan.Alternatives, want)
	}
}

func TestAlternatives_ChoosingTheGroupOffersTheHomeAsTheOtherWay(t *testing.T) {
	rec := alternativesRecord()
	home, group := stepHandleOfKind(t, rec, folderlook.StepHome), stepHandleOfKind(t, rec, folderlook.StepGroup)
	plan := BuildPlan(rec, []string{group})
	want := []Alternative{{Step: home, Title: "Create your Music Production Home and put Albums on its shelf (6 songs)", Tradeoff: homeTradeoff}}
	if !reflect.DeepEqual(plan.Alternatives, want) {
		t.Fatalf("alternatives = %+v, want %+v", plan.Alternatives, want)
	}
}

func TestAlternatives_NothingChosenAgainstAStepLeavesNoAlternative(t *testing.T) {
	rec := alternativesRecord()
	// Neither of the two is chosen: both are simply optional, neither is "the other way".
	if plan := BuildPlan(rec, []string{}); len(plan.Alternatives) != 0 {
		t.Fatalf("with nothing chosen: %+v", plan.Alternatives)
	}
	// An add-on never excludes anything, so it is never an alternative: it
	// stays among the optional steps whatever else is chosen.
	rec.Candidates = append(rec.Candidates, folderlook.Candidate{ID: "skill:mix", Kind: folderlook.KindSkill, Name: "Mix Notes", Action: folderlook.ActionAdd})
	var skill string
	for _, s := range folderlook.Steps(rec.FolderName, rec.Facts, rec.Candidates) {
		if s.Name == "Mix Notes" {
			skill = s.Handle
		}
	}
	home := stepHandleOfKind(t, rec, folderlook.StepHome)
	plan := BuildPlan(rec, []string{home})
	for _, a := range plan.Alternatives {
		if a.Step == skill {
			t.Fatalf("an add-on is an alternative: %+v", plan.Alternatives)
		}
	}
	if len(plan.Alternatives) != 1 {
		t.Fatalf("alternatives = %+v, want only the group", plan.Alternatives)
	}
	// Without a Home candidate there is nothing for the group to exclude.
	solo := alternativesRecord()
	solo.Candidates = nil
	group := stepHandleOfKind(t, solo, folderlook.StepGroup)
	if plan := BuildPlan(solo, []string{group}); len(plan.Alternatives) != 0 {
		t.Fatalf("a lone group has alternatives: %+v", plan.Alternatives)
	}
}

func TestAlternatives_SwappingChangesTheDigestAndSwappingBackRestoresIt(t *testing.T) {
	rec := alternativesRecord()
	home, group := stepHandleOfKind(t, rec, folderlook.StepHome), stepHandleOfKind(t, rec, folderlook.StepGroup)
	first := BuildPlan(rec, []string{home})
	swapped := BuildPlan(rec, []string{group})
	back := BuildPlan(rec, []string{home})
	if first.Digest == "" || first.Digest == swapped.Digest {
		t.Fatalf("the swap kept the digest: %s %s", first.Digest, swapped.Digest)
	}
	if back.Digest != first.Digest || !reflect.DeepEqual(back.Alternatives, first.Alternatives) {
		t.Fatalf("swapping back did not restore the first plan: %+v vs %+v", back, first)
	}
	// The swap changes what the plan creates, installs and hires in step.
	if first.Stage == swapped.Stage || len(swapped.Installs) != 0 || len(swapped.Hires) != 0 || swapped.HiresNote != "" {
		t.Fatalf("swapped plan = stage %s installs %+v hires %+v", swapped.Stage, swapped.Installs, swapped.Hires)
	}
	if len(first.Installs) != 1 {
		t.Fatalf("the Home plan installs = %+v", first.Installs)
	}
	// The alternatives are information, not a consequence: not in the digest.
	bare := first
	bare.Alternatives = nil
	if planDigest(bare) != first.Digest {
		t.Fatal("the alternatives list is in the digest")
	}
}

func TestAlternatives_TheSourceOfThePlanIsNotRewrittenBySwapping(t *testing.T) {
	rec := alternativesRecord()
	home, group := stepHandleOfKind(t, rec, folderlook.StepHome), stepHandleOfKind(t, rec, folderlook.StepGroup)
	rec.Sections = &folderlook.Sections{Plan: "Create [" + home + "].", PlanSteps: []string{home}}
	recommended := BuildPlan(rec, nil)
	if recommended.Source != PlanByAgent || len(recommended.Selected) != 1 || recommended.Selected[0] != home {
		t.Fatalf("recommended = %+v", recommended)
	}
	// The owner takes the other way: the existing source indication applies
	// unchanged and the agent's words stay as written.
	swapped := BuildPlan(rec, []string{group})
	if swapped.Source != recommended.Source || swapped.Words != recommended.Words || swapped.WordsRaw != recommended.WordsRaw {
		t.Fatalf("swapping rewrote the source or the agent's words: %+v", swapped)
	}
}

func TestAlternatives_TheViewCarriesThemAndAReceiptHasNone(t *testing.T) {
	rec := alternativesRecord()
	group := stepHandleOfKind(t, rec, folderlook.StepGroup)
	raw, err := json.Marshal(planViewOf(BuildPlan(rec, []string{stepHandleOfKind(t, rec, folderlook.StepHome)})))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Alternatives []map[string]string `json:"alternatives"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Alternatives) != 1 || decoded.Alternatives[0]["step"] != group || decoded.Alternatives[0]["tradeoff"] != groupTradeoff ||
		!strings.HasPrefix(decoded.Alternatives[0]["title"], "Turn Albums into a group") {
		t.Fatalf("alternatives in the view = %+v", decoded.Alternatives)
	}
	// Never nil in the view, so the card can always read the list.
	if view := planViewOf(BuildPlan(rec, []string{})); view.Alternatives == nil {
		t.Fatal("the view's alternatives are nil")
	}
}

// The swap is the existing re-plan request: Plan with the other step in place of
// the one it excludes, and Set it up runs that plan, not the first one.
func TestAlternatives_ASwapThroughTheServiceRunsTheSwappedPlan(t *testing.T) {
	h := newHarness(t, demo(t))
	view := h.start(t, "")
	if _, err := h.svc.AnswerPremise(context.Background(), "u1", view.WorkspaceID, PremiseConfirmed); err != nil {
		t.Fatal(err)
	}
	rec, _ := h.svc.Record("u1", view.WorkspaceID)
	home, group := stepHandleOfKind(t, *rec, folderlook.StepHome), stepHandleOfKind(t, *rec, folderlook.StepGroup)

	homePlan, err := h.svc.Plan("u1", view.WorkspaceID, []string{home})
	if err != nil || len(homePlan.Alternatives) != 1 || homePlan.Alternatives[0].Step != group {
		t.Fatalf("home plan = %+v %v", homePlan.Alternatives, err)
	}
	groupPlan, err := h.svc.Plan("u1", view.WorkspaceID, []string{group})
	if err != nil || len(groupPlan.Alternatives) != 1 || groupPlan.Alternatives[0].Step != home || groupPlan.Digest == homePlan.Digest {
		t.Fatalf("group plan = %+v %v", groupPlan.Alternatives, err)
	}
	// The first plan's digest no longer confirms the swapped one.
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: homePlan.Digest, Selected: []string{group}}); err == nil {
		t.Fatal("the home plan's digest confirmed the group plan")
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: groupPlan.Digest, Selected: []string{group}}); err != nil {
		t.Fatalf("set up on the swapped plan = %v", err)
	}
	h.svc.Wait()
	if len(h.setups.runs) != 1 {
		t.Fatalf("runs = %d", len(h.setups.runs))
	}
	var kinds []string
	for _, line := range h.setups.runs[0].Lines {
		kinds = append(kinds, line.Kind)
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, FolderPlanPromote) || strings.Contains(joined, personalassistant.FolderPlanHome) ||
		strings.Contains(joined, personalassistant.FolderPlanProvider) {
		t.Fatalf("the swapped run's lines = %s: want the group and no Home", joined)
	}
}
