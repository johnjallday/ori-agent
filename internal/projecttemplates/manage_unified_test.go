package projecttemplates

import (
	"errors"
	"path/filepath"
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
