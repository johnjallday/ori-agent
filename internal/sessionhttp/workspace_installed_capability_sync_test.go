package sessionhttp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func capabilityInstallJSON(t *testing.T, source string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal([]agentworkspace.InstalledCapability{{
		ID:          agentworkspace.CapabilityFileJanitor,
		Version:     1,
		InstalledAt: time.Now().UTC().Truncate(time.Second),
		Source:      source,
	}})
	if err != nil {
		t.Fatalf("marshal install: %v", err)
	}
	return data
}

// TestBuildFileStoreWorkspace_DecodesInstalledCapabilities covers the
// session -> folder conversion used by workspace creation, group backfill, and
// the portable-state resync. It is a hand-written field-by-field conversion, so
// a field omitted here silently writes an empty collection to workspace.json.
func TestBuildFileStoreWorkspace_DecodesInstalledCapabilities(t *testing.T) {
	sessionWS := &session.Workspace{
		ID:                        "ws-build",
		Name:                      "Inbox",
		InstalledCapabilitiesJSON: capabilityInstallJSON(t, agentworkspace.InstallSourceBlueprint),
	}

	folderWS, err := buildFileStoreWorkspace(sessionWS)
	if err != nil {
		t.Fatalf("buildFileStoreWorkspace: %v", err)
	}

	installed, ok := folderWS.GetInstalledCapability(agentworkspace.CapabilityFileJanitor)
	if !ok {
		t.Fatalf("install dropped by buildFileStoreWorkspace: %+v", folderWS.GetInstalledCapabilities())
	}
	if installed.Source != agentworkspace.InstallSourceBlueprint {
		t.Fatalf("install provenance not carried: %+v", installed)
	}
}

// TestMergePortableWorkspaceState_PreservesInstallWhenSourceCarriesNone is the
// FR-144 guard for the file-store sync path: `target` is the canonical
// workspace.json record and `source` is built from the SQLite row. A source that
// carries no capability data means "this record said nothing", not "uninstall
// File Janitor" — clobbering it would silently uninstall the capability on an
// unrelated sync (a rename, a tag edit, a group backfill).
func TestMergePortableWorkspaceState_PreservesInstallWhenSourceCarriesNone(t *testing.T) {
	target := &agentworkspace.Workspace{ID: "ws-merge", Name: "Inbox"}
	if _, err := target.AddInstalledCapability(agentworkspace.InstalledCapability{
		ID:          agentworkspace.CapabilityFileJanitor,
		Version:     1,
		InstalledAt: time.Now(),
		Source:      agentworkspace.InstallSourceInPlace,
	}); err != nil {
		t.Fatalf("install: %v", err)
	}

	source := &agentworkspace.Workspace{ID: "ws-merge", Name: "Renamed Inbox"}

	mergePortableWorkspaceState(target, source)

	if !target.HasInstalledCapability(agentworkspace.CapabilityFileJanitor) {
		t.Fatalf("install erased by an unrelated portable-state sync: %+v", target.GetInstalledCapabilities())
	}
	if target.Name != "Renamed Inbox" {
		t.Fatalf("the actual sync did not happen: name = %q", target.Name)
	}
}

// TestMergePortableWorkspaceState_WritesThroughInstallFromSource is the other
// half: preserve-when-absent must not become preserve-always, or a real install
// performed through the SQLite path would never reach workspace.json.
func TestMergePortableWorkspaceState_WritesThroughInstallFromSource(t *testing.T) {
	target := &agentworkspace.Workspace{ID: "ws-merge", Name: "Inbox"}

	source := &agentworkspace.Workspace{ID: "ws-merge", Name: "Inbox"}
	if _, err := source.AddInstalledCapability(agentworkspace.InstalledCapability{
		ID:          agentworkspace.CapabilityFileJanitor,
		Version:     1,
		InstalledAt: time.Now(),
		Source:      agentworkspace.InstallSourceBlueprint,
	}); err != nil {
		t.Fatalf("install: %v", err)
	}

	mergePortableWorkspaceState(target, source)

	installed, ok := target.GetInstalledCapability(agentworkspace.CapabilityFileJanitor)
	if !ok {
		t.Fatalf("install never reached the canonical record: %+v", target.GetInstalledCapabilities())
	}
	if installed.Source != agentworkspace.InstallSourceBlueprint {
		t.Fatalf("install provenance not carried: %+v", installed)
	}
}

// TestMergeWorkspaceJSONField_HydratesInstalledCapabilities covers the read-side
// hydration used by hydrateWorkspaceMetadataInto: a SQLite row written before
// the installed_capabilities_json column existed (or a workspace folder imported
// from another machine) must have its install filled in from workspace.json
// rather than reported to the UI as "not installed".
func TestMergeWorkspaceJSONField_HydratesInstalledCapabilities(t *testing.T) {
	fallback := capabilityInstallJSON(t, agentworkspace.InstallSourceLegacyMigration)

	var target json.RawMessage
	mergeWorkspaceJSONField(&target, fallback)
	if len(target) == 0 {
		t.Fatal("hydration did not fill an empty capability collection from disk")
	}

	// A record that already carries data wins: disk must not override a live
	// SQLite value (that is the write path's job, not hydration's).
	existing := capabilityInstallJSON(t, agentworkspace.InstallSourceInPlace)
	target = existing
	mergeWorkspaceJSONField(&target, fallback)

	var caps []agentworkspace.InstalledCapability
	if err := json.Unmarshal(target, &caps); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(caps) != 1 || caps[0].Source != agentworkspace.InstallSourceInPlace {
		t.Fatalf("hydration overwrote a populated collection: %+v", caps)
	}
}
