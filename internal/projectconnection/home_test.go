package projectconnection

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestSplitHomePreparationNamesAMissingProviderWithoutCreatingAnything(t *testing.T) {
	for _, providerErr := range []error{
		grouprequirements.ErrHomeProviderNotInstalled,
		grouprequirements.ErrHomeProviderDisabled,
		grouprequirements.ErrHomeProviderIncompatible,
	} {
		t.Run(providerErr.Error(), func(t *testing.T) {
			service, store, _, resolver := splitConnectionService(t)
			resolver.err = providerErr
			scope := Scope{OwnerUserID: "owner-1", RunID: "run-1", Template: splitConnectionTemplate(t)}
			preparation, err := service.HomePreparation(scope)
			if !errors.Is(err, ErrHomeProviderMissing) {
				t.Fatalf("error = %v, want ErrHomeProviderMissing", err)
			}
			want := HomePreparation{
				Name: "Production Home", TemplateID: scope.Template.ID,
				GroupPolicy: "required", AvailableCompositions: []string{"grouped"},
			}
			if !reflect.DeepEqual(preparation, want) {
				t.Fatalf("partial preparation = %#v, want %#v", preparation, want)
			}
			if _, err := service.CreateHome(scope, "Production Home"); !errors.Is(err, ErrHomeProviderMissing) {
				t.Fatalf("create without provider = %v", err)
			}
			if ids, _ := store.List(); len(ids) != 0 {
				t.Fatalf("a missing provider created workspaces: %v", ids)
			}
		})
	}
}

func TestSplitHomePreparationRefusesAnAmbiguousJoin(t *testing.T) {
	service, _, _, resolver := splitConnectionService(t)
	resolver.err = grouprequirements.ErrIndependentHomeInvalid
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-1", Template: splitConnectionTemplate(t)}
	if preparation, err := service.HomePreparation(scope); !errors.Is(err, ErrUnavailable) || preparation.Name != "" {
		t.Fatalf("ambiguous join = %#v %v", preparation, err)
	}
	unwired, _, _ := connectionService(t)
	if _, err := unwired.HomePreparation(scope); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("split read without a group-requirements service = %v", err)
	}
}

