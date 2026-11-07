package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func wireReviewedGroupStaffing(t *testing.T, h *Handler, workspaces workspace.Store) *setupjourney.AssistantStaffingAdapter {
	t.Helper()
	return wireReviewedGroupStaffingWithGrants(t, h, workspaces, nil)
}

func wireReviewedGroupStaffingWithGrants(t *testing.T, h *Handler, workspaces workspace.Store, grants setupjourney.StaffingToolGrants) *setupjourney.AssistantStaffingAdapter {
	t.Helper()
	adapter := setupjourney.NewAssistantStaffingAdapter(workspaces, h.agentStore, grants,
		func() (string, string) { t.Error("reviewed role resolved an undisclosed default"); return "", "" },
		func(provider, model string) error {
			if provider != "scripted" || model != "scripted-1" {
				return setupjourney.ErrInvalid
			}
			return nil
		})
	h.SetAssistantWorkspaceRoleStaffer(func(ctx context.Context, id string, fills []RoleStaffingFill) error {
		roles := make([]setupjourney.RoleFill, 0, len(fills))
		for _, fill := range fills {
			mode := fill.Mode
			if mode == "assign" {
				mode = setupjourney.StaffingModeBind
			}
			roles = append(roles, setupjourney.RoleFill{RoleID: fill.RoleID, Mode: mode, Name: fill.Name, Provider: fill.Provider, Model: fill.Model,
				ReasoningEffort: fill.ReasoningEffort, Appearance: fill.Appearance, SourceWitness: fill.SourceWitness, CapabilityWitness: fill.CapabilityWitness,
				ChoiceDigest: fill.ChoiceDigest, PreviousChoiceDigest: fill.PreviousChoiceDigest})
		}
		return adapter.StaffRoleOnWorkspace(ctx, id, roles)
	})
	return adapter
}

func TestGroupTemplateHomeReviewedTeamCommitsExactEffectsAndReplaysAfterProfileRestart(t *testing.T) {
	for _, mode := range []string{"assign", "create"} {
		t.Run(mode, func(t *testing.T) {
			h, workspaces, id, revision, cleanup := newGroupTemplateHomeHandler(t)
			defer cleanup()
			index := filepath.Join(t.TempDir(), "agents.json")
			profiles, err := store.NewFileStore(index, types.Settings{})
			if err != nil {
				t.Fatal(err)
			}
			h.SetAgentStore(profiles)
			f := newBuildFixtureOn(t, h, false)
			if err := profiles.UpdateAgent("Scout", func(profile *agent.Agent) error {
				profile.Settings.Provider, profile.Settings.Model, profile.Settings.SystemPrompt = "scripted", "scripted-1", "AUTHORED_PROFILE_CANARY"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			wireReviewedGroupStaffing(t, h, workspaces)
			choice := groupHomeRoleChoice{RoleID: "home-lead", Mode: mode, Name: "Scout"}
			if mode == "create" {
				choice.Name, choice.Provider, choice.Model = "New Guide", "scripted", "scripted-1"
				choice.Appearance = &types.AgentAppearance{Mode: types.AppearanceModeGenerated}
			}
			request := map[string]any{"group_template_id": id, "revision": revision, "name": "Collection", "role_staffing": []groupHomeRoleChoice{choice}}
			before, err := json.Marshal(store.LibraryDefinition(profiles, "Scout").Definition)
			if err != nil {
				t.Fatal(err)
			}
			count := len(profiles.ListAgents())
			code, body := postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, request))
			review, _ := body["group_template_review"].(map[string]any)
			if code != http.StatusOK || review["role_staffing"] == nil {
				t.Fatal("exact team review unavailable", code, body)
			}
			if ids, _ := workspaces.List(); len(ids) != 0 || len(profiles.ListAgents()) != count {
				t.Fatal("review performed effects")
			}
			request["group_review_token"], request["idempotency_key"] = review["review_token"], "exact-team"
			code, body = postGroupTemplateHome(t, h, true, groupTemplateHomeBody(t, request))
			result, _ := body["group_template"].(map[string]any)
			outcome, _ := result["home_staffing"].(map[string]any)
			if code != http.StatusOK || outcome["state"] != "staffed" {
				t.Fatal("canonical team commit failed", code, body)
			}
			homeID, _ := result["home_workspace_id"].(string)
			home, err := workspaces.Get(homeID)
			if err != nil || len(home.GetAgentInstances()) != 1 || home.GetAgentInstances()[0].Name != choice.Name {
				t.Fatal("wrong binding", err)
			}
			expectedCount := count
			if mode == "create" {
				expectedCount++
			}
			if len(profiles.ListAgents()) != expectedCount || f.provider.calls != 0 {
				t.Fatal("commit created extra profiles or ranked")
			}
			after, err := json.Marshal(store.LibraryDefinition(profiles, "Scout").Definition)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("saved authored profile changed", err)
			}
			restarted, err := store.NewFileStore(index, types.Settings{})
			if err != nil {
				t.Fatal(err)
			}
			h.SetAgentStore(restarted)
			adapter := wireReviewedGroupStaffing(t, h, workspaces)
			code, body = postGroupTemplateHome(t, h, true, groupTemplateHomeBody(t, request))
			result, _ = body["group_template"].(map[string]any)
			outcome, _ = result["home_staffing"].(map[string]any)
			if code != http.StatusOK || outcome["state"] != "staffed" || len(restarted.ListAgents()) != expectedCount {
				t.Fatal("completed outcome did not replay", code, body)
			}
			if err := adapter.UnstaffRoleFromWorkspace(t.Context(), homeID, choice.RoleID); err != nil {
				t.Fatal(err)
			}
			code, body = postGroupTemplateHome(t, h, true, groupTemplateHomeBody(t, request))
			result, _ = body["group_template"].(map[string]any)
			outcome, _ = result["home_staffing"].(map[string]any)
			home, _ = workspaces.Get(homeID)
			if code != http.StatusOK || outcome["state"] != "needs_review" || len(home.GetAgentInstances()) != 0 || len(restarted.ListAgents()) != expectedCount {
				t.Fatal("old review refilled cleared role", code, body)
			}
		})
	}
}

