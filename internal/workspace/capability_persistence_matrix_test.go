package workspace

import (
	"testing"
	"time"
)

// fullyLoadedWorkspace is a workspace carrying every piece of state this feature
// persists, so a round trip has something real to lose.
func fullyLoadedWorkspace(t *testing.T) *Workspace {
	t.Helper()
	ws := &Workspace{ID: "ws-1", Name: "Files", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ws.SetTemplateProvenance(&TemplateProvenance{TemplateID: "downloads-janitor"})

	if _, err := ws.AddInstalledCapability(InstalledCapability{
		ID:          CapabilityFileJanitor,
		Version:     1,
		InstalledAt: time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC),
		Source:      InstallSourceBlueprint,
	}); err != nil {
		t.Fatalf("AddInstalledCapability: %v", err)
	}
	for _, resource := range []CapabilityResource{
		{Kind: ResourceDirectoryReference, ID: "ref-1"},
		{Kind: ResourceMCPBinding, ID: "binding-1"},
		{Kind: ResourceCompanionAgent, ID: "agent-1", Shared: true},
	} {
		if !ws.RecordCapabilityResource(CapabilityFileJanitor, resource) {
			t.Fatalf("RecordCapabilityResource(%v) did not record", resource)
		}
	}
	return ws
}

func assertCapabilityStateIntact(t *testing.T, got *Workspace, where string) {
	t.Helper()
	record, ok := got.GetInstalledCapability(CapabilityFileJanitor)
	if !ok {
		t.Fatalf("%s: the install record was lost", where)
	}
	if record.Version != 1 {
		t.Errorf("%s: version = %d, want 1", where, record.Version)
	}
	if record.Source != InstallSourceBlueprint {
		t.Errorf("%s: source = %q, want the original provenance", where, record.Source)
	}
	if record.InstalledAt.UTC() != time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC) {
		t.Errorf("%s: installed_at = %v, want the original time", where, record.InstalledAt)
	}
	if len(record.OwnedResources) != 3 {
		t.Fatalf("%s: owned resources = %d, want 3 — ownership decides what removal may delete",
			where, len(record.OwnedResources))
	}
	// Sharing is the flag that decides whether removal deletes a resource or
	// merely disassociates from it. Losing it in a round trip would turn a
	// disassociation into a deletion.
	for _, resource := range record.OwnedResources {
		if resource.Kind == ResourceCompanionAgent && !resource.Shared {
			t.Errorf("%s: the shared flag was lost on %v", where, resource)
		}
		if resource.Kind == ResourceDirectoryReference && resource.Shared {
			t.Errorf("%s: an exclusively-owned resource became shared: %v", where, resource)
		}
	}
	if !got.IsFromTemplate("downloads-janitor") {
		t.Errorf("%s: template provenance was lost", where)
	}
}

// A JSON round trip is what every persistence path ultimately performs — the
// folder store writes it, the SQLite mirror stores it as a column, and a
// reload reconstructs the workspace from it.
func TestCapabilityState_SurvivesAJSONRoundTrip(t *testing.T) {
	original := fullyLoadedWorkspace(t)

	data, err := original.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	restored, err := FromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	assertCapabilityStateIntact(t, restored, "json round trip")
}

// Two round trips in a row must be identical to one. A normalization that
// dropped or rewrote something would show up as drift here rather than as a
// mysterious loss weeks later.
func TestCapabilityState_IsStableAcrossRepeatedRoundTrips(t *testing.T) {
	original := fullyLoadedWorkspace(t)

	first, err := original.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	restored, err := FromJSON(first)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	second, err := restored.ToJSON()
	if err != nil {
		t.Fatalf("second ToJSON: %v", err)
	}

	again, err := FromJSON(second)
	if err != nil {
		t.Fatalf("second FromJSON: %v", err)
	}
	assertCapabilityStateIntact(t, again, "second round trip")
}

// A removal must survive persistence too. If the tombstone were dropped on
// reload, the startup migration would re-install the capability on the very
// next boot — which is the failure the tombstone exists to prevent.
func TestRemoval_SurvivesAJSONRoundTrip(t *testing.T) {
	ws := fullyLoadedWorkspace(t)
	if !ws.RemoveInstalledCapability(CapabilityFileJanitor) {
		t.Fatal("removal did not happen")
	}

	data, err := ws.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	restored, err := FromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}

	if restored.HasInstalledCapability(CapabilityFileJanitor) {
		t.Fatal("a removed capability came back after a reload")
	}
	if !restored.CapabilityWasRemoved(CapabilityFileJanitor) {
		t.Fatal("the removal marker did not survive the round trip")
	}
	// Template provenance survives, which is exactly why the marker is needed.
	if !restored.IsFromTemplate("downloads-janitor") {
		t.Error("template provenance should be unaffected by removal")
	}
}

// A reinstall after a persisted removal starts clean: the resources the old
// grant owned must not come back with it.
func TestReinstallAfterReload_DoesNotResurrectTheOldGrant(t *testing.T) {
	ws := fullyLoadedWorkspace(t)
	if !ws.RemoveInstalledCapability(CapabilityFileJanitor) {
		t.Fatal("removal did not happen")
	}
	data, err := ws.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	restored, err := FromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}

	if _, err := restored.AddInstalledCapability(InstalledCapability{
		ID:          CapabilityFileJanitor,
		Version:     1,
		InstalledAt: time.Now(),
		Source:      InstallSourceInPlace,
	}); err != nil {
		t.Fatalf("reinstall: %v", err)
	}

	record, ok := restored.GetInstalledCapability(CapabilityFileJanitor)
	if !ok {
		t.Fatal("the reinstall did not take")
	}
	if len(record.OwnedResources) != 0 {
		t.Errorf("the reinstall resurrected the old grant's resources: %v", record.OwnedResources)
	}
	if record.Source != InstallSourceInPlace {
		t.Errorf("source = %q, want the new install's provenance", record.Source)
	}
}

// Clone is used wherever a workspace is copied between layers. A shallow copy
// would let one caller's mutation of the resource list reach another's.
func TestClone_DeepCopiesCapabilityState(t *testing.T) {
	original := fullyLoadedWorkspace(t)
	copied := CloneInstalledCapabilities(original.AllCapabilityRecords())

	// Mutating the copy must not reach the original.
	copied[0].OwnedResources[0].ID = "mutated"
	copied[0].Version = 99

	record, ok := original.GetInstalledCapability(CapabilityFileJanitor)
	if !ok {
		t.Fatal("original lost its record")
	}
	if record.Version == 99 {
		t.Error("Clone shared the record with its copy")
	}
	for _, resource := range record.OwnedResources {
		if resource.ID == "mutated" {
			t.Error("Clone shared the owned-resource slice with its copy")
		}
	}
}

// A workspace decoded from JSON that never had the field must not be reported
// as having deliberately empty capabilities — that distinction is what stops
// the sync store from erasing a concurrent install (FR-144).
func TestDecodedWorkspace_DoesNotClaimAnExplicitEmptyCollection(t *testing.T) {
	restored, err := FromJSON([]byte(`{"id":"ws-1","name":"Files"}`))
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if restored.InstalledCapabilitiesExplicit() {
		t.Fatal("a workspace that never carried the field must not look deliberately emptied")
	}

	// Whereas one that was deliberately emptied does say so.
	restored.SetInstalledCapabilities(nil)
	if !restored.InstalledCapabilitiesExplicit() {
		t.Fatal("an explicit edit must be reported as one")
	}
}
