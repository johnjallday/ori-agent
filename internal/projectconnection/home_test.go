package projectconnection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHomePreparationIsIndependentAndReusesTheCanonicalGroup(t *testing.T) {
	service, store, _ := connectionService(t)
	scope := Scope{OwnerUserID: "owner-1", RunID: "first-run", Template: connectionTemplate(t)}
	before, err := service.HomePreparation(scope)
	if err != nil || before.Exists || before.Acknowledged {
		t.Fatalf("before: %+v %v", before, err)
	}
	if _, err := service.AcknowledgePreparation(scope); err == nil {
		t.Fatal("acknowledged absent group")
	}
	home, err := service.CreateHome(scope, "My Studio")
	if err != nil || !home.Exists || home.Name != "My Studio" || home.Acknowledged {
		t.Fatalf("home: %+v %v", home, err)
	}
	ids, _ := store.List()
	if len(ids) != 1 {
		t.Fatalf("group creation created other resources: %v", ids)
	}
	group, err := store.Get(home.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	state := group.GetAssistantProgramState()
	if group.Kind != "group" || state == nil || state.Hired || len(state.LinkedProjectIDs) != 0 || len(group.AgentInstances) != 0 || len(group.Tasks) != 0 || group.RuntimeState != nil {
		t.Fatal("group creation hired, linked, scheduled or granted access")
	}
	again, err := service.CreateHome(scope, "Do not rename on retry")
	if err != nil || again.HomeID != home.HomeID || again.Name != "My Studio" {
		t.Fatalf("reuse: %+v %v", again, err)
	}
	home, err = service.AcknowledgePreparation(scope)
	if err != nil || !home.Acknowledged {
		t.Fatalf("acknowledge: %+v %v", home, err)
	}
	// The acknowledgement survives a fresh service, but is not a runtime mode.
	restarted := NewService(store, nil)
	observed, err := restarted.HomePreparation(scope)
	if err != nil || !observed.Acknowledged {
		t.Fatalf("restart: %+v %v", observed, err)
	}
	request := Request{ModeID: "new_project", WorkspaceName: "First Song", ProjectName: "First Song"}
	preview, err := restarted.Preview(context.Background(), scope, request)
	if err != nil || preview.Projection.ParentWorkspaceName != "My Studio" || preview.Projection.HomeWillBeCreated {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	scope.Template.PluginOwner.PluginVersion = "2.0.0"
	changed, err := restarted.HomePreparation(scope)
	if err != nil || changed.Acknowledged {
		t.Fatalf("changed integration retained acknowledgement: %+v %v", changed, err)
	}
	data, _ := json.Marshal(changed)
	if strings.Contains(string(data), "shared_data") || strings.Contains(string(data), "system_prompt") {
		t.Fatal("preparation leaked owner internals")
	}
}
