package projecttemplates

import (
	"errors"
	"testing"
)

func upgradeHome(t *testing.T, mutate func(*AssistantProgramHome)) AssistantProgramHome {
	t.Helper()
	home := independentHomeFixture()
	if mutate != nil {
		mutate(&home)
	}
	if err := NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return home
}

func TestAcceptedHomeChangeAcceptsAnAddedProfile(t *testing.T) {
	installed := upgradeHome(t, nil)
	next := upgradeHome(t, func(home *AssistantProgramHome) { home.HomeProfile = homeProfileFixture() })
	change, err := AcceptedHomeChange(installed, next)
	if err != nil {
		t.Fatalf("an added profile was refused: %v", err)
	}
	if !change.AddsHomeProfile || change.HomeProfileTitle != "Your studio" || len(change.RolePrompts) != 0 {
		t.Fatalf("change = %+v", change)
	}
	// The older rule on its own still refuses it: the section is a real
	// difference, accepted only as its own class.
	if _, err := GuidanceOnlyHomeChange(installed, next); !errors.Is(err, ErrHomeUpgradeNotGuidanceOnly) {
		t.Fatalf("guidance-only accepted an added section: %v", err)
	}
	// The caller's declarations are left as they were.
	if next.HomeProfile == nil || installed.HomeProfile != nil {
		t.Fatal("the comparison changed its inputs")
	}
}

func TestAcceptedHomeChangeAcceptsAnAddedProfileWithNewGuidance(t *testing.T) {
	installed := upgradeHome(t, nil)
	next := upgradeHome(t, func(home *AssistantProgramHome) {
		home.HomeProfile = homeProfileFixture()
		home.Roles[0].SystemPrompt = "Coordinate reviewed work. Use the studio profile."
	})
	change, err := AcceptedHomeChange(installed, next)
	if err != nil || !change.AddsHomeProfile || len(change.RolePrompts) != 1 || change.RolePrompts[0].RoleID != "portfolio_manager" {
		t.Fatalf("change = %+v err = %v", change, err)
	}
}

func TestAcceptedHomeChangeIsGuidanceOnlyWithoutAnAddedProfile(t *testing.T) {
	installed := upgradeHome(t, nil)
	next := upgradeHome(t, func(home *AssistantProgramHome) { home.Roles[0].SystemPrompt = "Coordinate reviewed work carefully." })
	change, err := AcceptedHomeChange(installed, next)
	if err != nil || change.AddsHomeProfile || change.HomeProfileTitle != "" || len(change.RolePrompts) != 1 {
		t.Fatalf("change = %+v err = %v", change, err)
	}
	// Two releases that both declare the same profile differ by guidance only.
	withProfile := func(prompt string) AssistantProgramHome {
		return upgradeHome(t, func(home *AssistantProgramHome) {
			home.HomeProfile = homeProfileFixture()
			home.Roles[0].SystemPrompt = prompt
		})
	}
	change, err = AcceptedHomeChange(withProfile("Coordinate reviewed work."), withProfile("Coordinate reviewed work carefully."))
	if err != nil || change.AddsHomeProfile || len(change.RolePrompts) != 1 {
		t.Fatalf("same profile on both sides: %+v %v", change, err)
	}
}

func TestAcceptedHomeChangeRefusesEverythingElse(t *testing.T) {
	withProfile := func(mutate func(*AssistantProgramHome)) AssistantProgramHome {
		return upgradeHome(t, func(home *AssistantProgramHome) {
			home.HomeProfile = homeProfileFixture()
			if mutate != nil {
				mutate(home)
			}
		})
	}
	tests := map[string]struct{ installed, next AssistantProgramHome }{
		"a profile removed": {withProfile(nil), upgradeHome(t, nil)},
		"a profile row relabelled": {withProfile(nil), withProfile(func(home *AssistantProgramHome) {
			home.HomeProfile.Fields[0].Label = "Applications"
		})},
		"a profile row dropped": {withProfile(nil), withProfile(func(home *AssistantProgramHome) {
			home.HomeProfile.Fields = home.HomeProfile.Fields[:3]
		})},
		"a profile retitled": {withProfile(nil), withProfile(func(home *AssistantProgramHome) { home.HomeProfile.Title = "Studio" })},
		"an added profile and another project attachment": {upgradeHome(t, nil), withProfile(func(home *AssistantProgramHome) {
			home.AllowedProjectAttachments[0].MaxProjectTeamVersion = 2
		})},
		"an added profile and a renamed Home": {upgradeHome(t, nil), withProfile(func(home *AssistantProgramHome) {
			home.StationName = "Studio Home"
		})},
		"an added profile and a new role": {upgradeHome(t, nil), withProfile(func(home *AssistantProgramHome) {
			home.Roles = append(home.Roles, AssistantProgramHomeRole{ID: "mix_engineer", Label: "Mix Engineer", CapabilityID: "mixing", SystemPrompt: "Mix."})
		})},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if change, err := AcceptedHomeChange(tc.installed, tc.next); !errors.Is(err, ErrHomeUpgradeNotGuidanceOnly) {
				t.Fatalf("change = %+v err = %v, want refused", change, err)
			}
		})
	}
}
