package projecttemplates

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureMutableRejectsBuiltin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "shipped", ManifestFileName), `{"name":"Shipped","builtin":true}`)
	writeFile(t, filepath.Join(dir, "mine", ManifestFileName), `{"name":"Mine"}`)

	if err := EnsureMutable(dir, "shipped"); !errors.Is(err, ErrTemplateReadOnly) {
		t.Errorf("EnsureMutable(builtin) = %v, want ErrTemplateReadOnly", err)
	}
	if err := EnsureMutable(dir, "mine"); err != nil {
		t.Errorf("EnsureMutable(user) = %v, want nil", err)
	}
	if err := EnsureMutable(dir, "missing"); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("EnsureMutable(missing) = %v, want ErrTemplateNotFound", err)
	}
}

// TestDuplicateStripsBuiltin covers "Duplicate to customize": a copy of a
// built-in must be an editable user template, not another built-in.
func TestDuplicateStripsBuiltin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "shipped", ManifestFileName),
		`{"name":"Shipped","builtin":true,"icon":"✈","behavior_profile":"research","starter_tasks":[{"description":"Do a thing"}]}`)

	dup, err := Duplicate(dir, "shipped", "My Copy")
	if err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	if dup.Builtin {
		t.Errorf("duplicate is still builtin: %+v", dup)
	}
	// Other fields carry over (icon/behavior/tasks copied verbatim).
	if dup.Icon != "✈" || dup.BehaviorProfile != BehaviorProfileResearch || len(dup.StarterTasks) != 1 {
		t.Errorf("duplicate lost carried fields: %+v", dup)
	}
	// And the copy is now mutable.
	if err := EnsureMutable(dir, dup.ID); err != nil {
		t.Errorf("duplicate should be mutable, got %v", err)
	}
}

func TestUpdateManifestMetaRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "demo", ManifestFileName), `{"name":"Demo"}`)

	icon := "📚"
	profile := "research"
	tasks := []StarterTask{
		{Description: "  Build the doc  ", Details: "  capture the question  "},
		{Description: "   ", Details: "dropped: blank"},
	}
	tpl, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{
		Icon:            &icon,
		BehaviorProfile: &profile,
		StarterTasks:    &tasks,
	})
	if err != nil {
		t.Fatalf("UpdateManifest(meta): %v", err)
	}
	if tpl.Icon != "📚" || tpl.BehaviorProfile != BehaviorProfileResearch {
		t.Errorf("meta not applied: %+v", tpl)
	}
	if len(tpl.StarterTasks) != 1 || tpl.StarterTasks[0].Description != "Build the doc" || tpl.StarterTasks[0].Details != "capture the question" {
		t.Errorf("starter tasks not normalized: %+v", tpl.StarterTasks)
	}

	// Re-read from disk to confirm persistence.
	reread, err := FindLibraryTemplate(dir, "demo")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if reread.Icon != "📚" || reread.BehaviorProfile != BehaviorProfileResearch || len(reread.StarterTasks) != 1 {
		t.Errorf("meta did not persist: %+v", reread)
	}

	// Clearing icon (empty pointer) and starter_tasks (empty slice) removes them.
	empty := ""
	noTasks := []StarterTask{}
	tpl, err = UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{
		Icon:         &empty,
		StarterTasks: &noTasks,
	})
	if err != nil {
		t.Fatalf("UpdateManifest(clear meta): %v", err)
	}
	if tpl.Icon != "" || len(tpl.StarterTasks) != 0 {
		t.Errorf("meta not cleared: %+v", tpl)
	}
	// behavior_profile was not in this edit (nil) so it is preserved.
	if tpl.BehaviorProfile != BehaviorProfileResearch {
		t.Errorf("behavior_profile should be preserved, got %q", tpl.BehaviorProfile)
	}
}

func TestUpdateManifestRejectsMultipleSetupTasks(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateBlank(dir, "Demo"); err != nil {
		t.Fatalf("CreateBlank: %v", err)
	}

	_, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{
		StarterTasks: &[]StarterTask{
			{Description: "one", Setup: true},
			{Description: "two", Setup: true},
		},
	})
	if !errors.Is(err, ErrInvalidStarterTasks) {
		t.Fatalf("expected ErrInvalidStarterTasks, got %v", err)
	}

	// A single setup task saves and round-trips.
	tpl, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{
		StarterTasks: &[]StarterTask{
			{Description: "one", Setup: true},
			{Description: "two"},
		},
	})
	if err != nil {
		t.Fatalf("UpdateManifest(setup): %v", err)
	}
	if len(tpl.StarterTasks) != 2 || !tpl.StarterTasks[0].Setup || tpl.StarterTasks[1].Setup {
		t.Fatalf("setup flag did not persist correctly: %+v", tpl.StarterTasks)
	}
}

func TestUpdateManifestStripsLegacyOnboarding(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "legacy", ManifestFileName),
		`{"name":"Legacy","custom_key":"kept","onboarding":{"version":"1","fields":[]}}`)

	tpl, err := UpdateManifest(dir, "legacy", "Legacy", "still legacy", nil, nil)
	if err != nil {
		t.Fatalf("UpdateManifest: %v", err)
	}
	if tpl.HasOnboarding() {
		t.Fatalf("expected onboarding stripped from reloaded template")
	}

	data, err := os.ReadFile(filepath.Join(dir, "legacy", ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"onboarding"`) {
		t.Errorf("legacy onboarding key should be removed on save: %s", data)
	}
	// Unknown keys the author added are still preserved.
	if !strings.Contains(string(data), `"custom_key"`) {
		t.Errorf("unknown keys should survive the save: %s", data)
	}
}
