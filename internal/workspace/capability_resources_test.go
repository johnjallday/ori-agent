package workspace

import (
	"encoding/json"
	"testing"
	"time"
)

func installedWorkspace(t *testing.T) *Workspace {
	t.Helper()
	ws := &Workspace{ID: "ws-1", Name: "Inbox"}
	if _, err := ws.AddInstalledCapability(InstalledCapability{
		ID:          CapabilityFileJanitor,
		Version:     1,
		InstalledAt: time.Now(),
		Source:      InstallSourceInPlace,
	}); err != nil {
		t.Fatalf("install: %v", err)
	}
	return ws
}

// TestRecordCapabilityResource_DistinguishesExclusiveFromShared is the FR-27
// contract. Removal may delete a resource this capability created; it may only
// drop its association with one it merely uses. Nothing here consults a display
// name — that is the whole point (PRD §9.5).
func TestRecordCapabilityResource_DistinguishesExclusiveFromShared(t *testing.T) {
	ws := installedWorkspace(t)

	if !ws.RecordCapabilityResource(CapabilityFileJanitor, CapabilityResource{
		Kind: ResourceMCPBinding, ID: "binding-1",
	}) {
		t.Fatal("recording an exclusive resource reported no change")
	}
	if !ws.RecordCapabilityResource(CapabilityFileJanitor, CapabilityResource{
		Kind: ResourceDirectoryReference, ID: "dir-1", Shared: true,
	}) {
		t.Fatal("recording a shared resource reported no change")
	}

	record, ok := ws.GetInstalledCapability(CapabilityFileJanitor)
	if !ok {
		t.Fatal("capability missing")
	}

	exclusive, recorded := record.Owns(ResourceMCPBinding, "binding-1")
	if !recorded || !exclusive {
		t.Fatalf("binding should be exclusively owned: exclusive=%v recorded=%v", exclusive, recorded)
	}
	exclusive, recorded = record.Owns(ResourceDirectoryReference, "dir-1")
	if !recorded {
		t.Fatal("shared directory reference was not recorded")
	}
	if exclusive {
		t.Fatal("a shared resource must not be reported as exclusively owned")
	}

	// A resource nobody recorded is not owned, however plausible its id.
	if _, recorded := record.Owns(ResourceDirectoryReference, "dir-999"); recorded {
		t.Fatal("an unrecorded resource reported as owned")
	}
}

func TestRecordCapabilityResource_IsIdempotentAndUpdatesInPlace(t *testing.T) {
	ws := installedWorkspace(t)

	ws.RecordCapabilityResource(CapabilityFileJanitor, CapabilityResource{Kind: ResourceMCPBinding, ID: "binding-1"})
	ws.RecordCapabilityResource(CapabilityFileJanitor, CapabilityResource{Kind: ResourceMCPBinding, ID: "binding-1"})
	ws.RecordCapabilityResource(CapabilityFileJanitor, CapabilityResource{Kind: ResourceMCPBinding, ID: "binding-1"})

	record, _ := ws.GetInstalledCapability(CapabilityFileJanitor)
	if got := len(record.ResourcesOfKind(ResourceMCPBinding)); got != 1 {
		t.Fatalf("expected one binding record, got %d", got)
	}

	// Re-recording with a different sharing status updates rather than adding.
	ws.RecordCapabilityResource(CapabilityFileJanitor, CapabilityResource{
		Kind: ResourceMCPBinding, ID: "binding-1", Shared: true,
	})
	record, _ = ws.GetInstalledCapability(CapabilityFileJanitor)
	if got := len(record.OwnedResources); got != 1 {
		t.Fatalf("expected the entry to be replaced, got %d records", got)
	}
	if exclusive, _ := record.Owns(ResourceMCPBinding, "binding-1"); exclusive {
		t.Fatal("sharing status was not updated")
	}
}

func TestRecordCapabilityResource_RejectsUnusableInput(t *testing.T) {
	ws := installedWorkspace(t)

	cases := []struct {
		name     string
		capID    string
		resource CapabilityResource
	}{
		{"blank capability", "", CapabilityResource{Kind: ResourceMCPBinding, ID: "b1"}},
		{"unknown capability", "not-installed", CapabilityResource{Kind: ResourceMCPBinding, ID: "b1"}},
		{"blank kind", CapabilityFileJanitor, CapabilityResource{Kind: "  ", ID: "b1"}},
		{"unknown kind", CapabilityFileJanitor, CapabilityResource{Kind: "database_table", ID: "b1"}},
		{"blank id", CapabilityFileJanitor, CapabilityResource{Kind: ResourceMCPBinding, ID: "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if ws.RecordCapabilityResource(tc.capID, tc.resource) {
				t.Fatal("expected the record to be rejected")
			}
		})
	}

	record, _ := ws.GetInstalledCapability(CapabilityFileJanitor)
	if len(record.OwnedResources) != 0 {
		t.Fatalf("rejected inputs still recorded something: %+v", record.OwnedResources)
	}
}

