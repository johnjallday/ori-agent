package onboarding

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersonalAssistantOnboarding_BrandNewStatePersistsAcrossResetAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app_state.json")
	manager := NewManager(path)
	assertCanonicalPersonalAssistantOnboarding(t, manager)
	if err := manager.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := manager.ResetOnboarding(); err != nil {
		t.Fatalf("ResetOnboarding: %v", err)
	}
	assertCanonicalPersonalAssistantOnboarding(t, manager)
	assertCanonicalPersonalAssistantOnboarding(t, NewManager(path))
}

func TestPersonalAssistantOnboarding_ExistingStateUsesCanonicalDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app_state.json")
	if err := os.WriteFile(path, []byte(`{"version":"legacy","onboarding":{"completed":true}}`), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	manager := NewManager(path)
	_, assistantName := manager.GetNames()
	if assistantName != DefaultAssistantName {
		t.Fatalf("assistant name = %q, want %q", assistantName, DefaultAssistantName)
	}
	if err := manager.ResetOnboarding(); err != nil {
		t.Fatalf("ResetOnboarding: %v", err)
	}
	assertCanonicalPersonalAssistantOnboarding(t, NewManager(path))
}

func TestPersonalAssistantOnboarding_CorruptStateRecoversCanonicalDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app_state.json")
	if err := os.WriteFile(path, []byte(`{broken`), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	manager := NewManager(path)
	assertCanonicalPersonalAssistantOnboarding(t, manager)
	if err := manager.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertCanonicalPersonalAssistantOnboarding(t, NewManager(path))
}

func assertCanonicalPersonalAssistantOnboarding(t *testing.T, manager *Manager) {
	t.Helper()
	if manager.IsOnboardingComplete() {
		t.Fatal("canonical personal-assistant onboarding must be incomplete")
	}
	_, assistantName := manager.GetNames()
	if assistantName != DefaultAssistantName {
		t.Fatalf("assistant name = %q, want %q", assistantName, DefaultAssistantName)
	}
}
