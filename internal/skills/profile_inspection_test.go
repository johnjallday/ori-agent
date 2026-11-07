package skills

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func profileInspectionFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	manager := &Manager{agentFolder: func(string) (string, bool) { return dir, true }}
	return manager, filepath.Join(dir, skillStateFileName)
}

func TestProfileSkillInspectionIsReadOnlyBoundedAndDoesNotResolveUnderGuard(t *testing.T) {
	manager, path := profileInspectionFixture(t)
	var resolves atomic.Int32
	manager.agentFolder = func(string) (string, bool) { resolves.Add(1); return filepath.Dir(path), true }
	inspection, err := manager.PrepareProfileSkillInspection("Aster")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := inspection.Read()
	if err != nil || empty.Enabled("catalog") || empty.Witness == "" {
		t.Fatal("missing state is not an empty opt-in collection", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read initialized skill state", err)
	}
	if err := os.WriteFile(path, []byte(`{"skills":{"*":{"enabled":true},"catalog":{"enabled":false},"songs":{"enabled":true,"trusted":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := inspection.Read()
	if err != nil || snapshot.Enabled("catalog") || !snapshot.Enabled("songs") || !snapshot.Enabled("other") || snapshot.Witness == empty.Witness {
		t.Fatal("skill membership projection differs from runtime semantics", err)
	}
	if err := inspection.WithUnchanged(snapshot.Witness, func() error { return nil }); err != nil || resolves.Load() != 1 {
		t.Fatal("inspection re-entered the profile resolver", err)
	}
	if err := manager.SetSkillEnabled("Aster", "songs", false); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := inspection.WithUnchanged(snapshot.Witness, func() error { called = true; return nil }); !errors.Is(err, ErrProfileSkillsChanged) || called {
		t.Fatal("changed skills reached binding callback", err)
	}
}

func TestProfileSkillInspectionRejectsUnreadableAndNonCanonicalState(t *testing.T) {
	for _, scenario := range []string{"malformed", "oversized", "collision", "symlink", "missing_folder"} {
		t.Run(scenario, func(t *testing.T) {
			manager, path := profileInspectionFixture(t)
			switch scenario {
			case "missing_folder":
				manager.agentFolder = func(string) (string, bool) { return filepath.Join(filepath.Dir(path), "missing"), true }
			case "symlink":
				target := filepath.Join(t.TempDir(), "other.json")
				if err := os.WriteFile(target, []byte(`{"skills":{}}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			default:
				data := "{broken"
				if scenario == "oversized" {
					data = strings.Repeat(" ", 256*1024+1)
				}
				if scenario == "collision" {
					data = `{"skills":{"catalog":{"enabled":false},"Catalog":{"enabled":true}}}`
				}
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			inspection, err := manager.PrepareProfileSkillInspection("Aster")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := inspection.Read(); !errors.Is(err, ErrProfileSkillStateUnavailable) {
				t.Fatal("unknown state treated as known capabilities", err)
			}
		})
	}
}

func TestProfileSkillInspectionGuardSerializesProfileToggles(t *testing.T) {
	manager, path := profileInspectionFixture(t)
	if err := manager.SetSkillEnabled("Aster", "catalog", true); err != nil {
		t.Fatal(err)
	}
	inspection, err := manager.PrepareProfileSkillInspection("Aster")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := inspection.Read()
	if err != nil {
		t.Fatal(err)
	}
	resolved := make(chan struct{})
	manager.agentFolder = func(string) (string, bool) { close(resolved); return filepath.Dir(path), true }
	done := make(chan error, 1)
	if err := inspection.WithUnchanged(snapshot.Witness, func() error {
		go func() { done <- manager.SetSkillEnabled("Aster", "catalog", false) }()
		<-resolved
		select {
		case err := <-done:
			t.Fatal("skill toggle escaped the bind guard", err)
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := inspection.WithUnchanged(snapshot.Witness, func() error { t.Fatal("old guard accepted changed membership"); return nil }); !errors.Is(err, ErrProfileSkillsChanged) {
		t.Fatal("toggle did not invalidate capability witness", err)
	}
}