func TestForgetCapabilityResource_DropsOnlyTheNamedResource(t *testing.T) {
	ws := installedWorkspace(t)
	ws.RecordCapabilityResource(CapabilityFileJanitor, CapabilityResource{Kind: ResourceMCPBinding, ID: "binding-1"})
	ws.RecordCapabilityResource(CapabilityFileJanitor, CapabilityResource{Kind: ResourceDirectoryReference, ID: "dir-1"})
	ws.RecordCapabilityResource(CapabilityFileJanitor, CapabilityResource{Kind: ResourceWatcher, ID: "watch-1"})

	if !ws.ForgetCapabilityResource(CapabilityFileJanitor, ResourceDirectoryReference, "dir-1") {
		t.Fatal("forgetting a recorded resource reported no change")
	}
	if ws.ForgetCapabilityResource(CapabilityFileJanitor, ResourceDirectoryReference, "dir-1") {
		t.Fatal("forgetting an already-forgotten resource reported a change")
	}

	record, _ := ws.GetInstalledCapability(CapabilityFileJanitor)
	if _, recorded := record.Owns(ResourceDirectoryReference, "dir-1"); recorded {
		t.Fatal("the resource was not dropped")
	}
	if _, recorded := record.Owns(ResourceMCPBinding, "binding-1"); !recorded {
		t.Fatal("an unrelated resource was dropped")
	}
	if _, recorded := record.Owns(ResourceWatcher, "watch-1"); !recorded {
		t.Fatal("an unrelated resource was dropped")
	}
}

// TestCapabilityResources_SurviveAJSONRoundTrip keeps ownership durable: it is
// read at uninstall, which may happen many restarts after setup.
func TestCapabilityResources_SurviveAJSONRoundTrip(t *testing.T) {
	ws := installedWorkspace(t)
	ws.RecordCapabilityResource(CapabilityFileJanitor, CapabilityResource{Kind: ResourceMCPBinding, ID: "binding-1"})
	ws.RecordCapabilityResource(CapabilityFileJanitor, CapabilityResource{Kind: ResourceDirectoryReference, ID: "dir-1", Shared: true})

	data, err := ws.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	decoded, err := FromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}

	record, ok := decoded.GetInstalledCapability(CapabilityFileJanitor)
	if !ok {
		t.Fatal("capability lost")
	}
	if exclusive, recorded := record.Owns(ResourceMCPBinding, "binding-1"); !recorded || !exclusive {
		t.Fatalf("exclusive ownership lost: exclusive=%v recorded=%v", exclusive, recorded)
	}
	if exclusive, recorded := record.Owns(ResourceDirectoryReference, "dir-1"); !recorded || exclusive {
		t.Fatalf("shared marker lost: exclusive=%v recorded=%v", exclusive, recorded)
	}
}

// TestCapabilityResources_NormalizationDropsUnknownKinds guards the decode
// path: acting on a resource class this build does not understand is exactly
// what removal must never attempt.
func TestCapabilityResources_NormalizationDropsUnknownKinds(t *testing.T) {
	raw := []byte(`{
		"id":"ws-1","name":"Inbox","status":"active",
		"installed_capabilities":[{
			"id":"file-janitor","version":1,
			"installed_at":"2026-07-30T12:00:00Z","source":"in-place",
			"owned_resources":[
				{"kind":"mcp_binding","id":"binding-1"},
				{"kind":"database_table","id":"secrets"},
				{"kind":"mcp_binding","id":"  "},
				{"kind":"mcp_binding","id":"binding-1"}
			]
		}]
	}`)

	ws, err := FromJSON(raw)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	record, ok := ws.GetInstalledCapability(CapabilityFileJanitor)
	if !ok {
		t.Fatal("capability missing")
	}
	if len(record.OwnedResources) != 1 {
		t.Fatalf("expected only the one usable, deduplicated record, got %+v", record.OwnedResources)
	}
	if record.OwnedResources[0].ID != "binding-1" {
		t.Fatalf("wrong record survived: %+v", record.OwnedResources[0])
	}
}

// TestCapabilityResources_CloneIsDeep proves a returned record cannot be used
// to mutate workspace state behind the accessors' backs.
func TestCapabilityResources_CloneIsDeep(t *testing.T) {
	ws := installedWorkspace(t)
	ws.RecordCapabilityResource(CapabilityFileJanitor, CapabilityResource{Kind: ResourceMCPBinding, ID: "binding-1"})

	first, _ := ws.GetInstalledCapability(CapabilityFileJanitor)
	first.OwnedResources[0].ID = "mutated"
	first.OwnedResources[0].Shared = true

	second, _ := ws.GetInstalledCapability(CapabilityFileJanitor)
	if exclusive, recorded := second.Owns(ResourceMCPBinding, "binding-1"); !recorded || !exclusive {
		t.Fatalf("workspace state was mutated through a returned copy: %+v", second.OwnedResources)
	}
}

// TestRecordCapabilityResource_MarksTheCollectionEdited ties ownership writes
// into the same stale-write protection installs use: a resource recorded during
// setup must survive the next unrelated save.
func TestRecordCapabilityResource_MarksTheCollectionEdited(t *testing.T) {
	ws := installedWorkspace(t)
	reloaded, err := FromJSON(mustJSON(t, ws))
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if reloaded.InstalledCapabilitiesExplicit() {
		t.Fatal("precondition: a reloaded workspace carries no edit intent")
	}

	reloaded.RecordCapabilityResource(CapabilityFileJanitor, CapabilityResource{Kind: ResourceWatcher, ID: "watch-1"})
	if !reloaded.InstalledCapabilitiesExplicit() {
		t.Fatal("recording a resource must mark the collection as deliberately edited")
	}
}

func mustJSON(t *testing.T, ws *Workspace) []byte {
	t.Helper()
	data, err := ws.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	var check map[string]json.RawMessage
	if err := json.Unmarshal(data, &check); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return data
}
