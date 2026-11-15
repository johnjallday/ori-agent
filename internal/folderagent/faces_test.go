package folderagent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

type faceSetups struct {
	*fakeSetups
	characters []string
}

func (s faceSetups) HomeHireCharacters(count int) []string {
	return s.characters[:min(count, len(s.characters))]
}

func TestPlan_HireCharactersArePresentationOnly(t *testing.T) {
	rec := hiresRecord(newHomeFacts(folderlook.HomeHire{Name: "Manager"}, folderlook.HomeHire{Name: "Assistant"}), testModel)
	plain := homePlan(t, rec)
	rec.HomeHireCharacters = []string{"analyst", "builder"}
	shown := homePlan(t, rec)
	if shown.Hires[0].Character != "analyst" || shown.Hires[1].Character != "builder" || shown.Digest != plain.Digest {
		t.Fatalf("suggestions changed the plan: %+v", shown.Hires)
	}
	if !reflect.DeepEqual(withoutHireFaces(plain), withoutHireFaces(shown)) {
		t.Fatal("faces changed execution equivalence")
	}
	rec.HireFaces = map[string]*types.AgentAppearance{"Manager": types.NewAgentAppearance()}
	if got := homePlan(t, rec); got.Hires[0].Character != "" || got.Hires[1].Character != "builder" || got.Digest != plain.Digest {
		t.Fatalf("explicit generated face did not override suggestion: %+v", got.Hires)
	}
}

func TestSetUp_HireFacesValidateBeforeStartingAndSurviveRetry(t *testing.T) {
	h := newHarness(t, demo(t))
	working := charactercatalog.MustLoad().Working()
	first, second := string(working[0].ID), string(working[1].ID)
	h.svc.deps.Setups = faceSetups{h.setups, []string{first, second}}
	view := h.start(t, "")
	if _, err := h.svc.AnswerPremise(context.Background(), "u1", view.WorkspaceID, PremiseConfirmed); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.deps.Store.Update(view.WorkspaceID, func(r *Record) error {
		for i := range r.Candidates {
			if r.Candidates[i].Kind == folderlook.KindHomeProvider {
				r.Candidates[i].Home = newHomeFacts(folderlook.HomeHire{Name: "Manager"}, folderlook.HomeHire{Name: "Assistant"})
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
	if err != nil || len(plan.Hires) != 2 || plan.Hires[0].Character != first || plan.Hires[1].Character != second {
		t.Fatalf("projected suggestions: %+v %v", plan.Hires, err)
	}
	for _, bad := range []string{"unknown-face", string(charactercatalog.MustLoad().ReservedGuideID)} {
		_, err = h.svc.SetUp(context.Background(), "u1", view.WorkspaceID, SetUpRequest{PlanDigest: plan.Digest, Selected: selected,
			HireFaces: map[string]string{"Manager": bad}})
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != "agent_character_unknown" {
			t.Fatalf("invalid face: %v", err)
		}
		stored, _ := h.svc.deps.Store.Read(view.WorkspaceID)
		if stored.State != StateReported || stored.Setup != nil || len(stored.HireFaces) != 0 || len(h.setups.runs) != 0 {
			t.Fatal("invalid face started or mutated the run")
		}
	}
	chosen := map[string]string{"Manager": second, "Assistant": "", "Not hired": "unknown-face"}
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID,
		SetUpRequest{PlanDigest: plan.Digest, Selected: selected, HireFaces: chosen}); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	stored, _ := h.svc.deps.Store.Read(view.WorkspaceID)
	if len(stored.HireFaces) != 2 || stored.HireAppearance("Manager").CharacterCatalogID() != second ||
		stored.HireAppearance("Assistant").Mode != types.AppearanceModeGenerated {
		t.Fatalf("saved choices: %+v", stored.HireFaces)
	}
	if len(h.setups.runs) != 1 || h.setups.runs[0].Hires[0].Character != second || h.setups.runs[0].Hires[1].Character != "" {
		t.Fatalf("confirmed choices did not reach run: %+v", h.setups.runs)
	}
	// A changed suggestion must not invalidate execution checks.
	h.svc.deps.Setups = faceSetups{h.setups, []string{second, first}}
	if err := h.svc.CheckPlan(view.WorkspaceID, h.setups.runs[0]); err != nil {
		t.Fatalf("face-only change invalidated run: %v", err)
	}
	_, _ = h.svc.deps.Store.Update(view.WorkspaceID, func(r *Record) error {
		r.Setup.Status = personalassistant.FolderSetupStopped
		return nil
	})
	retry, err := h.svc.Plan("u1", view.WorkspaceID, selected)
	if err != nil || retry.Hires[0].Character != second || retry.Hires[1].Character != "" {
		t.Fatalf("retry changed the shown faces: %+v %v", retry.Hires, err)
	}
	if _, err := h.svc.SetUp(context.Background(), "u1", view.WorkspaceID,
		SetUpRequest{PlanDigest: retry.Digest, Selected: selected}); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	if h.setups.runs[1].Hires[0].Character != second || h.setups.runs[1].Hires[1].Character != "" {
		t.Fatal("old-client retry lost the confirmed choice")
	}
	if _, err := h.svc.LookFurther(context.Background(), "u1", view.WorkspaceID, nil, ""); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	stored, _ = h.svc.deps.Store.Read(view.WorkspaceID)
	if len(stored.HireFaces) != 0 {
		t.Fatal("new look retained an obsolete confirmation")
	}
}

func TestConfirmedHireFaces_BoundedAndLegacyGenerated(t *testing.T) {
	chosen := map[string]string{}
	for i := 0; i <= maxReceiptItems; i++ {
		chosen[string(rune('a'+i))] = ""
	}
	if _, err := confirmedHireFaces(Record{}, nil, chosen); !errors.Is(err, personalassistant.ErrValidation) {
		t.Fatalf("unbounded faces accepted: %v", err)
	}
	faces, err := confirmedHireFaces(Record{}, []HireEntry{{Name: "Manager"}}, nil)
	if err != nil || len(faces) != 1 || faces["Manager"] != nil {
		t.Fatalf("legacy confirmation changed default: %+v %v", faces, err)
	}
}
