package workspace

import (
	"bytes"
	"slices"
	"testing"
	"time"
)

func legacyReaperWorkspaceFixture() *Workspace {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Legacy REAPER Song"})
	ws.ProjectPath = "legacy-song"
	ws.PinnedReaperScripts = []string{"40026", "custom:legacy.lua"}
	ws.SetTemplateProvenance(&TemplateProvenance{
		TemplateID: "reaper-song", TemplateName: "Reaper Song", Builtin: true, Version: 9,
		RuntimeRequirements: &RuntimeRequirementsContract{
			SchemaVersion:  RuntimeRequirementsSchemaVersion,
			OperatingModes: []RuntimeOperatingMode{{ID: "assisted", Label: "Assisted", Description: "Legacy live control", Requires: []string{"reaper_live_control"}}},
			Requirements:   []RuntimeRequirement{{Key: "reaper_live_control", Label: "REAPER", Description: "Legacy", Adapter: "reaper_live_control"}},
		},
	})
	ws.SetRuntimeState(&WorkspaceRuntimeState{
		SelectedModeID: "assisted",
		Grants:         []RuntimeCapabilityGrant{{CapabilityKey: "reaper_live_control", AgentInstanceID: "legacy-agent", GrantedAt: time.Now()}},
	})
	ws.Tasks = []Task{{ID: "legacy-task", Description: "Legacy task", Status: TaskStatusPending}}
	return ws
}

func TestLegacyReaperWorkspaceDecodeNeverInventsPluginAttachment(t *testing.T) {
	legacy := legacyReaperWorkspaceFixture()
	before, err := legacy.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := FromJSON(before)
	if err != nil {
		t.Fatal(err)
	}
	after, err := loaded.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.GetInstalledCapabilities()) != 0 {
		t.Fatalf("legacy read invented plugin capabilities: %+v", loaded.GetInstalledCapabilities())
	}
	if !bytes.Equal(before, after) {
		t.Fatal("reading legacy REAPER metadata rewrote its portable workspace bytes")
	}
}

func TestFreshPluginAttachmentDoesNotImportLegacyPinsGrantsOrProvenance(t *testing.T) {
	ws := legacyReaperWorkspaceFixture()
	pins := append([]string(nil), ws.PinnedReaperScripts...)
	grant := ws.GetRuntimeState().Grants[0]
	provenance := ws.GetTemplateProvenance()
	owner := CapabilityOwner{Kind: CapabilityOwnerPlugin, PluginID: "reaper-plugin", PluginVersion: "0.3.0"}
	added, err := ws.AddInstalledCapability(InstalledCapability{
		ID: "reaper-live-control", Version: 1, InstalledAt: time.Now(), Source: InstallSourceInPlace, Owner: &owner,
	})
	if err != nil || !added {
		t.Fatalf("fresh attachment = %v, %v", added, err)
	}
	record, _ := ws.GetInstalledCapability("reaper-live-control")
	if len(record.OwnedResources) != 0 || !slices.Equal(ws.PinnedReaperScripts, pins) {
		t.Fatalf("fresh attachment imported legacy resources/pins: record=%+v pins=%v", record, ws.PinnedReaperScripts)
	}
	state := ws.GetRuntimeState()
	if len(state.Grants) != 1 || state.Grants[0].AgentInstanceID != grant.AgentInstanceID {
		t.Fatalf("fresh attachment rewrote legacy grants: %+v", state.Grants)
	}
	if got := ws.GetTemplateProvenance(); got.TemplateID != provenance.TemplateID || got.PluginOwner != nil {
		t.Fatalf("fresh attachment rewrote provenance: %+v", got)
	}
}
