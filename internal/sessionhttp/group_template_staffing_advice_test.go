package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

type homeStaffingTestProvider struct {
	*scriptedProvider
	request llm.ChatRequest
}

func (p *homeStaffingTestProvider) Chat(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	p.request = request
	return p.scriptedProvider.Chat(ctx, request)
}

func TestGroupTemplateHomeStaffingRecommendationIsExplicitPrivateAndReadOnly(t *testing.T) {
	h, workspaces, id, revision, cleanup := newGroupTemplateHomeHandler(t)
	defer cleanup()
	f := newBuildFixtureOn(t, h, false)
	if err := h.agentStore.UpdateAgent("Scout", func(profile *agent.Agent) error {
		profile.Settings.Provider, profile.Settings.Model = "scripted", "scripted-1"
		profile.Settings.SystemPrompt = "PRIVATE_HOME_PROMPT_CANARY"
		profile.Metadata = &types.AgentMetadata{Description: "Coordinates a portfolio of music projects."}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var candidate rosterstaffing.Candidate
	for _, item := range h.workspaceBuildRoster(t.Context(), "local").Candidates {
		if item.Name == "Scout" {
			candidate = item
		}
	}
	if candidate.Handle == "" {
		t.Fatal("missing fixture candidate")
	}
	proposal := rosterstaffing.Proposal{Roles: []rosterstaffing.RoleProposal{
		{RoleID: "home-lead", State: rosterstaffing.Recommended, Choices: []rosterstaffing.Choice{{Candidate: candidate.Handle, Evidence: []string{candidate.Evidence[0].ID}}}},
		{RoleID: "home-addon", State: rosterstaffing.NeedsChoice},
	}}
	encoded, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	f.provider.replies = []string{string(encoded)}
	provider := &homeStaffingTestProvider{scriptedProvider: f.provider}
	h.workspaceBuild.ResolveModel = func(context.Context, string) (WorkspaceBuildModel, error) {
		return WorkspaceBuildModel{Provider: provider, Model: "scripted-1"}, nil
	}
	before, err := json.Marshal(store.LibraryDefinition(h.agentStore, "Scout").Definition)
	if err != nil {
		t.Fatal(err)
	}
	count := len(h.agentStore.ListAgents())
	selection := map[string]any{"group_template_id": id, "revision": revision, "name": "Collection"}
	if code, _ := postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, selection)); code != http.StatusOK || provider.calls != 0 {
		t.Fatal("ordinary Home review invoked ranking", code)
	}
	selection["recommend_staffing"] = true
	code, body := postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, selection))
	review, _ := body["group_template_review"].(map[string]any)
	data, err := json.Marshal(review["staffing_advice"])
	if err != nil {
		t.Fatal(err)
	}
	var advice rosterstaffing.Advice
	if code != http.StatusOK || json.Unmarshal(data, &advice) != nil || len(advice.Roles) != 2 || advice.Roles[0].Choices[0].Name != "Scout" {
		t.Fatal("missing Home advice", code)
	}
	if provider.calls != 1 || !provider.request.Isolated || len(provider.request.Tools) != 0 || provider.request.MaxTokens > 2000 {
		t.Fatal("ranking was repeated or not isolated")
	}
	if strings.Contains(string(data), "witness") || strings.Contains(string(data), "PRIVATE_HOME_PROMPT_CANARY") || strings.Contains(provider.request.SystemPrompt, "PRIVATE_HOME_PROMPT_CANARY") || strings.Contains(provider.request.SystemPrompt, candidate.Witness) {
		t.Fatal("private source material escaped ranking boundary")
	}
	getGroupTemplates(t, h)
	delete(selection, "recommend_staffing")
	if code, _ := postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, selection)); code != http.StatusOK || provider.calls != 1 {
		t.Fatal("read/review re-ranked")
	}
	after, err := json.Marshal(store.LibraryDefinition(h.agentStore, "Scout").Definition)
	if err != nil || !bytes.Equal(before, after) || len(h.agentStore.ListAgents()) != count {
		t.Fatal("recommendation changed saved profiles", err)
	}
	if ids, _ := workspaces.List(); len(ids) != 0 {
		t.Fatal("recommendation created a Home")
	}
	wireReviewedGroupStaffing(t, h, workspaces)
	selection["role_staffing"] = []groupHomeRoleChoice{{RoleID: "home-lead", Mode: "assign", Name: "Scout", Recommendation: advice.Roles[0].Choices[0].Handle}}
	selection["staffing_advice_token"] = review["review_token"]
	code, body = postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, selection))
	selected, _ := body["group_template_review"].(map[string]any)
	if code != http.StatusOK || selected["role_staffing"] == nil {
		t.Fatal("recommended choice did not enter exact team review", code)
	}
	selection["group_review_token"], selection["idempotency_key"] = selected["review_token"], "selected-recommendation"
	code, body = postGroupTemplateHome(t, h, true, groupTemplateHomeBody(t, selection))
	result, _ := body["group_template"].(map[string]any)
	outcome, _ := result["home_staffing"].(map[string]any)
	if code != http.StatusOK || outcome["state"] != "staffed" || provider.calls != 1 || len(h.agentStore.ListAgents()) != count {
		t.Fatal("recommended reuse was reranked, changed or recreated", code)
	}
}

func TestGroupTemplateHomeStaffingUnavailableKeepsManualReviewAndCannotRankOnCommit(t *testing.T) {
	h, workspaces, id, revision, cleanup := newGroupTemplateHomeHandler(t)
	defer cleanup()
	request := map[string]any{"group_template_id": id, "revision": revision, "name": "Collection", "recommend_staffing": true}
	code, body := postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, request))
	review, _ := body["group_template_review"].(map[string]any)
	advice, _ := review["staffing_advice"].(map[string]any)
	if code != http.StatusOK || advice["state"] != rosterstaffing.Unavailable {
		t.Fatal("unavailable ranker blocked manual review", code)
	}
	request["group_review_token"], request["idempotency_key"] = review["review_token"], "not-an-inference-action"
	if code, _ := postGroupTemplateHome(t, h, true, groupTemplateHomeBody(t, request)); code != http.StatusBadRequest {
		t.Fatal("commit accepted inference action")
	}
	if ids, _ := workspaces.List(); len(ids) != 0 {
		t.Fatal("invalid commit caused effects")
	}
}
