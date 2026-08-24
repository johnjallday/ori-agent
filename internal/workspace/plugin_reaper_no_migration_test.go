package workspace

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func legacyWorkspaceFixture() *Workspace {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Legacy media project"})
	ws.ProjectPath = "legacy-song"
	ws.SetTemplateProvenance(&TemplateProvenance{
		TemplateID: "retired-media-template", TemplateName: "Retired media template", Builtin: true, Version: 9,
		RuntimeRequirements: &RuntimeRequirementsContract{
			SchemaVersion:  RuntimeRequirementsSchemaVersion,
			OperatingModes: []RuntimeOperatingMode{{ID: "assisted", Label: "Assisted", Description: "Legacy live control", Requires: []string{"retired_live_control"}}},
			Requirements:   []RuntimeRequirement{{Key: "retired_live_control", Label: "Retired provider", Description: "Legacy", Adapter: "retired_live_control"}},
		},
	})
	ws.SetRuntimeState(&WorkspaceRuntimeState{
		SelectedModeID: "assisted",
		Grants:         []RuntimeCapabilityGrant{{CapabilityKey: "retired_live_control", AgentInstanceID: "legacy-agent", GrantedAt: time.Now()}},
	})
	ws.Tasks = []Task{{ID: "legacy-task", Description: "Legacy task", Status: TaskStatusPending}}
	return ws
}

func legacyWorkspaceJSON(t *testing.T, ws *Workspace) []byte {
	t.Helper()
	encoded, err := ws.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	// Simulate a field owned by a retired compiled integration. Strict behavior
	// fields are ignored rather than interpreted or migrated into plugin state.
	raw["pinned_reaper_scripts"] = []string{"40026", "custom:legacy.lua"}
	encoded, err = json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestRetiredProviderWorkspaceDecodeNeverInventsPluginAttachmentOrMutatesInput(t *testing.T) {
	before := legacyWorkspaceJSON(t, legacyWorkspaceFixture())
	input := append([]byte(nil), before...)
	loaded, err := FromJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.GetInstalledCapabilities()) != 0 {
		t.Fatalf("legacy read invented plugin capabilities: %+v", loaded.GetInstalledCapabilities())
	}
	if !bytes.Equal(before, input) {
		t.Fatal("decoding legacy workspace metadata mutated its source bytes")
	}
	after, err := loaded.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(after, []byte("pinned_reaper_scripts")) {
		t.Fatal("retired executable state remained active in the normalized model")
	}
}

func TestFreshPluginAttachmentDoesNotImportRetiredGrantsOrProvenance(t *testing.T) {
	ws, err := FromJSON(legacyWorkspaceJSON(t, legacyWorkspaceFixture()))
	if err != nil {
		t.Fatal(err)
	}
	grant := ws.GetRuntimeState().Grants[0]
	provenance := ws.GetTemplateProvenance()
	owner := CapabilityOwner{Kind: CapabilityOwnerPlugin, PluginID: "media-plugin", PluginVersion: "1.0.0"}
	added, err := ws.AddInstalledCapability(InstalledCapability{
		ID: "media-live-control", Version: 1, InstalledAt: time.Now(), Source: InstallSourceInPlace, Owner: &owner,
	})
	if err != nil || !added {
		t.Fatalf("fresh attachment = %v, %v", added, err)
	}
	record, _ := ws.GetInstalledCapability("media-live-control")
	if len(record.OwnedResources) != 0 {
		t.Fatalf("fresh attachment imported retired resources: %+v", record)
	}
	state := ws.GetRuntimeState()
	if len(state.Grants) != 1 || state.Grants[0].AgentInstanceID != grant.AgentInstanceID {
		t.Fatalf("fresh attachment rewrote legacy grants: %+v", state.Grants)
	}
	if got := ws.GetTemplateProvenance(); got.TemplateID != provenance.TemplateID || got.PluginOwner != nil {
		t.Fatalf("fresh attachment rewrote provenance: %+v", got)
	}
}
