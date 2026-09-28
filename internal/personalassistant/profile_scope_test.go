package personalassistant

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

type scopedProfileFixture struct{ entries []store.WorkspaceAgentEntry }

func (s *scopedProfileFixture) WorkspaceAgents() []store.WorkspaceAgentEntry { return s.entries }

type scopedAttachmentFixture struct {
	attachment workspacecontinuity.Attachment
}

func (s *scopedAttachmentFixture) Attachment(_ context.Context, _ string) (workspacecontinuity.Attachment, error) {
	return s.attachment, nil
}

func TestImportedRelationshipProfileNeverFallsBackToSameNamedGlobalAgent(t *testing.T) {
	root := t.TempDir()
	system, err := store.NewFileStore(filepath.Join(root, "system.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	global := &agent.Agent{Role: types.RoleOrchestrator, Metadata: &types.AgentMetadata{Tags: []string{ProfileAssistantMarker("foreign"), ProfileHireMarker("hire-foreign")}}}
	if err := system.SetAgent("Ada", global); err != nil {
		t.Fatal(err)
	}
	composite, err := store.NewCompositeStore(system, filepath.Join(root, "workspaces"), filepath.Join(root, "agent-state"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	copy := &agent.Agent{Role: types.RoleOrchestrator, Metadata: &types.AgentMetadata{Tags: []string{ProfileAssistantMarker("assistant-1"), ProfileHireMarker("hire-1")}}}
	source := &scopedProfileFixture{entries: []store.WorkspaceAgentEntry{{WorkspaceID: "import-hq", WorkspaceName: "Imported HQ", AgentName: "Ada", Agent: copy}}}
	composite.SetWorkspaceAgentSource(source)
	attachments := &scopedAttachmentFixture{attachment: workspacecontinuity.Attachment{WorkspaceID: "import-hq", State: workspacecontinuity.ImportedInactive,
		Disposition: workspacecontinuity.AdoptedHQ, Version: 1}}
	reader := NewContinuityAgentStoreProfileReader(composite, attachments)
	state := &State{HQWorkspaceID: "import-hq", GlobalAgentProfileName: "Ada", AssistantID: "assistant-1"}
	profile, found := relationshipProfileProvenance(t.Context(), reader, state)
	if !found || !profile.OwnedBy("assistant-1") {
		t.Fatal("did not read scoped assistant provenance", profile)
	}
	source.entries = nil
	composite.InvalidateWorkspaceAgents()
	if profile, found := relationshipProfileProvenance(t.Context(), reader, state); found {
		t.Fatal("global name substituted a removed import profile", profile)
	}
	service, relationship, hq, _, _ := serviceMatrixFixture(StatusPaused)
	service.WithProfileReader(reader)
	relationship.state.AssistantID = "assistant-1"
	relationship.state.HQWorkspaceID = "import-hq"
	hq.status.WorkspaceID, hq.status.Workspace.ID = "import-hq", "import-hq"
	projection, err := service.Get(t.Context(), "local")
	if err != nil || projection.State != APIStateRepairNeeded || projection.Availability.AgentInstance.Reason != "imported_profile_missing" {
		t.Fatal("paused imported relationship fell back to foreign roster", projection, err)
	}
	source.entries = []store.WorkspaceAgentEntry{{WorkspaceID: "import-hq", WorkspaceName: "Imported HQ", AgentName: "Ada", Agent: copy}}
	composite.InvalidateWorkspaceAgents()
	projection, err = service.Get(t.Context(), "local")
	if err != nil || projection.State != APIStatePaused {
		t.Fatal("exact scoped assistant remained hidden", projection, err)
	}
	attachments.attachment.State = workspacecontinuity.Restoring
	if profile, found := relationshipProfileProvenance(t.Context(), reader, state); found {
		t.Fatal("unfinished import trusted a roster profile", profile)
	}
	attachments.attachment.State = workspacecontinuity.Native
	if profile, found := relationshipProfileProvenance(t.Context(), reader, state); !found || !profile.OwnedBy("foreign") {
		t.Fatal("native global profile behavior changed", profile, found)
	}
}
