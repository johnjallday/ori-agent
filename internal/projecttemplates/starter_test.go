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

	// The file-scaffold starter and metadata-only built-ins all materialize.
	for _, id := range []string{
		"writing-project", "travels", "daily-briefings", "content-production", "research-project", "personal-ops", "code-project",
	} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("missing starter %q; got %v", id, templates)
		}
	}

	// Code Project is the workspace "show me a folder" sets up over a software
	// project: metadata only, no scaffold to supersede, and exactly one
	// development assistant as its entry agent.
	code := byID["code-project"]
	if !code.Builtin || code.HasSkeleton || len(code.Tags) != 1 || code.Tags[0] != "code" || code.Icon == "" || code.BuiltinVersion != 2 {
		t.Errorf("code-project: %+v", code)
	}
	if len(code.Agents) != 1 || code.Agents[0].Name != "Development Assistant" || code.Agents[0].Role != "general" ||
		code.Agents[0].Model != "" || code.Agents[0].Provider != "" || !code.Agents[0].Tools.IsEmpty() {
		t.Errorf("code-project roster: %+v", code.Agents)
	}
	// The assistant's own instructions keep it to understanding and planning and
	// to what it can show: it is told not to report an unrun command as passing.
	prompt := code.Agents[0].SystemPrompt
	for _, required := range []string{"read-only directory tools", "this project's conventions", "declared, not tested", "say plainly what you have not verified"} {
		if !strings.Contains(prompt, required) {
			t.Errorf("code-project assistant prompt lacks %q", required)
		}
	}
	if len(code.StarterTasks) != 0 || len(code.CapabilityRequirements) != 0 || code.SetupWizard != nil {
		t.Errorf("code-project gained tasks, capabilities or a wizard: %+v", code)
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

// An install that already has the agent-less Code Project (version 1) gets the
// shipped roster on the next start. Only that built-in's manifest is rewritten:
// other files in its folder, an edited built-in, and a user's own template that
// happens to look like it are left exactly as they are.
func TestEnsureLibraryUpgradesAnOlderCodeProjectAndNothingElse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	code := filepath.Join(dir, "code-project")
	older := `{"name":"Code Project","description":"A workspace over a code folder you already have.","icon":"💻","behavior_profile":"general","builtin":true,"builtin_version":1,"tags":["code"],"agents":[]}`
	if err := os.WriteFile(filepath.Join(code, ManifestFileName), []byte(older), 0o640); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(code, "my-notes.md")
	if err := os.WriteFile(notes, []byte("kept by the user"), 0o640); err != nil {
		t.Fatal(err)
	}
	// A template the user made, with an agent named like the shipped one.
	custom := filepath.Join(dir, "my-code-project")
	if err := os.MkdirAll(custom, 0o750); err != nil {
		t.Fatal(err)
	}
	customManifest := `{"name":"My Code Project","builtin_version":1,"agents":[{"name":"Development Assistant","role":"general","system_prompt":"Mine."}]}`
	if err := os.WriteFile(filepath.Join(custom, ManifestFileName), []byte(customManifest), 0o640); err != nil {
		t.Fatal(err)
	}
	// Another built-in the user has not been given a newer version of.
	research, err := os.ReadFile(filepath.Join(dir, "research-project", ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}

	if before, err := FindLibraryTemplate(dir, "code-project"); err != nil || before.HasAgents() || before.BuiltinVersion != 1 {
		t.Fatalf("the older install was not simulated: %+v %v", before, err)
	}
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary (upgrade): %v", err)
	}
	upgraded, err := FindLibraryTemplate(dir, "code-project")
	if err != nil || upgraded.BuiltinVersion != 2 || len(upgraded.Agents) != 1 || upgraded.Agents[0].Name != "Development Assistant" {
		t.Fatalf("code-project after upgrade: %+v %v", upgraded, err)
	}
	if data, err := os.ReadFile(notes); err != nil || string(data) != "kept by the user" {
		t.Fatalf("a file beside the manifest was touched: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(custom, ManifestFileName)); err != nil || string(data) != customManifest {
		t.Fatalf("the user's own template was rewritten: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "research-project", ManifestFileName)); err != nil || string(data) != string(research) {
		t.Fatalf("an up-to-date built-in was rewritten: %v", err)
	}

	// A built-in the user edited at the current version is not replaced.
	edited := strings.Replace(mustReadFile(t, filepath.Join(code, ManifestFileName)), "Development Assistant", "Pat", 1)
	if err := os.WriteFile(filepath.Join(code, ManifestFileName), []byte(edited), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary (no-op): %v", err)
	}
	if kept, err := FindLibraryTemplate(dir, "code-project"); err != nil || len(kept.Agents) != 1 || kept.Agents[0].Name != "Pat" {
		t.Fatalf("an edited current built-in was overwritten: %+v %v", kept.Agents, err)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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
