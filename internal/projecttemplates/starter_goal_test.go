package projecttemplates

import (
	"os"
	"path/filepath"
	"testing"
)

// starterTaskTemplateIDs are the built-ins that seed starter tasks. Each ships
// a default goal so a workspace created from it can suggest work right away.
var starterTaskTemplateIDs = []string{
	"calendar-ops", "content-production", "daily-briefings", "downloads-janitor", "email-ops",
	"file-janitor", "github-ops", "personal-ops", "research-project", "travels",
}

func TestStarterTemplatesWithStarterTasksShipAGoal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	for _, id := range starterTaskTemplateIDs {
		tpl, err := FindLibraryTemplate(dir, id)
		if err != nil {
			t.Fatalf("FindLibraryTemplate(%q): %v", id, err)
		}
		if len(tpl.StarterTasks) == 0 {
			t.Errorf("%s no longer seeds starter tasks; update starterTaskTemplateIDs", id)
		}
		if tpl.Goal == "" {
			t.Errorf("%s has starter tasks but no goal", id)
		}
	}

	// Every listed built-in that seeds starter tasks is covered above, so a new
	// starter cannot ship without deciding on its goal.
	listed, err := ListLibrary(dir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	covered := map[string]bool{}
	for _, id := range starterTaskTemplateIDs {
		covered[id] = true
	}
	for _, tpl := range listed {
		if tpl.Builtin && len(tpl.StarterTasks) > 0 && !covered[tpl.ID] {
			t.Errorf("built-in %s seeds starter tasks but is not in starterTaskTemplateIDs", tpl.ID)
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
