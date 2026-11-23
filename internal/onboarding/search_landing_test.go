package onboarding

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSearchLandingOnlyStartsOnNewInstallAndReopensIndependently(t *testing.T) {
	t.Setenv("WORKSPACE_DIR", "")
	t.Setenv("AGENT_STORE_PATH", "")
	root := filepath.Join(t.TempDir(), "data")
	newRoot, e := CreateInstallRoot(root)
	if e != nil || !newRoot {
		t.Fatal(e)
	}
	want := SearchFirst
	if runtime.GOOS != "darwin" {
		want = SearchLegacy
	}
	if SearchLanding(root) != want {
		t.Fatal("unexpected initial state")
	}
	for _, state := range []string{SearchLater, SearchDesk} {
		if err := DecideSearchLanding(root, want, state); err != nil {
			t.Fatal(err)
		}
		newRoot, e = CreateInstallRoot(root)
		if e != nil || newRoot || SearchLanding(root) != state {
			t.Fatal("reopen lost choice")
		}
		if err := DecideSearchLanding(root, want, state); err == nil {
			t.Fatal("stale decision succeeded")
		}
		want = state
	}
	if _, e = os.Stat(filepath.Join(root, "app_state.json")); !os.IsNotExist(e) {
		t.Fatal("workspace onboarding touched")
	}
	if e = os.WriteFile(filepath.Join(root, searchReceipt), []byte("bro"), 0600); e != nil {
		t.Fatal(e)
	}
	if SearchLanding(root) != SearchUnknown || DecideSearchLanding(root, SearchUnknown, SearchDesk) == nil {
		t.Fatal("incomplete write mistaken for decision")
	}
}
func TestExistingOrConfiguredRootIsNotANewSearchInstall(t *testing.T) {
	for _, configured := range []bool{false, true} {
		base := t.TempDir()
		root := base
		if configured {
			t.Setenv("WORKSPACE_DIR", filepath.Join(base, "configured"))
			root = filepath.Join(base, "data")
		}
		if _, err := CreateInstallRoot(root); err != nil {
			t.Fatal(err)
		}
		if SearchLanding(root) != SearchLegacy {
			t.Fatal("implicit fresh receipt")
		}
	}
}
