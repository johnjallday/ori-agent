package projecttemplates

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestListLibraryMissingDirIsEmpty(t *testing.T) {
	templates, err := ListLibrary(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	if len(templates) != 0 {
		t.Fatalf("expected empty library, got %d entries", len(templates))
	}
}

func TestListLibraryDiscoversPlainFolders(t *testing.T) {
	dir := t.TempDir()
	// Plain folder with no manifest must appear under its folder name
	// (PRD success metric 3: authoring = dropping a folder in).
	writeFile(t, filepath.Join(dir, "my-layout", "seed.txt"), "x")
	// Manifest overrides display name and adds metadata.
	writeFile(t, filepath.Join(dir, "fancy", ManifestFileName), `{"name":"Fancy Pack","description":"desc here","tags":[" Music ","music","REAPER"],"unknown_field":true}`)
	// Non-directories and hidden folders are not templates.
	writeFile(t, filepath.Join(dir, "stray.txt"), "x")
	if err := os.MkdirAll(filepath.Join(dir, ".hidden"), 0o750); err != nil {
		t.Fatal(err)
	}

	templates, err := ListLibrary(dir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	if len(templates) != 2 {
		t.Fatalf("expected 2 templates, got %d: %+v", len(templates), templates)
	}
	if templates[0].ID != "fancy" || templates[0].Name != "Fancy Pack" || templates[0].Description != "desc here" {
		t.Errorf("manifest template wrong: %+v", templates[0])
	}
	if len(templates[0].Tags) != 2 || templates[0].Tags[0] != "music" || templates[0].Tags[1] != "reaper" {
		t.Errorf("manifest tags wrong: %+v", templates[0].Tags)
	}
	if templates[1].ID != "my-layout" || templates[1].Name != "my-layout" || templates[1].Description != "" {
		t.Errorf("plain template wrong: %+v", templates[1])
	}
}

func TestListLibraryMalformedManifestFallsBack(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "broken", ManifestFileName), `{not json`)

	templates, err := ListLibrary(dir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	if len(templates) != 1 || templates[0].Name != "broken" {
		t.Fatalf("expected fallback to folder name, got %+v", templates)
	}
}

func TestListLibraryPreservesOnboardingRawAndToleratesGarbage(t *testing.T) {
	dir := t.TempDir()
	// Valid onboarding block: preserved verbatim, display metadata intact.
	writeFile(t, filepath.Join(dir, "withonb", ManifestFileName),
		`{"name":"With","onboarding":{"version":"1","completion":{"type":"none"}}}`)
	// Garbage onboarding (a string, not an object): this package must NOT
	// interpret it — the template still lists with its display name, and the raw
	// bytes are carried only for warning detection and strip-on-save.
	writeFile(t, filepath.Join(dir, "badonb", ManifestFileName),
		`{"name":"Bad","onboarding":"not-an-object"}`)
	// No onboarding key at all.
	writeFile(t, filepath.Join(dir, "plain", "seed.txt"), "x")

	templates, err := ListLibrary(dir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	byID := map[string]Template{}
	for _, tpl := range templates {
		byID[tpl.ID] = tpl
	}

	if got := byID["withonb"]; got.Name != "With" || !got.HasOnboarding() {
		t.Errorf("withonb: expected display name + onboarding present, got %+v", got)
	}
	if got := byID["badonb"]; got.Name != "Bad" || !got.HasOnboarding() {
		t.Errorf("badonb: garbage onboarding must still list with display name + raw bytes, got %+v", got)
	}
	if got := byID["plain"]; got.Name != "plain" || got.HasOnboarding() {
		t.Errorf("plain: expected no onboarding, got %+v", got)
	}
}

func TestFindLibraryTemplate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "good", "a.txt"), "x")

	tpl, err := FindLibraryTemplate(dir, "good")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if tpl.ID != "good" || tpl.Path != filepath.Join(dir, "good") {
		t.Errorf("unexpected template: %+v", tpl)
	}

	for _, id := range []string{"missing", "", "../good", "good/../good", ".hidden"} {
		if _, err := FindLibraryTemplate(dir, id); !errors.Is(err, ErrTemplateNotFound) {
			t.Errorf("FindLibraryTemplate(%q): expected ErrTemplateNotFound, got %v", id, err)
		}
	}
}

