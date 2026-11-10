package folderagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type fakeHiring struct {
	models []HireModel
	agents []HireAgent
	taken  map[string]bool
}

func (f fakeHiring) Models(context.Context) []HireModel         { return f.models }
func (f fakeHiring) Agents(context.Context, string) []HireAgent { return f.agents }
func (f fakeHiring) AgentExists(name string) bool               { return f.taken[strings.ToLower(name)] }

func hiringHarness(t *testing.T, hiring fakeHiring) *harness {
	t.Helper()
	h := newHarness(t, demo(t))
	h.svc.deps.Hiring = hiring
	return h
}

var sonnet = HireModel{Provider: "claude", Model: "claude-sonnet-5-5", Label: "claude-sonnet-5-5 (claude)", Default: true}

func TestHire_CardOffersAgentModelsAndRoster(t *testing.T) {
	h := hiringHarness(t, fakeHiring{
		models: []HireModel{sonnet, {Provider: "openai", Model: "gpt-x", Label: "gpt-x (openai)"}},
		agents: []HireAgent{{Name: "Atlas", Model: "claude-sonnet-5-5"}},
	})
	chosen, err := h.svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true})
	if err != nil {
		t.Fatal(err)
	}
	hire := chosen.Hire
	if chosen.Action != "Hire & start" || hire == nil || hire.AgentName != "Ori Music Demo Manager" ||
		hire.Model != "claude-sonnet-5-5" || hire.Provider != "claude" || len(hire.Models) != 2 ||
		len(hire.Agents) != 1 || hire.Existing != "" || hire.NameMax != AgentNameMaxRunes {
		t.Fatalf("card hire = %+v", hire)
	}
	// Nothing is created by choosing.
	if len(h.ws.created) != 0 {
		t.Fatalf("created on choose: %+v", h.ws.created)
	}
}

func TestHire_SavedManagerIsPreselectedAndReused(t *testing.T) {
	h := hiringHarness(t, fakeHiring{models: []HireModel{sonnet},
		agents: []HireAgent{{Name: "Ori Music Demo Manager"}}, taken: map[string]bool{"ori music demo manager": true}})
	chosen, _ := h.svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true})
	if chosen.Hire.Existing != "Ori Music Demo Manager" {
		t.Fatalf("existing = %q", chosen.Hire.Existing)
	}
	if _, err := h.svc.Start(context.Background(), "u1", chosen.Token, "", Hire{Existing: "ori music demo manager"}); err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	req := h.ws.created[0]
	if !req.BindExisting || req.AgentName != "Ori Music Demo Manager" || req.Model != "" {
		t.Fatalf("create = %+v", req)
	}
}

func TestHire_NewAgentCarriesNameModelAndMandate(t *testing.T) {
	h := hiringHarness(t, fakeHiring{models: []HireModel{sonnet, {Provider: "openai", Model: "gpt-x"}}, agents: []HireAgent{}})
	chosen, _ := h.svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true})
	view, err := h.svc.Start(context.Background(), "u1", chosen.Token, "List the songs",
		Hire{Name: "  Song   Keeper ", Provider: "openai", Model: "gpt-x", Mandate: "Keep my songs moving"})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Wait()
	req := h.ws.created[0]
	if req.BindExisting || req.AgentName != "Song Keeper" || req.Provider != "openai" || req.Model != "gpt-x" ||
		req.Mandate != "Keep my songs moving" || req.Mission != "" || view.AgentName != "Song Keeper" {
		t.Fatalf("create = %+v view agent %q", req, view.AgentName)
	}
	// No mandate: the brief is the mandate; no model sent: "No model yet".
	h2 := hiringHarness(t, fakeHiring{models: []HireModel{sonnet}, agents: []HireAgent{}})
	c2, _ := h2.svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true})
	if _, err := h2.svc.Start(context.Background(), "u1", c2.Token, "", Hire{}); err != nil {
		t.Fatal(err)
	}
	h2.svc.Wait()
	if r := h2.ws.created[0]; r.Mandate != DefaultBrief || r.Model != "" || r.Provider != "" {
		t.Fatalf("defaults = %+v", r)
	}
}

func TestHire_WithoutASystemModelNoModelIsPreselected(t *testing.T) {
	h := hiringHarness(t, fakeHiring{models: []HireModel{{Provider: "claude_code", Model: "sonnet", Label: "sonnet (claude_code)"}}})
	chosen, _ := h.svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true})
	if chosen.Hire.Model != "" || chosen.Hire.Provider != "" || len(chosen.Hire.Models) != 1 {
		t.Fatalf("hire = %+v", chosen.Hire)
	}
}

func TestHire_RefusalsCreateNothingAndKeepTheChoice(t *testing.T) {
	h := hiringHarness(t, fakeHiring{models: []HireModel{sonnet}, agents: []HireAgent{{Name: "Atlas"}},
		taken: map[string]bool{"atlas": true}})
	chosen, _ := h.svc.Choose(context.Background(), "u1", personalassistant.FolderChoice{Picker: true})
	for _, tc := range []struct {
		hire Hire
		code string
	}{
		{Hire{Name: "Atlas"}, "agent_name_taken"},
		{Hire{Name: "a/b"}, "agent_name_invalid"},
		{Hire{Name: strings.Repeat("n", AgentNameMaxRunes+1)}, "agent_name_invalid"},
		{Hire{Model: "made-up"}, "agent_model_unknown"},
		{Hire{Existing: "Someone Else"}, "agent_unknown"},
	} {
		_, err := h.svc.Start(context.Background(), "u1", chosen.Token, "", tc.hire)
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != tc.code {
			t.Fatalf("%+v: err = %v", tc.hire, err)
		}
	}
	if len(h.ws.created) != 0 {
		t.Fatalf("a refusal created %+v", h.ws.created)
	}
	// The choice is still held: a valid hire goes through.
	if _, err := h.svc.Start(context.Background(), "u1", chosen.Token, "", Hire{Existing: "Atlas"}); err != nil {
		t.Fatalf("after refusals: %v", err)
	}
	// That Start began a look; let it finish before the temp folder goes.
	h.svc.Wait()
}
