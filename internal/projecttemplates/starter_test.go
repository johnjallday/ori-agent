package projecttemplates

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureLibraryMaterializesStarters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	templates, err := ListLibrary(dir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	byID := map[string]Template{}
	for _, tpl := range templates {
		byID[tpl.ID] = tpl
	}

	// The file-scaffold starter and metadata-only built-ins all materialize.
	for _, id := range []string{
		"writing-project", "travels", "daily-briefings", "content-production", "research-project", "personal-ops",
	} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("missing starter %q; got %v", id, templates)
		}
	}

	// Scaffold starters keep their files and are flagged builtin.
	if writing := byID["writing-project"]; !writing.Builtin || !writing.HasSkeleton ||
		len(writing.Tags) != 1 || writing.Tags[0] != "writing" {
		t.Errorf("writing-project: %+v", writing)
	}

	// Metadata-only built-ins: builtin, no skeleton, carrying icon/behavior/tasks.
	travels := byID["travels"]
	if !travels.Builtin || travels.HasSkeleton {
		t.Errorf("travels should be a builtin metadata-only template: %+v", travels)
	}
	if travels.Icon == "" || travels.BehaviorProfile != BehaviorProfileGeneral || len(travels.StarterTasks) == 0 {
		t.Errorf("travels missing unified fields: %+v", travels)
	}
	if research := byID["research-project"]; research.BehaviorProfile != BehaviorProfileResearch {
		t.Errorf("research-project behavior_profile = %q, want research", research.BehaviorProfile)
	}
	// The research-project starter ships an agent roster (entry + specialists).
	if research := byID["research-project"]; !research.HasAgents() {
		t.Errorf("research-project should declare an agent roster, got none")
	} else if len(research.Agents) != 3 || research.Agents[0].Name != "Research Lead" {
		t.Errorf("research-project roster wrong: %+v", research.Agents)
	}

	// The dot-file under chapters/ made it out of the embed (all: prefix).
	if _, err := os.Stat(filepath.Join(dir, "writing-project", "chapters", ".keep")); err != nil {
		t.Errorf("chapters/.keep missing: %v", err)
	}
}

func TestEnsureLibraryNeverOverwritesUserEdits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	edited := filepath.Join(dir, "writing-project", "outline.md")
	if err := os.WriteFile(edited, []byte("user-edited"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary (second run): %v", err)
	}
	data, err := os.ReadFile(edited)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "user-edited" {
		t.Error("starter materialization overwrote a user edit")
	}
}

func TestEnsureLibraryRefreshesBuiltinManifestOnVersionBump(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	// Simulate an old install: an on-disk built-in with no agents and a stale
	// builtin_version (the embedded research-project is version >= 1 with a
	// roster), plus a user-edited seed file that must survive the refresh.
	wp := filepath.Join(dir, "writing-project")
	manifestPath := filepath.Join(wp, ManifestFileName)
	if err := os.WriteFile(manifestPath, []byte(`{"name":"Writing Project","builtin":true,"builtin_version":0}`), 0o640); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(wp, "outline.md")
	if err := os.WriteFile(seed, []byte("user-edited outline"), 0o640); err != nil {
		t.Fatal(err)
	}

	// A second EnsureLibrary should refresh the stale manifest (embedded
	// version > 0) but leave the hand-edited seed file untouched.
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary (refresh): %v", err)
	}

	refreshed, err := FindLibraryTemplate(dir, "writing-project")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if !refreshed.HasAgents() {
		t.Error("expected the stale built-in manifest to be refreshed with its shipped roster")
	}
	if refreshed.BuiltinVersion < 1 {
		t.Errorf("expected builtin_version refreshed to the shipped value, got %d", refreshed.BuiltinVersion)
	}
	// The hand-edited seed file is preserved (manifest-only refresh).
	data, err := os.ReadFile(seed)
	if err != nil || string(data) != "user-edited outline" {
		t.Errorf("seed file should survive a manifest refresh, got %q err=%v", string(data), err)
	}

	// A third run is a no-op now that on-disk version matches the embed.
	stamp, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary (no-op): %v", err)
	}
	if again, _ := os.Stat(manifestPath); again.ModTime() != stamp.ModTime() {
		t.Error("manifest was rewritten when versions already matched")
	}
}

func TestStarterInstantiatesEndToEnd(t *testing.T) {
	// Generality gate (PRD success metric 2): both starters run through the
	// identical engine path; the music template is in no way special.
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	for _, tc := range []struct {
		id        string
		wantFile  string
		wantEntry string
	}{
		{"writing-project", "outline.md", ""},
	} {
		wsDir := t.TempDir()
		tpl, err := FindLibraryTemplate(libDir, tc.id)
		if err != nil {
			t.Fatalf("FindLibraryTemplate(%s): %v", tc.id, err)
		}
		result, err := InstantiateTemplate(tpl, wsDir, "Midnight")
		if err != nil {
			t.Fatalf("Instantiate(%s): %v", tc.id, err)
		}
		target := filepath.Join(wsDir, result.ProjectPath, tc.wantFile)
		if _, err := os.Stat(target); err != nil {
			t.Errorf("%s: expected %s: %v", tc.id, tc.wantFile, err)
		}
		if result.ProjectEntryPath != tc.wantEntry || result.ProjectWarning != "" {
			t.Errorf("%s: project entry = %q warning = %q, want %q without warning", tc.id, result.ProjectEntryPath, result.ProjectWarning, tc.wantEntry)
		}
	}
}
