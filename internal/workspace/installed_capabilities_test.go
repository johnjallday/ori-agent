package workspace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testInstall(id string) InstalledCapability {
	return InstalledCapability{
		ID:          id,
		Version:     1,
		InstalledAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
		Source:      InstallSourceInPlace,
	}
}

func TestInstalledCapability_ValidateRejectsIncompleteRecords(t *testing.T) {
	valid := testInstall(CapabilityFileJanitor)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*InstalledCapability)
		wantErr string
	}{
		{"empty id", func(c *InstalledCapability) { c.ID = "   " }, "id is required"},
		{"zero version", func(c *InstalledCapability) { c.Version = 0 }, "version must be positive"},
		{"negative version", func(c *InstalledCapability) { c.Version = -1 }, "version must be positive"},
		{"zero installed_at", func(c *InstalledCapability) { c.InstalledAt = time.Time{} }, "installed_at is required"},
		{"empty source", func(c *InstalledCapability) { c.Source = "" }, "source is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := testInstall(CapabilityFileJanitor)
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestNormalizeInstalledCapabilities_CanonicalizesAndDeduplicates(t *testing.T) {
	first := testInstall("  File-Janitor  ")
	first.Source = "  Blueprint  "
	second := testInstall("FILE-JANITOR")
	second.Source = InstallSourceLegacyMigration
	second.InstalledAt = first.InstalledAt.Add(time.Hour)

	got := NormalizeInstalledCapabilities([]InstalledCapability{first, second})

	if len(got) != 1 {
		t.Fatalf("expected one record per capability ID, got %d: %+v", len(got), got)
	}
	if got[0].ID != CapabilityFileJanitor {
		t.Fatalf("id not canonicalized: %q", got[0].ID)
	}
	if got[0].Source != InstallSourceBlueprint {
		t.Fatalf("source not canonicalized, or first-seen did not win: %q", got[0].Source)
	}
	if !got[0].InstalledAt.Equal(first.InstalledAt) {
		t.Fatalf("first-seen record was replaced: %v", got[0].InstalledAt)
	}
}

func TestNormalizeInstalledCapabilities_DropsUnusableButKeepsIncompleteProvenance(t *testing.T) {
	noID := testInstall("   ")
	badVersion := testInstall("other-capability")
	badVersion.Version = 0

	// Incomplete provenance is NOT grounds for a drop: erasing a real install
	// because its source or timestamp went missing would lose user state.
	noProvenance := InstalledCapability{ID: CapabilityFileJanitor, Version: 2}

	got := NormalizeInstalledCapabilities([]InstalledCapability{noID, badVersion, noProvenance})

	if len(got) != 1 {
		t.Fatalf("expected only the usable record to survive, got %d: %+v", len(got), got)
	}
	if got[0].ID != CapabilityFileJanitor || got[0].Version != 2 {
		t.Fatalf("wrong record survived: %+v", got[0])
	}
	if !got[0].InstalledAt.IsZero() || got[0].Source != "" {
		t.Fatalf("normalization fabricated provenance: %+v", got[0])
	}
}

func TestNormalizeInstalledCapabilities_NilAndAllDroppedYieldNil(t *testing.T) {
	if got := NormalizeInstalledCapabilities(nil); got != nil {
		t.Fatalf("nil input must stay nil, got %+v", got)
	}
	// A slice of pure garbage must not become an empty-but-non-nil collection:
	// downstream merge guards read len()==0 as "no data", and asserting
	// "known to have none" on the strength of garbage would let a stale save
	// erase a real install.
	if got := NormalizeInstalledCapabilities([]InstalledCapability{{}, {ID: "x"}}); got != nil {
		t.Fatalf("all-dropped input must yield nil, got %+v", got)
	}
}

func TestCloneInstalledCapabilities_IsIndependentAndPreservesNil(t *testing.T) {
	if got := CloneInstalledCapabilities(nil); got != nil {
		t.Fatalf("nil input must stay nil, got %+v", got)
	}

	original := []InstalledCapability{testInstall(CapabilityFileJanitor)}
	clone := CloneInstalledCapabilities(original)
	clone[0].ID = "mutated"
	clone[0].Version = 99

	if original[0].ID != CapabilityFileJanitor || original[0].Version != 1 {
		t.Fatalf("clone shares backing storage with the original: %+v", original[0])
	}
}

func TestWorkspaceInstalledCapabilities_GetReturnsDetachedCopy(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}
	if _, err := ws.AddInstalledCapability(testInstall(CapabilityFileJanitor)); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	got := ws.GetInstalledCapabilities()
	got[0].Version = 42

	if again := ws.GetInstalledCapabilities(); again[0].Version != 1 {
		t.Fatalf("mutating the returned slice changed workspace state: %+v", again[0])
	}
}

func TestAddInstalledCapability_IsIdempotentAndPreservesOriginalProvenance(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}

	added, err := ws.AddInstalledCapability(testInstall(CapabilityFileJanitor))
	if err != nil || !added {
		t.Fatalf("first install: added=%v err=%v", added, err)
	}

	repeat := testInstall("FILE-JANITOR")
	repeat.Source = InstallSourceLegacyMigration
	repeat.InstalledAt = repeat.InstalledAt.Add(48 * time.Hour)

	added, err = ws.AddInstalledCapability(repeat)
	if err != nil {
		t.Fatalf("repeat install errored: %v", err)
	}
	if added {
		t.Fatal("repeat install reported a new record (FR-9 requires idempotency)")
	}

	caps := ws.GetInstalledCapabilities()
	if len(caps) != 1 {
		t.Fatalf("expected exactly one install record, got %d: %+v", len(caps), caps)
	}
	if caps[0].Source != InstallSourceInPlace {
		t.Fatalf("repeat install rewrote provenance: %q", caps[0].Source)
	}
}

