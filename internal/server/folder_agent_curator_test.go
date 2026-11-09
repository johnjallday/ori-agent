package server

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A folder of loose files is set up in place: File Janitor is installed on the
// folder's own workspace (no second workspace, no folder access yet) and File
// Curator joins it as the companion. Running it again changes nothing.
func TestFolderAgentFileCurator_InstallsFileJanitorInPlaceWithItsCompanion(t *testing.T) {
	builder := newBuiltTestBuilder(t)
	const workspaceID = "ws-messy"
	seed := &workspace.Workspace{
		ID: workspaceID, Name: "Messy", FolderSlug: "messy", OwnerUserID: "local",
		Status: workspace.StatusActive, SharedData: map[string]any{},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := builder.workspaceStore.Save(seed); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	count := func() int {
		ids, err := builder.workspaceStore.List()
		if err != nil {
			t.Fatal(err)
		}
		return len(ids)
	}
	before := count()
	states := map[string]string{}
	set := func(kind, state, _ string) { states[kind] = state }
	for run := 0; run < 2; run++ {
		if err := builder.addFolderAgentFileCurator(workspaceID, set); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	if states[folderagent.FolderPlanCapability] != personalassistant.FolderLineDone ||
		states[personalassistant.FolderPlanAgents] != personalassistant.FolderLineDone {
		t.Fatalf("line states = %v", states)
	}
	ws, err := builder.workspaceStore.Get(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	record, ok := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	if !ok || record.Source != workspace.InstallSourceInPlace {
		t.Fatalf("install record = %+v (present %v)", record, ok)
	}
	if got := len(record.ResourcesOfKind(workspace.ResourceCompanionAgent)); got != 1 {
		t.Fatalf("companions = %d, want 1", got)
	}
	if after := count(); after != before {
		t.Fatalf("workspaces %d -> %d: no other workspace may be created", before, after)
	}
}
