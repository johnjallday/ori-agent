package projecttemplates

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Enumerate the embedded sources, not starter-task membership or a hand-picked
// coverage list. A new active blueprint must make the same goal decision.
func TestActiveStarterTemplatesShipBoundedGoals(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	entries, err := starterFS.ReadDir(starterRoot)
	if err != nil {
		t.Fatal(err)
	}
	covered, goals := map[string]bool{}, map[string]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		data, err := starterFS.ReadFile(path.Join(starterRoot, id, ManifestFileName))
		if err != nil {
			t.Fatal(err)
		}
		var raw struct {
			Goal    string `json:"goal"`
			Retired bool   `json:"retired"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		if raw.Retired {
			if id != "downloads-janitor" {
				t.Errorf("unexpected retired starter %s; review its exclusion", id)
			}
			continue
		}
		t.Run(id, func(t *testing.T) {
			if raw.Goal == "" || strings.TrimSpace(raw.Goal) != raw.Goal || len(raw.Goal) > workspace.WorkspaceGoalPromptLimit {
				t.Fatalf("goal must be nonempty, trimmed and within the prompt bound: %q", raw.Goal)
			}
			if previous, found := goals[raw.Goal]; found {
				t.Errorf("goal duplicates %s instead of describing this template's outcome", previous)
			}
			goals[raw.Goal] = id
			tpl, err := FindLibraryTemplate(dir, id)
			if err != nil || tpl.Goal != raw.Goal {
				t.Fatalf("materialized goal = %q, err=%v", tpl.Goal, err)
			}
		})
		covered[id] = true
	}
	if len(covered) != 11 || !covered["code-project"] || !covered["writing-project"] {
		t.Fatalf("active starter coverage changed: %v; review the shipped catalog", covered)
	}
	retired, err := FindLibraryTemplate(dir, "downloads-janitor")
	if err != nil || !retired.Retired {
		t.Fatalf("retired ID must remain resolvable without revival: %+v, %v", retired, err)
	}
}

func TestBuiltinGoalRefreshPreservesUserData(t *testing.T) {
	for _, id := range []string{"writing-project", "code-project", "email-ops"} {
		for _, oldGoal := range []string{"", "An older default goal."} {
			t.Run(id+"/"+oldGoal, func(t *testing.T) {
				dir := t.TempDir()
				if err := EnsureLibrary(dir); err != nil {
					t.Fatal(err)
				}
				manifest := filepath.Join(dir, id, ManifestFileName)
				shipped := mustReadFile(t, manifest)
				var stale map[string]any
				if err := json.Unmarshal([]byte(shipped), &stale); err != nil {
					t.Fatal(err)
				}
				stale["builtin_version"] = stale["builtin_version"].(float64) - 1
				if oldGoal == "" {
					delete(stale, "goal")
				} else {
					stale["goal"] = oldGoal
				}
				data, err := json.Marshal(stale)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(manifest, data, 0o600); err != nil {
					t.Fatal(err)
				}
				seed := filepath.Join(dir, id, "outline.md")
				if err := os.WriteFile(seed, []byte("User-owned outline"), 0o600); err != nil {
					t.Fatal(err)
				}
				user := filepath.Join(dir, "my-project")
				if err := os.MkdirAll(user, 0o750); err != nil {
					t.Fatal(err)
				}
				custom := `{"name":"My Project","goal":"My own outcome.","private_key":"kept"}`
				if err := os.WriteFile(filepath.Join(user, ManifestFileName), []byte(custom), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := EnsureLibrary(dir); err != nil {
					t.Fatal(err)
				}
				if got := mustReadFile(t, manifest); got != shipped {
					t.Fatal("refresh did not deliver the shipped manifest")
				}
				if got := mustReadFile(t, seed); got != "User-owned outline" {
					t.Fatal("refresh changed the scaffold")
				}
				if got := mustReadFile(t, filepath.Join(user, ManifestFileName)); got != custom {
					t.Fatal("refresh changed a user template")
				}
				stamp, err := os.Stat(manifest)
				if err != nil {
					t.Fatal(err)
				}
				if err := EnsureLibrary(dir); err != nil {
					t.Fatal(err)
				}
				again, err := os.Stat(manifest)
				if err != nil || !stamp.ModTime().Equal(again.ModTime()) {
					t.Fatalf("repeat refresh rewrote the manifest: %v", err)
				}
				// An intentional clear at the current version is also user data.
				var current map[string]any
				if err := json.Unmarshal([]byte(shipped), &current); err != nil {
					t.Fatal(err)
				}
				current["goal"] = ""
				data, err = json.Marshal(current)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(manifest, data, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := EnsureLibrary(dir); err != nil {
					t.Fatal(err)
				}
				if got := mustReadFile(t, manifest); got != string(data) {
					t.Fatal("refresh replaced current-version data")
				}
			})
		}
	}
}

func TestTemplateGoalIsReadAndTrimmed(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"name":"Goal Demo","goal":"  Keep the brand voice consistent.  "}`
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	tpl := newTemplate(dir)
	if tpl.Goal != "Keep the brand voice consistent." {
		t.Fatalf("Goal = %q", tpl.Goal)
	}

	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(`{"name":"No Goal"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if tpl := newTemplate(dir); tpl.Goal != "" {
		t.Fatalf("Goal without a goal key = %q", tpl.Goal)
	}
}
