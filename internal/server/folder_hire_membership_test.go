package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestFolderHire_MembershipUsesOnlyTheExactReusedWorkspace(t *testing.T) {
	store := workspace.NewInMemoryStore()
	member := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Member workspace", Agents: []string{"Ari"}})
	other := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Other workspace"})
	for _, ws := range []*workspace.Workspace{member, other} {
		if err := store.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	host := &folderSetupHost{builder: &ServerBuilder{workspaceStore: store}}
	plan := personalassistant.FolderSetupPlan{Intent: personalassistant.FolderSetupIntent{Software: &personalassistant.FolderSoftwareIntent{
		Outcome: personalassistant.FolderSoftwareReuse, WorkspaceID: member.ID,
		AssistantAction: personalassistant.FolderSoftwareReuse, Assistant: "Ari",
	}}}
	read := func() *personalassistant.FolderHire {
		hire, _ := host.softwareHire(plan, sessionhttp.FolderOfferRoster{}, folderdigest.CapabilityRow{})
		return hire
	}
	if hire := read(); hire == nil || !hire.AssignedHere {
		t.Fatalf("actual membership omitted: %+v", hire)
	}
	plan.Intent.Software.WorkspaceID = other.ID
	if hire := read(); hire.AssignedHere || hire.Mode != personalassistant.FolderHireBind {
		t.Fatalf("existence in another workspace is not assignment here: %+v", hire)
	}
	plan.Intent.Software.WorkspaceID = "missing"
	if read().AssignedHere {
		t.Fatal("unreadable workspace invented membership")
	}
	plan.Intent.Software.WorkspaceID = member.ID
	plan.Intent.Software.Outcome = personalassistant.FolderSoftwareCreate
	if read().AssignedHere {
		t.Fatal("new workspace inherited membership")
	}
}