func TestGroupTemplateHomeReviewedTeamRejectsChangedSourceAndOmittedChoicesBeforeHomeCreation(t *testing.T) {
	for _, scenario := range []string{"source_changed", "omitted", "substituted", "home_created_elsewhere", "wrong_scope", "assign_configuration"} {
		t.Run(scenario, func(t *testing.T) {
			h, workspaces, id, revision, cleanup := newGroupTemplateHomeHandler(t)
			defer cleanup()
			newBuildFixtureOn(t, h, false)
			if err := h.agentStore.UpdateAgent("Scout", func(profile *agent.Agent) error {
				profile.Settings.Provider, profile.Settings.Model = "scripted", "scripted-1"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			wireReviewedGroupStaffing(t, h, workspaces)
			choice := groupHomeRoleChoice{RoleID: "home-lead", Mode: "assign", Name: "Scout"}
			if scenario == "wrong_scope" {
				choice.RoleID = "project-lead"
			}
			if scenario == "assign_configuration" {
				choice.Model = "override"
			}
			request := map[string]any{"group_template_id": id, "revision": revision, "name": "Collection", "role_staffing": []groupHomeRoleChoice{choice}}
			code, body := postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, request))
			if scenario != "wrong_scope" && scenario != "assign_configuration" {
				if code != http.StatusOK {
					t.Fatal("valid review refused", code)
				}
				review := body["group_template_review"].(map[string]any)
				request["group_review_token"], request["idempotency_key"] = review["review_token"], "stale-choice"
				switch scenario {
				case "source_changed":
					if err := h.agentStore.UpdateAgent("Scout", func(profile *agent.Agent) error { profile.Settings.SystemPrompt = "Changed"; return nil }); err != nil {
						t.Fatal(err)
					}
				case "home_created_elsewhere":
					other := map[string]any{"group_template_id": id, "revision": revision, "name": "Another collection"}
					status, result := postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, other))
					if status != http.StatusOK {
						t.Fatal("other Home review failed", status)
					}
					other["group_review_token"], other["idempotency_key"] = result["group_template_review"].(map[string]any)["review_token"], "other-home-operation"
					if status, _ := postGroupTemplateHome(t, h, true, groupTemplateHomeBody(t, other)); status != http.StatusOK {
						t.Fatal("other Home commit failed", status)
					}
				case "omitted":
					delete(request, "role_staffing")
				case "substituted":
					choice.Name = "Luna"
					request["role_staffing"] = []groupHomeRoleChoice{choice}
				}
				code, _ = postGroupTemplateHome(t, h, true, groupTemplateHomeBody(t, request))
			}
			if code != http.StatusConflict {
				t.Fatal("unsafe choice accepted", scenario, code)
			}
			ids, _ := workspaces.List()
			wantHomes := 0
			if scenario == "home_created_elsewhere" {
				wantHomes = 1
			}
			if len(ids) != wantHomes {
				t.Fatal("refused choice created a Home")
			}
			for _, id := range ids {
				home, _ := workspaces.Get(id)
				if len(home.GetAgentInstances()) != 0 {
					t.Fatal("review silently staffed another operation's Home")
				}
			}
		})
	}
}
