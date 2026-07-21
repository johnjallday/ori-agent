package projecttemplates

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestNewTemplate_LoadsAndNormalizesCapabilityRequirements(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "demo", ManifestFileName), `{
		"name": "Demo",
		"capability_requirements": [
			{"key": " Calendar ", "required_operations": [" List_Calendars ", "list_events", "list_events"], "optional_operations": ["  ", "create_event"]}
		]
	}`)

	tpl, err := FindLibraryTemplate(dir, "demo")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if len(tpl.CapabilityRequirements) != 1 {
		t.Fatalf("expected 1 capability requirement, got %d: %+v", len(tpl.CapabilityRequirements), tpl.CapabilityRequirements)
	}
	req := tpl.CapabilityRequirements[0]
	if req.Key != "calendar" {
		t.Fatalf("expected trimmed/lower-cased key, got %q", req.Key)
	}
	if len(req.RequiredOperations) != 2 || req.RequiredOperations[0] != "list_calendars" || req.RequiredOperations[1] != "list_events" {
		t.Fatalf("expected normalized+deduped required operations, got: %v", req.RequiredOperations)
	}
	if len(req.OptionalOperations) != 1 || req.OptionalOperations[0] != "create_event" {
		t.Fatalf("expected blank optional operation dropped, got: %v", req.OptionalOperations)
	}
}

func TestNewTemplate_OlderManifestWithNoCapabilityRequirementsIsUnaffected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "legacy", ManifestFileName), `{"name":"Legacy"}`)

	tpl, err := FindLibraryTemplate(dir, "legacy")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if tpl.CapabilityRequirements != nil {
		t.Fatalf("expected nil capability requirements for a manifest that predates the field, got: %+v", tpl.CapabilityRequirements)
	}
}

func TestUpdateManifest_CapabilityRequirementsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateBlank(dir, "Demo"); err != nil {
		t.Fatalf("CreateBlank: %v", err)
	}

	reqs := []CapabilityRequirement{
		{Key: "calendar", RequiredOperations: []string{"list_calendars", "list_events"}, OptionalOperations: []string{"create_event"}},
	}
	tpl, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{CapabilityRequirements: &reqs})
	if err != nil {
		t.Fatalf("UpdateManifest: %v", err)
	}
	if len(tpl.CapabilityRequirements) != 1 || tpl.CapabilityRequirements[0].Key != "calendar" {
		t.Fatalf("capability requirements not applied: %+v", tpl.CapabilityRequirements)
	}

	reread, err := FindLibraryTemplate(dir, "demo")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if len(reread.CapabilityRequirements) != 1 {
		t.Fatalf("capability requirements did not persist: %+v", reread.CapabilityRequirements)
	}

	// Clearing with an empty slice removes the key.
	empty := []CapabilityRequirement{}
	tpl, err = UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{CapabilityRequirements: &empty})
	if err != nil {
		t.Fatalf("UpdateManifest(clear): %v", err)
	}
	if len(tpl.CapabilityRequirements) != 0 {
		t.Fatalf("expected capability requirements cleared, got: %+v", tpl.CapabilityRequirements)
	}
}

func TestUpdateManifest_RejectsBlankCapabilityKey(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateBlank(dir, "Demo"); err != nil {
		t.Fatalf("CreateBlank: %v", err)
	}

	reqs := []CapabilityRequirement{{Key: "  ", RequiredOperations: []string{"list_events"}}}
	_, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{CapabilityRequirements: &reqs})
	if !errors.Is(err, ErrInvalidCapabilityRequirements) {
		t.Fatalf("expected ErrInvalidCapabilityRequirements, got %v", err)
	}
}

func TestUpdateManifest_RejectsDuplicateCapabilityKey(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateBlank(dir, "Demo"); err != nil {
		t.Fatalf("CreateBlank: %v", err)
	}

	reqs := []CapabilityRequirement{
		{Key: "calendar", RequiredOperations: []string{"list_events"}},
		{Key: "Calendar", RequiredOperations: []string{"list_calendars"}},
	}
	_, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{CapabilityRequirements: &reqs})
	if !errors.Is(err, ErrInvalidCapabilityRequirements) {
		t.Fatalf("expected ErrInvalidCapabilityRequirements for duplicate keys, got %v", err)
	}
}

func TestUpdateManifest_RejectsBlankOperationName(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateBlank(dir, "Demo"); err != nil {
		t.Fatalf("CreateBlank: %v", err)
	}

	reqs := []CapabilityRequirement{{Key: "calendar", RequiredOperations: []string{"  "}}}
	_, err := UpdateManifest(dir, "demo", "Demo", "", nil, &ManifestEdit{CapabilityRequirements: &reqs})
	if !errors.Is(err, ErrInvalidCapabilityRequirements) {
		t.Fatalf("expected ErrInvalidCapabilityRequirements for a blank operation name, got %v", err)
	}
}

func TestDuplicate_PreservesCapabilityRequirements(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "source", ManifestFileName), `{
		"name": "Source",
		"capability_requirements": [{"key": "calendar", "required_operations": ["list_calendars", "list_events"]}]
	}`)

	dup, err := Duplicate(dir, "source", "Source Copy")
	if err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	if len(dup.CapabilityRequirements) != 1 || dup.CapabilityRequirements[0].Key != "calendar" {
		t.Fatalf("expected duplicate to preserve capability requirements, got: %+v", dup.CapabilityRequirements)
	}
}
