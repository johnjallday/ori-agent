//go:build darwin

package onboarding

import (
	"path/filepath"
	"testing"
)

func TestLandingFollowsConsentNotWelcomeReplay(t *testing.T) {
	t.Setenv("WORKSPACE_DIR", "")
	t.Setenv("AGENT_STORE_PATH", "")
	dir := filepath.Join(t.TempDir(), "profile")
	if _, err := CreateInstallRoot(dir); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(filepath.Join(dir, "app_state.json"))
	if err := AcceptSearchFolder(dir); err != nil || SearchLanding(dir) != SearchDesk || manager.IsOnboardingComplete() {
		t.Fatal("grant must only change fresh landing", err)
	}
	if err := manager.ResetOnboarding(); err != nil || SearchLanding(dir) != SearchDesk {
		t.Fatal("Welcome replay changed Search", err)
	}
	if err := DecideSearchLanding(dir, SearchDesk, SearchLater); err != nil {
		t.Fatal(err)
	}
	if err := AcceptSearchFolder(dir); err != nil || SearchLanding(dir) != SearchLater {
		t.Fatal("subsequent grant overrode Home", err)
	}
	if err := ResetSearchLanding(dir); err != nil || SearchLanding(dir) != SearchFirst {
		t.Fatal("Start Fresh did not reset search decision", err)
	}
	legacy := t.TempDir()
	if err := AcceptSearchFolder(legacy); err != nil || SearchLanding(legacy) != SearchLegacy {
		t.Fatal("voluntary mapping changed legacy Home", err)
	}
}
