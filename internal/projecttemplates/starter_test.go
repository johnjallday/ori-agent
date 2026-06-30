package projecttemplates

import (
	"os"
	"path/filepath"
	"strings"
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

	// The two file-scaffold starters and the five metadata-only built-ins all
	// materialize on a fresh library.
	for _, id := range []string{
		"reaper-song", "writing-project",
		"travels", "daily-briefings", "content-production", "research-project", "personal-ops",
	} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("missing starter %q; got %v", id, templates)
		}
	}

	// Scaffold starters keep their files, onboarding, and are flagged builtin.
	reaper := byID["reaper-song"]
	if reaper.Name != "Reaper Song" || !reaper.Builtin || !reaper.HasSkeleton {
		t.Errorf("reaper-song: %+v", reaper)
	}
	if len(reaper.Tags) != 2 || reaper.Tags[0] != "music" || reaper.Tags[1] != "reaper" {
		t.Errorf("reaper starter tags not applied: %+v", reaper.Tags)
	}
	if !reaper.HasOnboarding() {
		t.Error("reaper starter should carry template onboarding")
	}
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

	// Seed file with token name plus the dot-file under chapters/ made it out
	// of the embed (all: prefix) and onto disk.
	if _, err := os.Stat(filepath.Join(dir, "reaper-song", "{{name}}.rpp")); err != nil {
		t.Errorf("reaper seed missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "writing-project", "chapters", ".keep")); err != nil {
		t.Errorf("chapters/.keep missing: %v", err)
	}
}

func TestEnsureLibraryNeverOverwritesUserEdits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	edited := filepath.Join(dir, "reaper-song", "{{name}}.rpp")
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

func TestStarterInstantiatesEndToEnd(t *testing.T) {
	// Generality gate (PRD success metric 2): both starters run through the
	// identical engine path; the music template is in no way special.
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	for _, tc := range []struct {
		id       string
		wantFile string
	}{
		{"reaper-song", "midnight.rpp"},
		{"writing-project", "outline.md"},
	} {
		wsDir := t.TempDir()
		tpl, err := FindLibraryTemplate(libDir, tc.id)
		if err != nil {
			t.Fatalf("FindLibraryTemplate(%s): %v", tc.id, err)
		}
		rel, err := Instantiate(tpl.Path, wsDir, "Midnight")
		if err != nil {
			t.Fatalf("Instantiate(%s): %v", tc.id, err)
		}
		target := filepath.Join(wsDir, rel, tc.wantFile)
		if _, err := os.Stat(target); err != nil {
			t.Errorf("%s: expected %s: %v", tc.id, tc.wantFile, err)
		}
	}
}

func TestReaperSeedLooksLikeAnRPP(t *testing.T) {
	data, err := starterFS.ReadFile("starter/reaper-song/{{name}}.rpp")
	if err != nil {
		t.Fatalf("embedded seed missing: %v", err)
	}
	if !strings.HasPrefix(string(data), "<REAPER_PROJECT") {
		t.Errorf("seed does not open with <REAPER_PROJECT: %q", data)
	}
}
