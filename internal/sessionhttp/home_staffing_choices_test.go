package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/store"
)

func TestHomeStaffingSharedReviewBindsOriginalAdviceAndHasNoEffects(t *testing.T) {
	h, workspaces, id, revision, cleanup := newGroupTemplateHomeHandler(t)
	defer cleanup()
	f := newBuildFixtureOn(t, h, false)
	wireReviewedGroupStaffing(t, h, workspaces)
	if err := h.agentStore.UpdateAgent("Scout", func(profile *agent.Agent) error {
		profile.Settings.Provider, profile.Settings.Model = "scripted", "scripted-1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	input, ok := h.prepareGroupTemplateHome(t.Context(), httptest.NewRecorder(), groupTemplateHomeRequest{GroupTemplateID: id, Revision: revision, Name: "Collection"})
	if !ok {
		t.Fatal("host target unavailable")
	}
	target := groupHomeStaffingTarget(input, h.groupRequirements.Evaluate(input))
	var witness string
	for _, profile := range store.ReadStaffingSnapshot(t.Context(), h.agentStore).Profiles {
		if profile.Name == "Scout" {
			witness = profile.Witness
		}
	}
	advice := &rosterstaffing.Advice{Version: 2, TemplateID: id, TemplateRevision: revision, State: rosterstaffing.Recommended,
		Roles: []rosterstaffing.RoleAdvice{{RoleID: "home-lead", Scope: "home", State: rosterstaffing.Recommended, Choices: []rosterstaffing.AdviceChoice{{
			Handle: "candidate", Name: "Scout", Source: "legacy", Configured: true, SourceWitness: witness,
			Reasons: []rosterstaffing.Evidence{{ID: "purpose", Kind: "declared_purpose", Value: "Manage the collection"}},
		}}}}}
	choices := []rosterstaffing.RoleChoice{{RoleID: "home-lead", Mode: "assign", Name: "Scout", Recommendation: "candidate"}}
	before, err := json.Marshal(store.LibraryDefinition(h.agentStore, "Scout").Definition)
	if err != nil {
		t.Fatal(err)
	}
	count := len(h.agentStore.ListAgents())
	team, err := h.ReviewHomeStaffingChoices(t.Context(), target, choices, advice)
	if err != nil || len(team.Roles) != 1 || team.Roles[0].SourceWitness != witness {
		t.Fatal("original explicit source not retained", err)
	}
	for name, change := range map[string]func(*rosterstaffing.Advice){
		"template": func(a *rosterstaffing.Advice) { a.TemplateID = "foreign" },
		"revision": func(a *rosterstaffing.Advice) { a.TemplateRevision = "foreign" },
		"source":   func(a *rosterstaffing.Advice) { a.Roles[0].Choices[0].SourceWitness = "foreign" },
		"legacy":   func(a *rosterstaffing.Advice) { a.Version = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			other := advice.Clone()
			change(other)
			if _, err := h.ReviewHomeStaffingChoices(t.Context(), target, choices, other); err == nil {
				t.Fatal("foreign advice gained authority")
			}
		})
	}
	choices[0].Recommendation = ""
	if _, err := h.ReviewHomeStaffingChoices(t.Context(), target, choices, nil); err != nil {
		t.Fatal("manual assignment requires recommender", err)
	}
	data, err := json.Marshal(target)
	if err != nil || string(data) != "{}" {
		t.Fatal("host-only declaration/ownership escaped projection", err)
	}
	after, err := json.Marshal(store.LibraryDefinition(h.agentStore, "Scout").Definition)
	ids, listErr := workspaces.List()
	if err != nil || listErr != nil || !bytes.Equal(before, after) || len(ids) != 0 || len(h.agentStore.ListAgents()) != count || f.provider.calls != 0 {
		t.Fatal("draft review inferred or performed effects")
	}
}
