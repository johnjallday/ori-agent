package workspace

import (
	"testing"
	"time"
)

func curatorWorkspace() *Workspace {
	return &Workspace{
		ID: "curator-ws", OwnerUserID: "local",
		AgentInstances: []AgentInstance{
			{ID: "lead", Name: "Lead", NodeID: "lead-node", EntryPoint: true},
			{ID: "curator", Name: "Renamed helper", NodeID: "helper-node", RoleID: "file-curator", RoleSource: RoleSourceAssigned},
		},
		InstalledCapabilities: []InstalledCapability{{ID: CapabilityFileJanitor, Version: 1, Source: InstallSourceBlueprint, InstalledAt: time.Now(), OwnedResources: []CapabilityResource{{Kind: ResourceCompanionAgent, ID: "curator", Shared: true}}}},
		TemplateProvenance:    &TemplateProvenance{TemplateID: "file-janitor", Builtin: true},
	}
}

func TestFileCuratorRequiresUniqueCurrentAssociation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Workspace)
		want   bool
	}{
		{"renamed non-entry", func(*Workspace) {}, true},
		{"no association", func(w *Workspace) { w.InstalledCapabilities[0].OwnedResources = nil }, false},
		{"detached", func(w *Workspace) { w.AgentInstances = w.AgentInstances[:1] }, false},
		{"removed", func(w *Workspace) { now := time.Now(); w.InstalledCapabilities[0].RemovedAt = &now }, false},
		{"duplicate instances", func(w *Workspace) { w.AgentInstances = append(w.AgentInstances, w.AgentInstances[1]) }, false},
		{"multiple resources", func(w *Workspace) {
			w.InstalledCapabilities[0].OwnedResources = append(w.InstalledCapabilities[0].OwnedResources, CapabilityResource{Kind: ResourceCompanionAgent, ID: "lead"})
		}, false},
		{"plugin owner", func(w *Workspace) {
			w.InstalledCapabilities[0].Owner = &CapabilityOwner{Kind: CapabilityOwnerPlugin, PluginID: "other"}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := curatorWorkspace()
			tc.change(w)
			if _, ok := w.FileCurator(); ok != tc.want {
				t.Fatalf("associated = %v", ok)
			}
		})
	}
}

func TestUniqueAgentInstanceNeverFallsBack(t *testing.T) {
	w := curatorWorkspace()
	for _, tc := range []struct {
		name, node string
		want       bool
	}{
		{"Renamed helper", "", true}, {"Renamed helper", "helper-node", true},
		{"Renamed helper", "gone", false}, {"Lead", "helper-node", false}, {"absent", "", false},
	} {
		if _, ok := w.FindUniqueAgentInstance(tc.name, tc.node); ok != tc.want {
			t.Errorf("%s/%s = %v", tc.name, tc.node, ok)
		}
	}
	w.AgentInstances = append(w.AgentInstances, AgentInstance{ID: "other", Name: "Renamed helper", NodeID: "other-node"})
	if _, ok := w.FindUniqueAgentInstance("Renamed helper", ""); ok {
		t.Fatal("ambiguous name selected first instance")
	}
	if got, ok := w.FindUniqueAgentInstance("Renamed helper", "helper-node"); !ok || got.ID != "curator" {
		t.Fatal("explicit unique node should resolve")
	}
}

func TestLegacyCuratorRepairRequiresPersistedRoleEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Workspace)
		want   bool
	}{
		{"renamed assigned role", func(*Workspace) {}, true},
		{"name only", func(w *Workspace) { w.AgentInstances[1].RoleID = ""; w.AgentInstances[1].Name = "File Curator" }, false},
		{"no creation evidence", func(w *Workspace) { w.AgentInstances[1].RoleSource = "" }, false},
		{"other blueprint", func(w *Workspace) { w.TemplateProvenance.TemplateID = "other" }, false},
		{"untrusted blueprint", func(w *Workspace) { w.TemplateProvenance.Builtin = false }, false},
		{"ambiguous roles", func(w *Workspace) { w.AgentInstances = append(w.AgentInstances, w.AgentInstances[1]) }, false},
		{"existing stale association", func(w *Workspace) {
			w.InstalledCapabilities[0].OwnedResources = []CapabilityResource{{Kind: ResourceCompanionAgent, ID: "gone"}}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := curatorWorkspace()
			w.InstalledCapabilities[0].OwnedResources = nil
			tc.change(w)
			got, ok := w.LegacyFileCuratorResource()
			if ok != tc.want {
				t.Fatalf("repair = %v", ok)
			}
			if ok && (got.ID != "curator" || !got.Shared) {
				t.Fatalf("unsafe repair: %+v", got)
			}
		})
	}
}
