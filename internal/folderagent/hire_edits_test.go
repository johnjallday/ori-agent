package folderagent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func editableHires(t *testing.T) (*harness, string, []string, PlanView) {
	t.Helper()
	h := hiringHarness(t, fakeHiring{models: []HireModel{sonnet, {Provider: "claude", Model: "alternate", Label: "Alternate"}}})
	h.setups.setModel(testModel)
	view := h.start(t, "")
	if _, err := h.svc.AnswerPremise(context.Background(), "u1", view.WorkspaceID, PremiseConfirmed); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.deps.Store.Update(view.WorkspaceID, func(r *Record) error {
		for i := range r.Candidates {
			if r.Candidates[i].Kind == folderlook.KindHomeProvider {
				r.Candidates[i].Home = newHomeFacts(folderlook.HomeHire{RoleID: "manager", Name: "Manager", Job: "Plans the work."}, folderlook.HomeHire{RoleID: "library", Name: "Librarian"})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := h.svc.Record("u1", view.WorkspaceID)
	selected := []string{stepHandleOfKind(t, *rec, folderlook.StepHome)}
	plan, err := h.svc.Plan("u1", view.WorkspaceID, selected)
	if err != nil || len(plan.Hires) != 2 || plan.Hires[0].Key == "" {
		t.Fatalf("editable plan: %+v %v", plan, err)
	}
	return h, view.WorkspaceID, selected, plan
}

func TestHireEdits_PreviewIsReadOnlyAndMaterialConsentIsRequired(t *testing.T) {
	h, id, selected, original := editableHires(t)
	key := original.Hires[0].Key
	choices := map[string]HireEdit{key: {Name: "Aster", Provider: "claude", Model: "alternate"}}
	before, _ := h.svc.deps.Store.Read(id)
	preview, err := h.svc.PlanWithHires(context.Background(), "u1", id, selected, choices)
	if err != nil || preview.Digest == original.Digest || preview.Hires[0].Name != "Aster" || preview.Hires[0].Key != key || preview.Hires[0].Model != "alternate (claude)" || preview.Hires[0].Job != original.Hires[0].Job {
		t.Fatalf("material preview: %+v %v", preview, err)
	}
	after, _ := h.svc.deps.Store.Read(id)
	if !reflect.DeepEqual(before, after) || len(h.setups.runs) != 0 {
		t.Fatal("preview mutated the report or started setup")
	}
	_, err = h.svc.SetUp(context.Background(), "u1", id, SetUpRequest{PlanDigest: original.Digest, Selected: selected, HireEdits: choices})
	if !errors.Is(err, personalassistant.ErrFolderPlanChanged) {
		t.Fatalf("old consent accepted edited hires: %v", err)
	}
	_, err = h.svc.SetUp(context.Background(), "u1", id, SetUpRequest{PlanDigest: preview.Digest, Selected: selected, HireEdits: choices, HireFaces: map[string]string{key: "unknown-face"}})
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != "agent_character_unknown" {
		t.Fatalf("edited hire accepted invalid face: %v", err)
	}
	after, _ = h.svc.deps.Store.Read(id)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("invalid face persisted material draft")
	}
	face := string(charactercatalog.MustLoad().Working()[0].ID)
	_, err = h.svc.SetUp(context.Background(), "u1", id, SetUpRequest{PlanDigest: preview.Digest, Selected: selected, HireEdits: choices, HireFaces: map[string]string{key: face}})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	stored, _ := h.svc.deps.Store.Read(id)
	if !reflect.DeepEqual(stored.HireEdits, choices) || stored.HireAppearance("Aster").CharacterCatalogID() != face || len(h.setups.runs) != 1 || h.setups.runs[0].Hires[0].Name != "Aster" {
		t.Fatalf("confirmed draft not persisted/passed to run: %+v", stored.HireEdits)
	}
	if got := linesOfKind(h.setups.runs[0], FolderPlanHire); !reflect.DeepEqual(got, []string{"Hires Aster", "Hires Librarian"}) {
		t.Fatalf("execution lines use stale names: %v", got)
	}
	// A stopped run preserves its exact draft even for older retry clients.
	_, _ = h.svc.deps.Store.Update(id, func(r *Record) error { r.Setup.Status = personalassistant.FolderSetupStopped; return nil })
	// Changing Default may move the old choice outside the bounded picker.
	// That must not rewrite or lose an already-confirmed create setting.
	h.svc.deps.Hiring = fakeHiring{models: []HireModel{{Provider: "claude", Model: "new-default", Default: true}}}
	retry, err := h.svc.PlanWithHires(context.Background(), "u1", id, selected, nil)
	if err != nil || retry.Hires[0].Name != "Aster" {
		t.Fatalf("retry lost draft: %+v %v", retry, err)
	}
	changed := map[string]HireEdit{key: {Name: "Another", Provider: "claude", Model: "alternate"}}
	if _, err := h.svc.PlanWithHires(context.Background(), "u1", id, selected, changed); err == nil {
		t.Fatal("partially executed settings could be overwritten")
	}
	_, err = h.svc.SetUp(context.Background(), "u1", id, SetUpRequest{PlanDigest: retry.Digest, Selected: selected})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	if len(h.setups.runs) != 2 || h.setups.runs[1].Hires[0].Name != "Aster" || h.setups.runs[1].Hires[0].ModelID != "alternate" {
		t.Fatal("old-client retry changed material choices")
	}
}

func TestHireEdits_CancelledReviewDoesNotChangeTheDraft(t *testing.T) {
	h, id, selected, original := editableHires(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before, _ := h.svc.deps.Store.Read(id)
	_, err := h.svc.PlanWithHires(ctx, "u1", id, selected, map[string]HireEdit{original.Hires[0].Key: {Name: "Cancelled"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled review continued: %v", err)
	}
	after, _ := h.svc.deps.Store.Read(id)
	if !reflect.DeepEqual(before, after) || len(h.setups.runs) != 0 {
		t.Fatal("cancelled review changed setup")
	}
}

func TestHireEdits_NoModelPreviewCannotAuthorizeSetup(t *testing.T) {
	h, id, selected, original := editableHires(t)
	h.svc.deps.Hiring = fakeHiring{}
	h.setups.setModel("")
	edits := map[string]HireEdit{original.Hires[0].Key: {Name: "Pending"}}
	preview, err := h.svc.PlanWithHires(context.Background(), "u1", id, selected, edits)
	if err != nil || preview.Runnable || preview.Hires[0].Model != NoModelYet {
		t.Fatalf("unfinished model draft: %+v %v", preview, err)
	}
	_, err = h.svc.SetUp(context.Background(), "u1", id, SetUpRequest{PlanDigest: preview.Digest, Selected: selected, HireEdits: edits})
	if !errors.Is(err, ErrNothingToSet) {
		t.Fatalf("unfinished model authorized setup: %v", err)
	}
	stored, _ := h.svc.deps.Store.Read(id)
	if stored.Setup != nil || len(stored.HireEdits) != 0 || len(h.setups.runs) != 0 {
		t.Fatal("unfinished draft mutated setup")
	}
}

func TestHireEdits_InvalidDraftsAreAtomic(t *testing.T) {
	h, id, selected, plan := editableHires(t)
	key := plan.Hires[0].Key
	before, _ := h.svc.deps.Store.Read(id)
	for _, tc := range []struct {
		name, code string
		edits      map[string]HireEdit
	}{
		{"empty", "agent_name_invalid", map[string]HireEdit{key: {Name: ""}}},
		{"path", "agent_name_invalid", map[string]HireEdit{key: {Name: "../agent"}}},
		{"newline", "agent_name_invalid", map[string]HireEdit{key: {Name: "A\nB"}}},
		{"format control", "agent_name_invalid", map[string]HireEdit{key: {Name: "A\u202eB"}}},
		{"long", "agent_name_invalid", map[string]HireEdit{key: {Name: strings.Repeat("a", 61)}}},
		{"unknown model", "agent_model_unknown", map[string]HireEdit{key: {Name: "Aster", Provider: "claude", Model: "unknown"}}},
		{"wrong provider", "agent_model_unknown", map[string]HireEdit{key: {Name: "Aster", Provider: "other", Model: sonnet.Model}}},
		{"unknown identity", "hire_unknown", map[string]HireEdit{"forged-role": {Name: "Aster"}}},
		{"duplicate", "agent_name_taken", map[string]HireEdit{key: {Name: "librarian"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.svc.PlanWithHires(context.Background(), "u1", id, selected, tc.edits)
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Code != tc.code {
				t.Fatalf("invalid edit: %v", err)
			}
			after, _ := h.svc.deps.Store.Read(id)
			if !reflect.DeepEqual(before, after) || len(h.setups.runs) != 0 {
				t.Fatal("invalid edit mutated setup")
			}
		})
	}
	// Recheck name ownership at confirmation, not just when the draft opened.
	edits := map[string]HireEdit{key: {Name: "Aster", Provider: "claude", Model: sonnet.Model}}
	preview, err := h.svc.PlanWithHires(context.Background(), "u1", id, selected, edits)
	if err != nil {
		t.Fatal(err)
	}
	hiring := h.svc.deps.Hiring.(fakeHiring)
	hiring.taken = map[string]bool{"aster": true}
	h.svc.deps.Hiring = hiring
	_, err = h.svc.SetUp(context.Background(), "u1", id, SetUpRequest{PlanDigest: preview.Digest, Selected: selected, HireEdits: edits})
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != "agent_name_taken" {
		t.Fatalf("adopted name holder: %v", err)
	}
}

func TestHireEdits_ExplicitModelDoesNotFollowDefaultAndCatalogIsNotAuthority(t *testing.T) {
	rec := hiresRecord(newHomeFacts(folderlook.HomeHire{RoleID: "manager", Name: "Manager"}), testModel)
	original := homePlan(t, rec)
	rec.HireEdits = map[string]HireEdit{original.Hires[0].Key: {Name: "Aster", Provider: "claude", Model: "alternate"}}
	a := homePlan(t, rec)
	rec.HomeHireModel = "another default (other)"
	rec.HireModels = []HireModel{{Provider: "other", Model: "new", Default: true}}
	b := homePlan(t, rec)
	if a.Digest != b.Digest || !reflect.DeepEqual(withoutHireFaces(a), withoutHireFaces(b)) {
		t.Fatal("new default/picker inventory rewrote explicit material choices")
	}
}