func TestAddInstalledCapability_RejectsMalformedWithoutMutating(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}

	bad := testInstall(CapabilityFileJanitor)
	bad.Version = 0
	if _, err := ws.AddInstalledCapability(bad); err == nil {
		t.Fatal("expected a non-positive version to be rejected")
	}
	if len(ws.GetInstalledCapabilities()) != 0 {
		t.Fatalf("rejected install still mutated the workspace: %+v", ws.GetInstalledCapabilities())
	}
}

func TestAddInstalledCapability_StampsMissingInstalledAt(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}
	c := testInstall(CapabilityFileJanitor)
	c.InstalledAt = time.Time{}

	before := time.Now()
	if _, err := ws.AddInstalledCapability(c); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	got := ws.GetInstalledCapabilities()[0]
	if got.InstalledAt.Before(before) {
		t.Fatalf("installed_at was not stamped: %v", got.InstalledAt)
	}
}

func TestRemoveInstalledCapability_IsIdempotent(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}
	if _, err := ws.AddInstalledCapability(testInstall(CapabilityFileJanitor)); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	if !ws.RemoveInstalledCapability("File-Janitor") {
		t.Fatal("remove reported no change for an installed capability")
	}
	if ws.HasInstalledCapability(CapabilityFileJanitor) {
		t.Fatal("capability still installed after removal")
	}
	if ws.RemoveInstalledCapability(CapabilityFileJanitor) {
		t.Fatal("removing an absent capability reported a change")
	}
	// Nil rather than empty: see NormalizeInstalledCapabilities' contract.
	if caps := ws.GetInstalledCapabilities(); caps != nil {
		t.Fatalf("expected nil collection after removing the last record, got %+v", caps)
	}
}

func TestHasAndGetInstalledCapability_NormalizeLookups(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}
	if _, err := ws.AddInstalledCapability(testInstall(CapabilityFileJanitor)); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	if !ws.HasInstalledCapability("  FILE-JANITOR  ") {
		t.Fatal("lookup did not normalize the requested ID")
	}
	if _, ok := ws.GetInstalledCapability("downloads-janitor"); ok {
		t.Fatal("an unrelated ID resolved to the File Janitor install")
	}
	if _, ok := ws.GetInstalledCapability(""); ok {
		t.Fatal("an empty ID resolved to an install")
	}
}

func TestSetInstalledCapabilities_NormalizesAndDetaches(t *testing.T) {
	ws := &Workspace{ID: "ws-1"}
	input := []InstalledCapability{testInstall("File-Janitor"), testInstall("FILE-JANITOR")}

	ws.SetInstalledCapabilities(input)
	input[0].ID = "mutated-after-set"

	caps := ws.GetInstalledCapabilities()
	if len(caps) != 1 || caps[0].ID != CapabilityFileJanitor {
		t.Fatalf("expected one canonicalized record, got %+v", caps)
	}
}

func TestWorkspaceJSON_RoundTripsInstalledCapabilities(t *testing.T) {
	ws := &Workspace{ID: "ws-1", Name: "Inbox"}
	if _, err := ws.AddInstalledCapability(testInstall(CapabilityFileJanitor)); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	data, err := ws.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	if !strings.Contains(string(data), `"installed_capabilities"`) {
		t.Fatalf("installed_capabilities missing from workspace.json:\n%s", data)
	}

	decoded, err := FromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	got, ok := decoded.GetInstalledCapability(CapabilityFileJanitor)
	if !ok {
		t.Fatalf("install lost through the JSON round trip: %+v", decoded.GetInstalledCapabilities())
	}
	want := testInstall(CapabilityFileJanitor)
	if got.Version != want.Version || got.Source != want.Source || !got.InstalledAt.Equal(want.InstalledAt) {
		t.Fatalf("record changed through the round trip: got %+v want %+v", got, want)
	}
}

