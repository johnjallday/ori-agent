package setupjourney

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type inspectedStaffingSkills struct {
	*staffingGrantStub
	manager      *skills.Manager
	afterPrepare func()
}

func (g inspectedStaffingSkills) PrepareStaffingCapabilities(name string, names []string, _ bool, witness string) (StaffingCapabilityGuard, error) {
	inspection, err := g.manager.PrepareProfileSkillInspection(name)
	if err != nil {
		return nil, err
	}
	current, err := inspection.Read()
	if err != nil || current.Witness != witness {
		return nil, ErrConflict
	}
	for _, name := range names {
		if !current.Enabled(name) || !g.Available(name) {
			return nil, ErrConflict
		}
	}
	if g.afterPrepare != nil {
		g.afterPrepare()
	}
	return func(apply func() error) error { return inspection.WithUnchanged(witness, apply) }, nil
}

func TestStaffRoleOnWorkspaceReviewedSkillsAreFencedWithoutGrantsOrSourceReentry(t *testing.T) {
	for _, scenario := range []string{"unchanged", "changed_before_bind", "missing_witness", "wrong_witness"} {
		t.Run(scenario, func(t *testing.T) {
			adapter, workspaces, scope, grants := staffingFixture(t)
			index := filepath.Join(t.TempDir(), "agents.json")
			profiles, err := store.NewFileStore(index, types.Settings{Provider: "openai", Model: "gpt-4o-mini"})
			if err != nil {
				t.Fatal(err)
			}
			adapter.profiles = profiles
			if err := profiles.CreateAgent("Aster", &store.CreateAgentConfig{}); err != nil {
				t.Fatal(err)
			}
			manager := skills.NewManager(skills.ManagerConfig{AgentStorePath: index})
			manager.SetAgentFolderResolver(func(name string) (string, bool) {
				if _, found := profiles.GetAgent(name); !found {
					t.Fatal("unexpected profile resolution")
				}
				return filepath.Join(filepath.Dir(index), "agents", name), true
			})
			if err := manager.SetSkillEnabled("Aster", "project-skill", true); err != nil {
				t.Fatal(err)
			}
			inspection, err := manager.PrepareProfileSkillInspection("Aster")
			if err != nil {
				t.Fatal(err)
			}
			capabilities, err := inspection.Read()
			if err != nil {
				t.Fatal(err)
			}
			source := store.ReadStaffingSnapshot(t.Context(), profiles).Profiles[0]
			if err := workspaces.Update(scope.HomeWorkspaceID, func(home *workspace.Workspace) error {
				state := home.GetAssistantProgramState()
				state.Declaration.Roles[0].Skills = []string{"project-skill"}
				home.SetAssistantProgramState(state)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			adapter.grants = inspectedStaffingSkills{staffingGrantStub: grants, manager: manager, afterPrepare: func() {
				if scenario == "changed_before_bind" {
					if err := manager.SetSkillEnabled("Aster", "project-skill", false); err != nil {
						t.Fatal(err)
					}
				}
				manager.SetAgentFolderResolver(func(string) (string, bool) { panic("source re-entry under capability fence") })
			}}
			fill := RoleFill{RoleID: "home_guide", Mode: StaffingModeBind, Name: "Aster", SourceWitness: source.Witness,
				CapabilityWitness: capabilities.Witness, ChoiceDigest: strings.Repeat("a", 64)}
			if scenario == "missing_witness" {
				fill.CapabilityWitness = ""
			}
			if scenario == "wrong_witness" {
				fill.CapabilityWitness = strings.Repeat("f", 64)
			}
			err = adapter.StaffRoleOnWorkspace(t.Context(), scope.HomeWorkspaceID, []RoleFill{fill})
			if (err == nil) != (scenario == "unchanged") {
				t.Fatal("unexpected bind result", scenario, err)
			}
			home, _ := workspaces.Get(scope.HomeWorkspaceID)
			if (len(home.GetAgentInstances()) == 1) != (scenario == "unchanged") {
				t.Fatal("refused capability state gained binding")
			}
			if len(grants.grantCalls) != 0 || len(grants.personalGrantCalls) != 0 {
				t.Fatal("reuse granted skills")
			}
			if now := store.ReadStaffingSnapshot(t.Context(), profiles); len(now.Profiles) != 1 || now.Profiles[0].Witness != source.Witness {
				t.Fatal("capability validation changed authored profile")
			}
			if scenario == "changed_before_bind" {
				current, err := inspection.Read()
				if err != nil || current.Enabled("project-skill") {
					t.Fatal("refused bind restored revoked membership", err)
				}
			}
		})
	}
}
