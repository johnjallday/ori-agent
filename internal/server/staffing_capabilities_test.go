package server

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestStaffingSkillEvidenceUsesExactProfileAndAvailableExistingMembership(t *testing.T) {
	for _, scenario := range []string{"available", "disabled", "unavailable", "source_changed_during_preparation", "membership_changed_before_bind"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			index := filepath.Join(root, "agents.json")
			profiles, err := store.NewFileStore(index, types.Settings{})
			if err != nil {
				t.Fatal(err)
			}
			if err := profiles.CreateAgent("Aster", &store.CreateAgentConfig{}); err != nil {
				t.Fatal(err)
			}
			manager := skills.NewManager(skills.ManagerConfig{AgentStorePath: index})
			dir := filepath.Join(root, "catalog")
			if err := os.Mkdir(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: catalog\ndescription: Catalog music projects\n---\nPRIVATE_PROMPT_CANARY\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			manager.SetPluginSkillProvider(func(string) []skills.PluginSkill {
				if scenario == "unavailable" {
					return nil
				}
				return []skills.PluginSkill{{Plugin: "music", Name: "catalog", Dir: dir}}
			})
			if err := manager.SetSkillEnabled("Aster", "catalog", scenario != "disabled"); err != nil {
				t.Fatal(err)
			}
			profile := store.ReadStaffingSnapshot(t.Context(), profiles).Profiles[0]
			selected := store.StaffingSelection{Name: profile.Name, Witness: profile.Witness}
			registry := filepath.Join(root, "agents", "Aster", "skills_state.json")
			before, err := os.ReadFile(registry)
			if err != nil {
				t.Fatal(err)
			}
			resolves := 0
			manager.SetAgentFolderResolver(func(name string) (string, bool) {
				resolves++
				if scenario == "source_changed_during_preparation" {
					if err := profiles.UpdateAgent(name, func(profile *agent.Agent) error {
						profile.Settings.SystemPrompt = "Different authored definition"
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				return filepath.Dir(registry), true
			})
			grants := serverStaffingToolGrants{builder: &ServerBuilder{st: profiles, skillsManager: manager}}
			membership := grants.ReadStaffingSkills(t.Context(), selected, []string{"catalog"}, true)
			if resolves != 1 {
				t.Fatal("read re-entered profile resolver")
			}
			shouldBeEnabled := scenario == "available" || scenario == "membership_changed_before_bind"
			if (len(membership.Enabled) == 1) != shouldBeEnabled {
				t.Fatal("eligibility disagrees with available membership", scenario)
			}
			if scenario == "source_changed_during_preparation" && membership.Witness != "" {
				t.Fatal("replaced definition gained old-source skill evidence")
			}
			after, err := os.ReadFile(registry)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("evidence collection changed registry", err)
			}
			if !shouldBeEnabled {
				return
			}
			guard, err := grants.PrepareStaffingCapabilities("Aster", []string{"catalog"}, true, membership.Witness)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "membership_changed_before_bind" {
				if err := manager.SetSkillEnabled("Aster", "catalog", false); err != nil {
					t.Fatal(err)
				}
			}
			manager.SetAgentFolderResolver(func(string) (string, bool) { panic("profile resolution under guard") })
			applied := false
			err = store.WithStaffingProfiles(t.Context(), profiles, []store.StaffingSelection{selected}, func(map[string]*agent.Agent) error {
				return guard(func() error { applied = true; return nil })
			})
			if scenario == "available" && (err != nil || !applied) {
				t.Fatal("unchanged source/capability refused", err)
			}
			if scenario == "membership_changed_before_bind" && (!errors.Is(err, skills.ErrProfileSkillsChanged) || applied) {
				t.Fatal("revoked membership bound", err)
			}
		})
	}
}
