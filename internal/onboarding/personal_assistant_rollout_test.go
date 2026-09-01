package onboarding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func TestPersonalAssistantRollout_BrandNewEnabledPersistsAcrossResetAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app_state.json")
	manager := NewManagerWithPersonalAssistantRollout(path, true)
	if !manager.IsPersonalAssistantEligible() {
		t.Fatal("brand-new state with rollout enabled should be eligible")
	}
	if got := manager.PersonalAssistantEligibilityVersion(); got != personalassistant.CurrentRolloutVersion {
		t.Fatalf("eligibility version = %d", got)
	}
	if err := manager.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := manager.ResetOnboarding(); err != nil {
		t.Fatalf("ResetOnboarding: %v", err)
	}
	if got := manager.PersonalAssistantEligibilityVersion(); got != personalassistant.CurrentRolloutVersion {
		t.Fatalf("reset changed eligibility version to %d", got)
	}

	reloaded := NewManagerWithPersonalAssistantRollout(path, true)
	if !reloaded.IsPersonalAssistantEligible() ||
		reloaded.PersonalAssistantEligibilityVersion() != personalassistant.CurrentRolloutVersion {
		t.Fatal("restart did not preserve eligible marker")
	}
}

func TestPersonalAssistantRollout_BrandNewDisabledRemainsLegacyWhenGateLaterEnables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app_state.json")
	manager := NewManagerWithPersonalAssistantRollout(path, false)
	if manager.IsPersonalAssistantEligible() || manager.PersonalAssistantEligibilityVersion() != 0 {
		t.Fatal("brand-new state with rollout disabled must be explicitly ineligible")
	}
	if err := manager.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded := NewManagerWithPersonalAssistantRollout(path, true)
	if reloaded.IsPersonalAssistantEligible() || reloaded.PersonalAssistantEligibilityVersion() != 0 {
		t.Fatal("enabling the gate later must not enroll an existing state file")
	}
}

func TestPersonalAssistantRollout_LegacyMissingMarkerNeverInheritsNewDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app_state.json")
	legacy := []byte(`{"version":"legacy","onboarding":{"completed":true}}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	manager := NewManagerWithPersonalAssistantRollout(path, true)
	if manager.IsPersonalAssistantEligible() || manager.PersonalAssistantEligibilityVersion() != 0 {
		t.Fatal("legacy file with absent marker was enrolled")
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != string(legacy) {
		t.Fatalf("startup read mutated legacy state: got=%q err=%v", unchanged, err)
	}
	if err := manager.ResetOnboarding(); err != nil {
		t.Fatalf("ResetOnboarding: %v", err)
	}
	reloaded := NewManagerWithPersonalAssistantRollout(path, true)
	if reloaded.IsPersonalAssistantEligible() || reloaded.PersonalAssistantEligibilityVersion() != 0 {
		t.Fatal("legacy marker changed after reset/restart")
	}
}

func TestPersonalAssistantRollout_ExplicitIneligibleAndCorruptStateFailClosed(t *testing.T) {
	t.Run("explicit zero", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "app_state.json")
		if err := os.WriteFile(path, []byte(`{"personal_assistant_rollout_version":0,"version":"v"}`), 0o600); err != nil {
			t.Fatalf("write state: %v", err)
		}
		manager := NewManagerWithPersonalAssistantRollout(path, true)
		if manager.IsPersonalAssistantEligible() {
			t.Fatal("explicit ineligible marker was ignored")
		}
	})

	t.Run("corrupt existing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "app_state.json")
		if err := os.WriteFile(path, []byte(`{broken`), 0o600); err != nil {
			t.Fatalf("write corrupt state: %v", err)
		}
		manager := NewManagerWithPersonalAssistantRollout(path, true)
		if manager.IsPersonalAssistantEligible() || manager.PersonalAssistantEligibilityVersion() != 0 {
			t.Fatal("corrupt existing state was treated as a new eligible install")
		}
		if err := manager.Save(); err != nil {
			t.Fatalf("save fail-closed replacement: %v", err)
		}
		reloaded := NewManagerWithPersonalAssistantRollout(path, true)
		if reloaded.IsPersonalAssistantEligible() {
			t.Fatal("corrupt state became eligible after save/restart")
		}
	})
}

func TestPersonalAssistantRollout_CurrentMarkerStillHonorsServerKillSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app_state.json")
	enabled := NewManagerWithPersonalAssistantRollout(path, true)
	if err := enabled.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	disabled := NewManagerWithPersonalAssistantRollout(path, false)
	if disabled.IsPersonalAssistantEligible() {
		t.Fatal("server kill switch did not disable an eligible marker")
	}
	if disabled.PersonalAssistantEligibilityVersion() != personalassistant.CurrentRolloutVersion {
		t.Fatal("kill switch must not erase the durable eligibility marker")
	}
	if err := disabled.Save(); err != nil {
		t.Fatalf("disabled Save: %v", err)
	}
	reenabled := NewManagerWithPersonalAssistantRollout(path, true)
	if !reenabled.IsPersonalAssistantEligible() || reenabled.PersonalAssistantEligibilityVersion() != personalassistant.CurrentRolloutVersion {
		t.Fatal("re-enabling the kill switch did not restore the same durable eligibility")
	}
}
