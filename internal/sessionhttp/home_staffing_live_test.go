package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Explicit opt-in: one paid text-only call using current skill-bearing Home
// prompting, then real review/commit/binding. All profiles, Home declarations and
// membership are synthetic in disposable HOME; no released plugin is asserted.
func TestHomeStaffingLiveClaudeUsesDeclaredPurposeAndEnabledMembershipThenBindsExactly(t *testing.T) {
	if os.Getenv("ORI_LIVE_HOME_STAFFING") != "1" {
		t.Skip("opt-in live Claude Home staffing acceptance")
	}
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Fatal("requires ANTHROPIC_API_KEY through with-keys")
	}
	model := os.Getenv("ORI_LIVE_BUILD_MODEL")
	if model == "" {
		model = "claude-sonnet-5-5"
	}
	t.Setenv("HOME", t.TempDir())
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	template.AssistantProgram.Roles[0].Description = "Coordinate music portfolio priorities, release milestones, blockers and explicit project handoffs without inheriting project access."
	template.AssistantProgram.Roles[0].Skills = []string{"music-coordination"}
	h, workspaces, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	index := filepath.Join(t.TempDir(), "agents.json")
	profiles, err := store.NewFileStore(index, types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	h.SetAgentStore(profiles)
	newBuildFixtureOn(t, h, false)
	if err := profiles.CreateAgent("Portfolio Manager", &store.CreateAgentConfig{}); err != nil {
		t.Fatal(err)
	}
	for name, purpose := range map[string]string{
		"Scout":             "Coordinates music portfolio priorities, release milestones, blockers and explicit project handoffs. Does not control audio software.",
		"Portfolio Manager": "Records physical warehouse inventory, invoices and stocktake dates. Does not coordinate music projects.",
	} {
		if err := profiles.UpdateAgent(name, func(profile *agent.Agent) error {
			profile.Settings.Provider, profile.Settings.Model = "scripted", "scripted-1"
			profile.Settings.SystemPrompt = "PRIVATE_HOME_LIVE_CANARY"
			profile.Metadata = &types.AgentMetadata{Description: purpose}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	manager := skills.NewManager(skills.ManagerConfig{AgentStorePath: index})
	manager.SetAgentFolderResolver(func(name string) (string, bool) {
		return filepath.Join(filepath.Dir(index), "agents", name), true
	})
	if err := manager.SetSkillEnabled("Scout", "music-coordination", true); err != nil {
		t.Fatal(err)
	}
	grants := liveHomeMembership{manager: manager}
	h.SetStaffingSkillReader(func(ctx context.Context, selected store.StaffingSelection, names []string, _ bool) rosterstaffing.SkillMembership {
		inspection, err := manager.PrepareProfileSkillInspection(selected.Name)
		if err != nil {
			return rosterstaffing.SkillMembership{}
		}
		var out rosterstaffing.SkillMembership
		err = store.WithStaffingProfiles(ctx, profiles, []store.StaffingSelection{selected}, func(map[string]*agent.Agent) error {
			snapshot, err := inspection.Read()
			if err != nil {
				return err
			}
			out = rosterstaffing.SkillMembership{SourceWitness: selected.Witness, Witness: snapshot.Witness, Complete: true}
			for _, name := range names {
				if !grants.Available(name) {
					out.Complete = false
				} else if snapshot.Enabled(name) {
					out.Enabled = append(out.Enabled, name)
				}
			}
			return nil
		})
		if err != nil {
			return rosterstaffing.SkillMembership{}
		}
		return out
	})
	wireReviewedGroupStaffingWithGrants(t, h, workspaces, grants)
	h.SetGroupTemplateCatalog(func(string) ([]GroupTemplateCatalogEntry, bool, error) {
		return []GroupTemplateCatalogEntry{{Template: template, Readiness: h.revalidateBlueprintReadiness(template), Active: true}}, false, nil
	})
	entry := getGroupTemplates(t, h).GroupTemplates[1]
	provider := &oneCallStaffingProvider{Provider: llm.NewClaudeProvider(llm.ClaudeProviderConfig(key, os.Getenv("ANTHROPIC_WORKSPACE_ID")))}
	h.workspaceBuild.ResolveModel = func(context.Context, string) (WorkspaceBuildModel, error) {
		return WorkspaceBuildModel{Provider: provider, ProviderName: "anthropic", Model: model}, nil
	}
	before, err := json.Marshal(store.LibraryDefinition(profiles, "Scout").Definition)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := manager.PrepareProfileSkillInspection("Scout")
	if err != nil {
		t.Fatal(err)
	}
	skillBefore, err := inspection.Read()
	if err != nil {
		t.Fatal(err)
	}
	count := len(profiles.ListAgents())
	request := map[string]any{"group_template_id": entry.ID, "revision": entry.Revision, "name": "Synthetic live Music Home", "recommend_staffing": true}
	code, body := postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, request))
	review, _ := body["group_template_review"].(map[string]any)
	data, err := json.Marshal(review["staffing_advice"])
	var advice rosterstaffing.Advice
	if err != nil || json.Unmarshal(data, &advice) != nil || code != http.StatusOK || provider.calls != 1 {
		t.Fatalf("live Home recommendation refused: http=%d calls=%d (provider output omitted)", code, provider.calls)
	}
	var selected rosterstaffing.AdviceChoice
	for _, role := range advice.Roles {
		if role.RoleID == "home-lead" && len(role.Choices) > 0 {
			selected = role.Choices[0]
		}
	}
	if advice.Version != 2 || selected.Name != "Scout" || selected.Handle == "" || len(selected.Reasons) == 0 {
		t.Fatalf("live Home returned no evidence-backed unrelated-name fit: state=%s (output omitted)", advice.State)
	}
	ids, err := workspaces.List()
	if err != nil || len(ids) != 0 || len(profiles.ListAgents()) != count || bytes.Contains(data, []byte("PRIVATE_HOME_LIVE_CANARY")) || bytes.Contains(data, []byte("witness")) {
		t.Fatal("recommendation exposed private material or performed effects", err)
	}
	delete(request, "recommend_staffing")
	request["staffing_advice_token"] = review["review_token"]
	request["role_staffing"] = []groupHomeRoleChoice{{RoleID: "home-lead", Mode: "assign", Name: selected.Name, Recommendation: selected.Handle}}
	code, body = postGroupTemplateHome(t, h, false, groupTemplateHomeBody(t, request))
	if code != http.StatusOK {
		t.Fatalf("exact live-result review refused: http=%d", code)
	}
	request["group_review_token"], request["idempotency_key"] = body["group_template_review"].(map[string]any)["review_token"], "live-exact-home-choice"
	code, body = postGroupTemplateHome(t, h, true, groupTemplateHomeBody(t, request))
	result, _ := body["group_template"].(map[string]any)
	outcome, _ := result["home_staffing"].(map[string]any)
	if code != http.StatusOK || outcome["state"] != "staffed" || provider.calls != 1 {
		t.Fatalf("exact live-result commit refused: http=%d calls=%d", code, provider.calls)
	}
	homeID, _ := result["home_workspace_id"].(string)
	home, err := workspaces.Get(homeID)
	if err != nil || len(home.AgentInstances) != 1 || home.AgentInstances[0].Name != "Scout" || home.AgentInstances[0].RoleSource != "assigned" {
		t.Fatal("live result did not produce exact observed binding", err)
	}
	after, err := json.Marshal(store.LibraryDefinition(profiles, "Scout").Definition)
	skillAfter, skillErr := inspection.Read()
	if err != nil || skillErr != nil || !bytes.Equal(before, after) || skillBefore.Witness != skillAfter.Witness || len(profiles.ListAgents()) != count {
		t.Fatal("live-result reuse changed the profile or skill membership")
	}
	t.Logf("provider=anthropic model=%s calls=1 prompt=Home-v2-with-required-skills recommended=Scout action=assign observed_binding=true profiles=%d->%d profile_and_membership_unchanged=true", model, count, len(profiles.ListAgents()))
}

type liveHomeMembership struct{ manager *skills.Manager }

func (g liveHomeMembership) Available(name string) bool { return name == "music-coordination" }
func (g liveHomeMembership) Grant(string, string) error {
	return errors.New("reuse must never grant skills")
}
func (g liveHomeMembership) Revoke(string, string) error {
	return errors.New("reuse must never revoke skills")
}
func (g liveHomeMembership) PrepareStaffingCapabilities(name string, required []string, _ bool, witness string) (setupjourney.StaffingCapabilityGuard, error) {
	inspection, err := g.manager.PrepareProfileSkillInspection(name)
	if err != nil {
		return nil, err
	}
	current, err := inspection.Read()
	if err != nil || current.Witness != witness {
		return nil, setupjourney.ErrConflict
	}
	for _, skill := range required {
		if !g.Available(skill) || !current.Enabled(skill) {
			return nil, setupjourney.ErrConflict
		}
	}
	return func(apply func() error) error { return inspection.WithUnchanged(witness, apply) }, nil
}