func TestWorkspaceJSON_LegacyRecordWithoutFieldDecodesEmpty(t *testing.T) {
	// A workspace.json written before this feature has no installed_capabilities
	// key at all. It must decode to "no data" (nil), never to a phantom install.
	legacy := []byte(`{"id":"ws-1","name":"Legacy","shared_data":{},"messages":[],"tasks":[],"status":"active"}`)

	ws, err := FromJSON(legacy)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if caps := ws.GetInstalledCapabilities(); caps != nil {
		t.Fatalf("legacy workspace gained capabilities: %+v", caps)
	}
	if ws.HasInstalledCapability(CapabilityFileJanitor) {
		t.Fatal("legacy workspace reports File Janitor installed")
	}
}

func TestFromJSON_NormalizesPersistedDuplicatesAndGarbage(t *testing.T) {
	raw := []byte(`{
		"id":"ws-1","name":"Hand edited","status":"active",
		"installed_capabilities":[
			{"id":"File-Janitor","version":1,"installed_at":"2026-07-30T12:00:00Z","source":"In-Place"},
			{"id":"file-janitor","version":3,"installed_at":"2026-07-31T12:00:00Z","source":"blueprint"},
			{"id":"","version":1,"installed_at":"2026-07-30T12:00:00Z","source":"in-place"},
			{"id":"broken","version":0,"installed_at":"2026-07-30T12:00:00Z","source":"in-place"}
		]
	}`)

	ws, err := FromJSON(raw)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}

	caps := ws.GetInstalledCapabilities()
	if len(caps) != 1 {
		t.Fatalf("expected one surviving record, got %d: %+v", len(caps), caps)
	}
	if caps[0].ID != CapabilityFileJanitor || caps[0].Version != 1 || caps[0].Source != InstallSourceInPlace {
		t.Fatalf("wrong record survived normalization: %+v", caps[0])
	}
}

func TestFromJSONMetadata_KeepsInstalledCapabilities(t *testing.T) {
	// The lean boot/cache decode drops chat history only. Capability records
	// must survive it, because FileStore.RenameWithSlug persists the
	// metadata-only cache copy straight back to workspace.json — see
	// tasks/trace-installed-capabilities-persistence.md (H3).
	ws := &Workspace{ID: "ws-1", Name: "Inbox"}
	if _, err := ws.AddInstalledCapability(testInstall(CapabilityFileJanitor)); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	data, err := ws.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	lean, err := FromJSONMetadata(data)
	if err != nil {
		t.Fatalf("FromJSONMetadata: %v", err)
	}
	if !lean.HasInstalledCapability(CapabilityFileJanitor) {
		t.Fatalf("install lost through the metadata decode: %+v", lean.GetInstalledCapabilities())
	}
}

func TestInstalledCapabilities_DoNotDisturbCapabilityMappings(t *testing.T) {
	// FR-7: the new collection is separate from connector CapabilityMapping
	// records, which live on MCPBinding and serialize inside mcp_bindings_json.
	// Installing a capability must not change their serialized form.
	binding := MCPBinding{
		ID:         "binding-1",
		ServerName: "calendar-connector",
		Enabled:    true,
		CapabilityMappings: []CapabilityMapping{{
			Capability: "calendar",
			Operations: map[string]OperationMapping{
				"list_events": {Tool: "list", ResultCollection: "/items"},
			},
		}},
	}

	ws := &Workspace{ID: "ws-1", MCPBindings: []MCPBinding{binding}}
	before, err := json.Marshal(ws.MCPBindings)
	if err != nil {
		t.Fatalf("marshal before: %v", err)
	}

	if _, err := ws.AddInstalledCapability(testInstall(CapabilityFileJanitor)); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	after, err := json.Marshal(ws.MCPBindings)
	if err != nil {
		t.Fatalf("marshal after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("capability mappings changed:\n before %s\n after  %s", before, after)
	}

	// And the reverse: the two collections must not share a JSON key.
	data, err := ws.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if _, ok := envelope["installed_capabilities"]; !ok {
		t.Fatal("installed_capabilities missing from the workspace envelope")
	}
	if _, ok := envelope["capability_mappings"]; ok {
		t.Fatal("capability mappings leaked to the workspace top level")
	}
}
