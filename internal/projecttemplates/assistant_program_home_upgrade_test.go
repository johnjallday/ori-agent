package projecttemplates

import (
	"errors"
	"testing"
)

func TestGuidanceOnlyHomeChangeListsChangedRolePromptsInOrder(t *testing.T) {
	current, next := independentHomeFixture(), independentHomeFixture()
	next.Roles[1].SystemPrompt = "Use reviewed sample operations and the approved library."
	changes, err := GuidanceOnlyHomeChange(current, next)
	if err != nil || len(changes) != 1 {
		t.Fatalf("changes = %#v, %v", changes, err)
	}
	if got := changes[0]; got.RoleID != "sample_library_manager" || got.Label != "Sample Library Manager" ||
		got.Old != current.Roles[1].SystemPrompt || got.New != next.Roles[1].SystemPrompt {
		t.Fatalf("change = %#v", got)
	}
	if current.Roles[1].SystemPrompt == "" || next.Roles[0].SystemPrompt == "" {
		t.Fatal("comparison blanked the caller's prompts")
	}

	next.Roles[0].SystemPrompt = "Coordinate reviewed work and library proposals."
	changes, err = GuidanceOnlyHomeChange(current, next)
	if err != nil || len(changes) != 2 || changes[0].RoleID != "portfolio_manager" || changes[1].RoleID != "sample_library_manager" {
		t.Fatalf("two changes = %#v, %v", changes, err)
	}
	if changes, err := GuidanceOnlyHomeChange(current, independentHomeFixture()); err != nil || len(changes) != 0 {
		t.Fatalf("identical releases = %#v, %v", changes, err)
	}
}

func TestGuidanceOnlyHomeChangeRefusesEveryNonPromptChange(t *testing.T) {
	for name, mutate := range map[string]func(*AssistantProgramHome){
		"home version":    func(home *AssistantProgramHome) { home.Version = 2 },
		"station name":    func(home *AssistantProgramHome) { home.StationName = "Renamed Home" },
		"role label":      func(home *AssistantProgramHome) { home.Roles[0].Label = "Producer" },
		"role id":         func(home *AssistantProgramHome) { home.Roles[1].ID = "sample_manager" },
		"role required":   func(home *AssistantProgramHome) { home.Roles[1].Required = true },
		"role capability": func(home *AssistantProgramHome) { home.Roles[1].CapabilityID = "" },
		"role skills":     func(home *AssistantProgramHome) { home.Roles[0].Skills = []string{"another-skill"} },
		"role order":      func(home *AssistantProgramHome) { home.Roles[0], home.Roles[1] = home.Roles[1], home.Roles[0] },
		"added role": func(home *AssistantProgramHome) {
			home.Roles = append(home.Roles, AssistantProgramHomeRole{ID: "extra", Label: "Extra"})
		},
		"stage":            func(home *AssistantProgramHome) { home.Stages[0].Label = "Start" },
		"reflection":       func(home *AssistantProgramHome) { home.Reflection.CadenceHours = 24 },
		"attachment range": func(home *AssistantProgramHome) { home.AllowedProjectAttachments[0].MaxProjectTeamVersion = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			next := independentHomeFixture()
			next.Roles[0].SystemPrompt = "A prompt change alone would be allowed."
			mutate(&next)
			if changes, err := GuidanceOnlyHomeChange(independentHomeFixture(), next); !errors.Is(err, ErrHomeUpgradeNotGuidanceOnly) {
				t.Fatalf("changes = %#v, err = %v", changes, err)
			}
		})
	}
}
