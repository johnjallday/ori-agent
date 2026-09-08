package samplelibrary

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestCatalogQuestionDelegationRequiresBothHomeRolesAndCapability(t *testing.T) {
	ctx := context.Background()
	service, _, workspaces, _, homeID := newTestService(t)
	home, _ := workspaces.Get(homeID)
	state := home.GetAssistantProgramState()
	state.Declaration.Roles = []workspace.AssistantProgramRoleSpec{{ID: "coordinator", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true}, {ID: "catalog", Scope: workspace.AssistantRoleScopeHome, CapabilityID: workspace.CapabilitySampleLibrary}}
	state.HomeBindings = workspace.AssistantRoleBindingSet{StateRevision: 2, Bindings: []workspace.AssistantRoleBinding{{RoleID: "coordinator", AgentInstanceID: "from", AgentName: "Coordinator"}, {RoleID: "catalog", AgentInstanceID: "to", AgentName: "Catalog"}}}
	home.AgentInstances = []workspace.AgentInstance{{ID: "from", Name: "Coordinator"}, {ID: "to", Name: "Catalog"}}
	home.SetAssistantProgramState(state)
	if err := workspaces.Save(home); err != nil {
		t.Fatal(err)
	}
	result, err := service.DelegateQuestion(ctx, homeID, "Find a dry kick", "question")
	if err != nil || !result.Recorded {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	again, err := service.DelegateQuestion(ctx, homeID, "Find a dry kick", "question")
	if err != nil || !again.Replayed {
		t.Fatalf("replay=%+v err=%v", again, err)
	}
	if _, err = service.DelegateQuestion(ctx, homeID, "different", "question"); err == nil {
		t.Fatal("changed replay accepted")
	}
	home, _ = workspaces.Get(homeID)
	home.SetInstalledCapabilities(nil)
	_ = workspaces.Save(home)
	if _, err = service.DelegateQuestion(ctx, homeID, "Find a kick", "next"); err == nil {
		t.Fatal("delegation without capability accepted")
	}
}