func TestLoadFolder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "anywhere", ManifestFileName), `{"name":"Anywhere"}`)

	tpl, err := LoadFolder(filepath.Join(dir, "anywhere"))
	if err != nil {
		t.Fatalf("LoadFolder: %v", err)
	}
	if tpl.Name != "Anywhere" || tpl.ID != "anywhere" {
		t.Errorf("unexpected template: %+v", tpl)
	}

	if _, err := LoadFolder(filepath.Join(dir, "nope")); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("expected ErrTemplateNotFound for missing folder, got %v", err)
	}
	if _, err := LoadFolder(filepath.Join(dir, "anywhere", ManifestFileName)); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("expected ErrTemplateNotFound for file path, got %v", err)
	}
}

func TestStarterTaskSetupFlagRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "song", ManifestFileName),
		`{"name":"Song","starter_tasks":[{"description":"Adjust tempo","details":"## Questions to ask","setup":true},{"description":"Write lyrics"}]}`)

	tpl, err := FindLibraryTemplate(dir, "song")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if len(tpl.StarterTasks) != 2 {
		t.Fatalf("expected 2 starter tasks, got %+v", tpl.StarterTasks)
	}
	if !tpl.StarterTasks[0].Setup || tpl.StarterTasks[1].Setup {
		t.Fatalf("setup flag not carried: %+v", tpl.StarterTasks)
	}
}

func TestNormalizeStarterTasksDemotesExtraSetupFlags(t *testing.T) {
	// A hand-edited manifest with two setup tasks loads (never fails), but only
	// the first flag survives so downstream seeding sees at most one setup task.
	out := normalizeStarterTasks([]StarterTask{
		{Description: "", Setup: true}, // dropped: no description, flag dies with it
		{Description: "first", Setup: true},
		{Description: "second", Setup: true},
	})
	if len(out) != 2 {
		t.Fatalf("expected 2 tasks, got %+v", out)
	}
	if !out[0].Setup || out[1].Setup {
		t.Fatalf("expected only the first surviving setup flag to be kept: %+v", out)
	}
}

func TestValidateStarterTasksRejectsMultipleSetup(t *testing.T) {
	if err := validateStarterTasks([]StarterTask{{Description: "a", Setup: true}, {Description: "b"}}); err != nil {
		t.Fatalf("single setup task should validate, got %v", err)
	}
	err := validateStarterTasks([]StarterTask{{Description: "a", Setup: true}, {Description: "b", Setup: true}})
	if !errors.Is(err, ErrInvalidStarterTasks) {
		t.Fatalf("expected ErrInvalidStarterTasks, got %v", err)
	}
	for _, want := range []string{`"a"`, `"b"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name offending task %s, got %q", want, err.Error())
		}
	}
}

func TestManifestWarnings(t *testing.T) {
	dir := t.TempDir()
	// Legacy onboarding block + no roster → both warnings.
	writeFile(t, filepath.Join(dir, "legacy", ManifestFileName), `{"name":"Legacy","onboarding":{"version":"1"}}`)
	// Roster present, no onboarding → no warnings.
	writeFile(t, filepath.Join(dir, "clean", ManifestFileName), `{"name":"Clean","agents":[{"name":"Lead"}]}`)

	legacy, err := FindLibraryTemplate(dir, "legacy")
	if err != nil {
		t.Fatalf("FindLibraryTemplate(legacy): %v", err)
	}
	if len(legacy.Warnings) != 2 {
		t.Fatalf("expected onboarding + roster warnings, got %v", legacy.Warnings)
	}
	if !strings.Contains(legacy.Warnings[0], "onboarding") || !strings.Contains(legacy.Warnings[1], "agents") {
		t.Fatalf("unexpected warning wording: %v", legacy.Warnings)
	}

	clean, err := FindLibraryTemplate(dir, "clean")
	if err != nil {
		t.Fatalf("FindLibraryTemplate(clean): %v", err)
	}
	if len(clean.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", clean.Warnings)
	}
}
