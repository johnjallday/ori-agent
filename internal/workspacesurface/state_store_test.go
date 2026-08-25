package workspacesurface

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestStateStoreDistinguishesMissingFromExplicitNullAndPersists(t *testing.T) {
	root := t.TempDir()
	store := NewStateStore(root)
	missing, err := store.Get("demo-plugin", "workspace-a", "display")
	if err != nil || missing.Found || missing.Revision != "0" || missing.Value != nil {
		t.Fatalf("missing state = %+v, %v", missing, err)
	}
	saved, err := store.Set("demo-plugin", "workspace-a", "display", 1, missing.Revision, json.RawMessage(`null`))
	if err != nil || !saved.Found || string(saved.Value) != "null" || saved.Revision != "1" {
		t.Fatalf("saved null = %+v, %v", saved, err)
	}
	got, err := NewStateStore(root).Get("demo-plugin", "workspace-a", "display")
	if err != nil || !got.Found || got.SchemaVersion != 1 || string(got.Value) != "null" {
		t.Fatalf("persisted null = %+v, %v", got, err)
	}
	path := store.path("demo-plugin", "workspace-a")
	if strings.Contains(path, "demo-plugin") || strings.Contains(path, "workspace-a") {
		t.Fatalf("raw namespace identifiers became path components: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, %v", info, err)
	}
}

func TestStateStoreCompareAndSwapPreventsConcurrentLostWrites(t *testing.T) {
	store := NewStateStore(t.TempDir())
	const writers = 12
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Set("demo-plugin", "workspace-a", "display", 1, "0", json.RawMessage(`{"writer":1}`))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	succeeded, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrStateConflict):
			conflicts++
		default:
			t.Fatalf("writer error = %v", err)
		}
	}
	if succeeded != 1 || conflicts != writers-1 {
		t.Fatalf("success/conflicts = %d/%d", succeeded, conflicts)
	}
}

func TestStateStoreNamespacesPluginsAndWorkspaces(t *testing.T) {
	store := NewStateStore(t.TempDir())
	for _, address := range [][2]string{{"plugin-a", "workspace-a"}, {"plugin-b", "workspace-a"}, {"plugin-a", "workspace-b"}} {
		if _, err := store.Set(address[0], address[1], "display", 1, "0", json.RawMessage(`{"owner":"`+address[0]+`/`+address[1]+`"}`)); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range [][2]string{{"plugin-a", "workspace-a"}, {"plugin-b", "workspace-a"}, {"plugin-a", "workspace-b"}} {
		got, err := store.Get(address[0], address[1], "display")
		if err != nil || !strings.Contains(string(got.Value), address[0]+"/"+address[1]) {
			t.Fatalf("namespace %v = %s, %v", address, got.Value, err)
		}
	}
}

func TestStateStoreEnforcesValueDepthAndNamespaceQuotas(t *testing.T) {
	store := NewStateStore(t.TempDir())
	tooLarge := json.RawMessage(`"` + strings.Repeat("x", maxStateValueBytes) + `"`)
	if _, err := store.Set("demo-plugin", "workspace-a", "large", 1, "0", tooLarge); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("large value error = %v", err)
	}
	deep := strings.Repeat(`{"x":`, 17) + `null` + strings.Repeat(`}`, 17)
	if _, err := store.Set("demo-plugin", "workspace-a", "deep", 1, "0", json.RawMessage(deep)); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("deep value error = %v", err)
	}
}

func TestStateStoreDisableUpdatePreserveAndExplicitUninstallDeletes(t *testing.T) {
	root := t.TempDir()
	store := NewStateStore(root)
	saved, err := store.Set("demo-plugin", "workspace-a", "display", 3, "0", json.RawMessage(`{"theme":"meter"}`))
	if err != nil {
		t.Fatal(err)
	}
	// Reconstructing the store models disable/re-enable or update; bytes and
	// schema version remain available to only the same namespace.
	afterUpdate, err := NewStateStore(root).Get("demo-plugin", "workspace-a", "display")
	if err != nil || afterUpdate.Revision != saved.Revision || afterUpdate.SchemaVersion != 3 {
		t.Fatalf("state after update = %+v, %v", afterUpdate, err)
	}
	if err := store.DeletePlugin("demo-plugin"); err != nil {
		t.Fatal(err)
	}
	afterUninstall, err := store.Get("demo-plugin", "workspace-a", "display")
	if err != nil || afterUninstall.Found || afterUninstall.Revision != "0" {
		t.Fatalf("state after uninstall = %+v, %v", afterUninstall, err)
	}
}