func TestSplitHomeIsCreatedUnderTheResolvedProviderAndReused(t *testing.T) {
	service, store, grouping, _ := splitConnectionService(t)
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-1", Template: splitConnectionTemplate(t)}
	before, err := service.HomePreparation(scope)
	if err != nil || before.Exists || before.Name != "Production Home" {
		t.Fatalf("before: %+v %v", before, err)
	}
	key := workspace.AssistantProgramKey{OwnerUserID: "owner-1", PluginID: "home-provider", ProgramID: "production"}.Normalize()
	if before.GroupTemplateID != projecttemplates.GroupTemplateIDForKey(key) {
		t.Fatalf("group template id = %q", before.GroupTemplateID)
	}
	home, err := service.CreateHome(scope, "My Production")
	if err != nil || !home.Exists || home.Name != "My Production" || home.HomeID == "" {
		t.Fatalf("create: %+v %v", home, err)
	}
	group, err := store.Get(home.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	state := group.GetAssistantProgramState()
	if group.Kind != "group" || state == nil || state.Key.Normalize() != key || state.HomeProvider == nil ||
		state.HomeProvider.PluginID != "home-provider" || state.Declaration == nil || state.Declaration.ID != "production" {
		t.Fatalf("split Home state = %#v", state)
	}
	// The Home must satisfy the same group-requirement evaluation the quest's
	// next step (project creation) runs, or the quest would stall right after it.
	evaluation := grouping.Evaluate(grouprequirements.Input{
		OwnerUserID: "owner-1", OperationKind: grouprequirements.OperationCreateProject, Template: scope.Template,
	})
	if evaluation.State != grouprequirements.StateReadyGrouped || evaluation.HomeWorkspaceID != home.HomeID {
		t.Fatalf("evaluation after Build Group = %s %q (%s)", evaluation.State, evaluation.HomeWorkspaceID, evaluation.Summary)
	}
	request := Request{ModeID: projecttemplates.ProjectConnectionNewProject, WorkspaceName: "First Song", ProjectName: "First Song"}
	preview, err := service.Preview(context.Background(), scope, request)
	if err != nil || preview.Projection.ParentWorkspaceName != "My Production" || preview.Projection.HomeWillBeCreated {
		t.Fatalf("preview: %+v %v", preview.Projection, err)
	}
	again, err := service.CreateHome(scope, "Do not rename on retry")
	if err != nil || again.HomeID != home.HomeID || again.Name != "My Production" {
		t.Fatalf("reuse: %+v %v", again, err)
	}
	if ids, _ := store.List(); len(ids) != 1 {
		t.Fatalf("Build Group created more than the Home: %v", ids)
	}
}

func TestCombinedHomePreparationNeverConsultsTheSplitResolver(t *testing.T) {
	service, _, _, resolver := splitConnectionService(t)
	resolver.err = grouprequirements.ErrIndependentHomeInvalid
	scope := Scope{OwnerUserID: "owner-1", RunID: "run-1", Template: connectionTemplate(t)}
	preparation, err := service.HomePreparation(scope)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := homeKey(scope)
	want := HomePreparation{Name: "Project Guide Home", TemplateID: scope.Template.ID, GroupTemplateID: projecttemplates.GroupTemplateIDForKey(key)}
	if !reflect.DeepEqual(preparation, want) {
		t.Fatalf("combined preparation = %#v, want %#v", preparation, want)
	}
	home, err := service.CreateHome(scope, "Studio")
	if err != nil || !home.Exists {
		t.Fatalf("combined create: %+v %v", home, err)
	}
	if resolver.calls != 0 {
		t.Fatalf("combined path called the split resolver %d times", resolver.calls)
	}
}

func TestHomePreparationIsIndependentAndReusesTheCanonicalGroup(t *testing.T) {
	service, store, _ := connectionService(t)
	scope := Scope{OwnerUserID: "owner-1", RunID: "first-run", Template: connectionTemplate(t)}
	before, err := service.HomePreparation(scope)
	if err != nil || before.Exists {
		t.Fatalf("before: %+v %v", before, err)
	}
	key, _ := homeKey(scope)
	wantTemplateID := projecttemplates.GroupTemplateIDForKey(key)
	if wantTemplateID == "" || before.GroupTemplateID != wantTemplateID {
		t.Fatalf("group template id = %q, want %q", before.GroupTemplateID, wantTemplateID)
	}
	otherOwner := scope
	otherOwner.OwnerUserID = "owner-2"
	if other, err := service.HomePreparation(otherOwner); err != nil || other.GroupTemplateID != wantTemplateID {
		t.Fatalf("group template id must be owner-free: %+v %v", other, err)
	}
	home, err := service.CreateHome(scope, "My Studio")
	if err != nil || !home.Exists || home.Name != "My Studio" {
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
	// The group is the only preparation readiness, and it survives a restart.
	// Reading it never writes a shared-data acknowledgement.
	restarted := NewService(store, nil)
	observed, err := restarted.HomePreparation(scope)
	if err != nil || !observed.Exists || observed.HomeID != home.HomeID {
		t.Fatalf("restart: %+v %v", observed, err)
	}
	if stored, _ := store.Get(home.HomeID); stored != nil && stored.SharedData["setup_preparation_acknowledgement"] != nil {
		t.Fatal("a preparation acknowledgement was written")
	}
	request := Request{ModeID: "new_project", WorkspaceName: "First Song", ProjectName: "First Song"}
	preview, err := restarted.Preview(context.Background(), scope, request)
	if err != nil || preview.Projection.ParentWorkspaceName != "My Studio" || preview.Projection.HomeWillBeCreated {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	scope.Template.PluginOwner.PluginVersion = "2.0.0"
	changed, err := restarted.HomePreparation(scope)
	if err != nil || !changed.Exists {
		t.Fatalf("group readiness depended on the integration version: %+v %v", changed, err)
	}
	data, _ := json.Marshal(changed)
	if strings.Contains(string(data), "shared_data") || strings.Contains(string(data), "system_prompt") ||
		strings.Contains(string(data), "acknowledged") {
		t.Fatal("preparation leaked owner internals")
	}
}
