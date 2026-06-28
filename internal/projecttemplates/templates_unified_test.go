package projecttemplates

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestNewTemplateParsesUnifiedFields covers the manifest fields added for the
// unified-templates merge: icon, behavior_profile, starter_tasks, builtin.
func TestNewTemplateParsesUnifiedFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "travels", ManifestFileName), `{
		"name": "Travels",
		"description": "Flights, hotels, trip planning.",
		"icon": "✈",
		"behavior_profile": "research",
		"builtin": true,
		"starter_tasks": [
			{"description": "Plan an upcoming trip", "details": "destination, dates, budget"},
			{"description": "  ", "details": "dropped: blank description"},
			{"description": "  Compile reading list  ", "details": "  trim me  "}
		]
	}`)

	tpl, err := FindLibraryTemplate(dir, "travels")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if tpl.Icon != "✈" {
		t.Errorf("Icon = %q, want ✈", tpl.Icon)
	}
	if tpl.BehaviorProfile != BehaviorProfileResearch {
		t.Errorf("BehaviorProfile = %q, want research", tpl.BehaviorProfile)
	}
	if !tpl.Builtin {
		t.Error("Builtin = false, want true")
	}
	// Blank-description task dropped; remaining trimmed.
	if len(tpl.StarterTasks) != 2 {
		t.Fatalf("StarterTasks len = %d, want 2: %+v", len(tpl.StarterTasks), tpl.StarterTasks)
	}
	if got := tpl.StarterTasks[1]; got.Description != "Compile reading list" || got.Details != "trim me" {
		t.Errorf("StarterTasks[1] not trimmed: %+v", got)
	}
	// A manifest-only template is metadata-only.
	if tpl.HasSkeleton {
		t.Error("HasSkeleton = true for a manifest-only template, want false")
	}
}

func TestNormalizeBehaviorProfile(t *testing.T) {
	cases := map[string]string{
		"general":          BehaviorProfileGeneral,
		"research":         BehaviorProfileResearch,
		"software_project": BehaviorProfileSoftwareProject,
		"  Research  ":     BehaviorProfileResearch, // trimmed + lowercased
		"":                 BehaviorProfileGeneral,  // empty → general
		"nonsense":         BehaviorProfileGeneral,  // unknown → general
	}
	for in, want := range cases {
		if got := NormalizeBehaviorProfile(in); got != want {
			t.Errorf("NormalizeBehaviorProfile(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHasSkeletonViaListLibrary(t *testing.T) {
	dir := t.TempDir()
	// Metadata-only: manifest only.
	writeFile(t, filepath.Join(dir, "meta", ManifestFileName), `{"name":"Meta"}`)
	// Scaffold: a real file beyond the manifest.
	writeFile(t, filepath.Join(dir, "scaffold", ManifestFileName), `{"name":"Scaffold"}`)
	writeFile(t, filepath.Join(dir, "scaffold", "seed.txt"), "x")

	templates, err := ListLibrary(dir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	byID := map[string]Template{}
	for _, tpl := range templates {
		byID[tpl.ID] = tpl
	}
	if byID["meta"].HasSkeleton {
		t.Error("meta: HasSkeleton = true, want false")
	}
	if !byID["scaffold"].HasSkeleton {
		t.Error("scaffold: HasSkeleton = false, want true")
	}
}

// TestInstantiateMetadataOnlyReturnsErrNoSkeleton guards the create flow: a
// metadata-only template must never silently produce an empty project folder.
func TestInstantiateMetadataOnlyReturnsErrNoSkeleton(t *testing.T) {
	dir := t.TempDir()
	tplDir := filepath.Join(dir, "meta")
	writeFile(t, filepath.Join(tplDir, ManifestFileName), `{"name":"Meta"}`)
	wsDir := t.TempDir()

	if _, err := Instantiate(tplDir, wsDir, "My Project"); !errors.Is(err, ErrNoSkeleton) {
		t.Errorf("Instantiate(metadata-only) = %v, want ErrNoSkeleton", err)
	}
}
