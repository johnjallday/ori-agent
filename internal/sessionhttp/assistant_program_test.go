package sessionhttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type assistantInstalledPluginLister struct {
	installed []plugin.InstalledPlugin
	err       error
}

func (l assistantInstalledPluginLister) List() ([]plugin.InstalledPlugin, error) {
	return l.installed, l.err
}

func assistantProgramHandlerFixture(t *testing.T) (*Handler, *workspace.InMemoryStore, *workspace.Workspace, *workspace.Workspace) {
	t.Helper()
	handler, cleanup := createTestHandler(t)
	t.Cleanup(cleanup)
	store := workspace.NewInMemoryStore()
	handler.SetWorkspaceTaskStore(store)
	project := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "First Song"})
	project.FolderSlug = "first-song"
	project.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID:  "plugin:reaper-plugin:reaper-song",
		Version:     3,
		PluginOwner: &workspace.PluginTemplateOwner{PluginID: "reaper-plugin", BlueprintID: "reaper-song", BlueprintVersion: 3},
		AssistantProgram: &workspace.AssistantProgramDeclaration{
			SchemaVersion: 1, ID: "music-producer-assistant", StationName: "Producer Home", DefaultPrimaryName: "Producer",
			Roles: []workspace.AssistantProgramRoleSpec{
				{ID: "producer", Label: "Producer", Description: "Coordinates", Primary: true, Role: "orchestrator", Type: "tool_calling", SystemPrompt: "Coordinate safely", Skills: []string{"reaper-session-setup"}},
				{ID: "engineer", Label: "Mix Engineer", Description: "Technical specialist", Role: "specialist", Type: "tool_calling", SystemPrompt: "Bounded engineering"},
				{ID: "songwriter", Label: "Songwriter", Description: "Writing specialist", Role: "specialist", Type: "general", SystemPrompt: "Bounded writing"},
			},
			Stages: []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}, {ID: "collaborator", Label: "Collaborator", AcceptedCompletionThreshold: 5}},
		},
	})
	if err := store.Save(project); err != nil {
		t.Fatal(err)
	}
	station, _, err := workspace.NewAssistantProgramStore(store).EnsureProjectStation(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store, project, station
}

func assistantProgramRequest(method, target, workspaceID, body string) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	request.SetPathValue("workspaceID", workspaceID)
	return request
}

func decodeAssistantSummary(t *testing.T, recorder *httptest.ResponseRecorder) assistantProgramSummary {
	t.Helper()
	var summary assistantProgramSummary
	if err := json.Unmarshal(recorder.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary: %v; body=%s", err, recorder.Body.String())
	}
	return summary
}

func TestAssistantProgramGetAndHireMaterializeStableSharedRoster(t *testing.T) {
	handler, store, project, station := assistantProgramHandlerFixture(t)

	get := httptest.NewRecorder()
	handler.GetAssistantProgram(get, assistantProgramRequest(http.MethodGet, "/api/workspaces/"+project.ID+"/assistant-program", project.ID, ""))
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d, body=%s", get.Code, get.Body.String())
	}
	before := decodeAssistantSummary(t, get)
	if !before.Available || before.Hired || before.StationID != station.ID || before.ProjectID != project.ID {
		t.Fatalf("before hire summary = %+v", before)
	}

	hire := httptest.NewRecorder()
	body := `{"name":"June","provider":"openai","model":"studio-model","version":` + jsonNumber(before.StateRevision) + `}`
	handler.HireAssistantProgram(hire, assistantProgramRequest(http.MethodPost, "/api/workspaces/"+project.ID+"/assistant-program/hire", project.ID, body))
	if hire.Code != http.StatusOK {
		t.Fatalf("hire status = %d, body=%s", hire.Code, hire.Body.String())
	}
	after := decodeAssistantSummary(t, hire)
	if !after.Hired || after.PrimaryName != "June" || after.StageID != "helper" || after.Level != 1 || len(after.Roster) != 3 {
		t.Fatalf("after hire summary = %+v", after)
	}

	savedStation, _ := store.Get(station.ID)
	savedProject, _ := store.Get(project.ID)
	stationInstances := savedStation.GetAgentInstances()
	projectInstances := savedProject.GetAgentInstances()
	if len(stationInstances) != 3 || len(projectInstances) != 3 {
		t.Fatalf("roster sizes station=%d project=%d", len(stationInstances), len(projectInstances))
	}
	for index := range stationInstances {
		if stationInstances[index].ID != projectInstances[index].ID || stationInstances[index].Name != projectInstances[index].Name {
			t.Fatalf("role %d is not stable across station/project: %+v / %+v", index, stationInstances[index], projectInstances[index])
		}
	}
	if savedStation.EntryAgentName() != "June" || savedProject.EntryAgentName() != "June" {
		t.Fatalf("entry agents station=%q project=%q", savedStation.EntryAgentName(), savedProject.EntryAgentName())
	}
	for _, name := range []string{"June", "Mix Engineer", "Songwriter"} {
		if _, ok := handler.agentStore.GetAgent(name); !ok {
			t.Errorf("global agent %q missing", name)
		}
	}

	// A retry after success is idempotent and cannot duplicate station agents.
	retry := httptest.NewRecorder()
	handler.HireAssistantProgram(retry, assistantProgramRequest(http.MethodPost, "/api/workspaces/"+project.ID+"/assistant-program/hire", project.ID, body))
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status = %d, body=%s", retry.Code, retry.Body.String())
	}
	savedStation, _ = store.Get(station.ID)
	if got := len(savedStation.GetAgentInstances()); got != 3 {
		t.Fatalf("retry duplicated station roster: %d", got)
	}
}

func TestAssistantProgramConcurrentHireCreatesExactlyOneRoster(t *testing.T) {
	handler, store, project, station := assistantProgramHandlerFixture(t)
	state := station.GetAssistantProgramState()
	body := `{"name":"June","version":` + jsonNumber(state.StateRevision) + `}`
	var wait sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			recorder := httptest.NewRecorder()
			handler.HireAssistantProgram(recorder, assistantProgramRequest(http.MethodPost, "/hire", project.ID, body))
			statuses <- recorder.Code
		}()
	}
	wait.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("concurrent hire status = %d", status)
		}
	}
	stored, _ := store.Get(station.ID)
	if got := len(stored.GetAgentInstances()); got != 3 {
		t.Fatalf("station roster size = %d", got)
	}
	for _, name := range []string{"June", "Mix Engineer", "Songwriter"} {
		if _, ok := handler.agentStore.GetAgent(name); !ok {
			t.Fatalf("agent %q missing", name)
		}
	}
}

func TestAssistantProgramHireRejectsStaleVersionAndNameCollision(t *testing.T) {
	handler, _, project, station := assistantProgramHandlerFixture(t)
	state := station.GetAssistantProgramState()

	stale := httptest.NewRecorder()
	handler.HireAssistantProgram(stale, assistantProgramRequest(http.MethodPost, "/hire", project.ID, `{"name":"June","version":0}`))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale status = %d, body=%s", stale.Code, stale.Body.String())
	}

	if err := handler.agentStore.CreateAgent("Mix Engineer", nil); err != nil {
		t.Fatal(err)
	}
	collision := httptest.NewRecorder()
	body := `{"name":"June","version":` + jsonNumber(state.StateRevision) + `}`
	handler.HireAssistantProgram(collision, assistantProgramRequest(http.MethodPost, "/hire", project.ID, body))
	if collision.Code != http.StatusConflict {
		t.Fatalf("collision status = %d, body=%s", collision.Code, collision.Body.String())
	}
	if _, ok := handler.agentStore.GetAgent("June"); ok {
		t.Fatalf("primary agent persisted after collision")
	}
}

func TestAssistantProgramHireRollsBackAgentsAndStationRosterOnProjectConflict(t *testing.T) {
	handler, store, project, station := assistantProgramHandlerFixture(t)
	if err := store.Update(project.ID, func(current *workspace.Workspace) error {
		current.AgentInstances = []workspace.AgentInstance{{ID: "existing-engineer", Name: "Mix Engineer"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Get(project.ID)
	if got := before.GetAgentInstances(); len(got) != 1 {
		t.Fatalf("conflicting fixture roster = %+v", got)
	}
	state := station.GetAssistantProgramState()
	recorder := httptest.NewRecorder()
	body := `{"name":"June","version":` + jsonNumber(state.StateRevision) + `}`
	handler.HireAssistantProgram(recorder, assistantProgramRequest(http.MethodPost, "/hire", project.ID, body))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, name := range []string{"June", "Mix Engineer", "Songwriter"} {
		if _, ok := handler.agentStore.GetAgent(name); ok {
			t.Fatalf("agent %q remained after rollback", name)
		}
	}
	storedStation, _ := store.Get(station.ID)
	if storedStation.GetAssistantProgramState().Hired || len(storedStation.GetAgentInstances()) != 0 {
		t.Fatalf("station retained partial hire: %+v", storedStation)
	}
	storedProject, _ := store.Get(project.ID)
	if got := storedProject.GetAgentInstances(); len(got) != 1 || got[0].ID != "existing-engineer" {
		t.Fatalf("project roster rollback = %+v", got)
	}
}

func TestAssistantProgramHireRejectsUnavailableExplicitModel(t *testing.T) {
	handler, _, project, station := assistantProgramHandlerFixture(t)
	handler.SetAssistantModelValidator(func(provider, model string) error {
		if provider != "configured" || model != "known" {
			return errors.New("unavailable")
		}
		return nil
	})
	state := station.GetAssistantProgramState()
	recorder := httptest.NewRecorder()
	body := `{"name":"June","provider":"missing","model":"unknown","version":` + jsonNumber(state.StateRevision) + `}`
	handler.HireAssistantProgram(recorder, assistantProgramRequest(http.MethodPost, "/hire", project.ID, body))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if _, ok := handler.agentStore.GetAgent("June"); ok {
		t.Fatal("agent persisted after model validation failure")
	}
}

func TestAssistantProgramDisabledContributionIsReadableButReadOnly(t *testing.T) {
	handler, store, project, station := assistantProgramHandlerFixture(t)
	handler.SetInstalledPluginLister(assistantInstalledPluginLister{})

	getRecorder := httptest.NewRecorder()
	handler.GetAssistantProgram(getRecorder, assistantProgramRequest(http.MethodGet, "/", project.ID, ""))
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	var summary map[string]any
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary["plugin_available"] != false || summary["available"] != true {
		t.Fatalf("disabled summary = %#v", summary)
	}

	mutation := httptest.NewRecorder()
	handler.AcknowledgeAssistantPromotion(mutation, assistantProgramRequest(http.MethodPost, "/promotion/acknowledge", project.ID, ""))
	if mutation.Code != http.StatusConflict {
		t.Fatalf("mutation status=%d body=%s", mutation.Code, mutation.Body.String())
	}
	stored, _ := store.Get(station.ID)
	if stored.GetAssistantProgramState().PluginAvailable {
		t.Fatal("plugin availability did not fail closed")
	}
}

func TestAssistantProgramOrdinaryWorkspaceIsUnavailable(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	store := workspace.NewInMemoryStore()
	handler.SetWorkspaceTaskStore(store)
	ordinary := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Ordinary"})
	if err := store.Save(ordinary); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	handler.GetAssistantProgram(recorder, assistantProgramRequest(http.MethodGet, "/assistant", ordinary.ID, ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	summary := decodeAssistantSummary(t, recorder)
	if summary.Available || summary.ActivationNeeded {
		t.Fatalf("ordinary workspace exposed assistant entry: %+v", summary)
	}
}

func TestAssistantProgramLegacyActivationIsExplicit(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()
	store := workspace.NewInMemoryStore()
	handler.SetWorkspaceTaskStore(store)
	legacy := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Legacy Song"})
	legacy.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID:  "plugin:reaper-plugin:reaper-song",
		PluginOwner: &workspace.PluginTemplateOwner{PluginID: "reaper-plugin", BlueprintID: "reaper-song", BlueprintVersion: 2},
	})
	if err := store.Save(legacy); err != nil {
		t.Fatal(err)
	}
	declaration := projecttemplates.Template{ID: "plugin:reaper-plugin:reaper-song", PluginOwner: &workspace.PluginTemplateOwner{PluginID: "reaper-plugin", BlueprintID: "reaper-song", BlueprintVersion: 3}, AssistantProgram: &workspace.AssistantProgramDeclaration{
		SchemaVersion: 1, ID: "music-producer-assistant", StationName: "Producer Home",
		Roles:  []workspace.AssistantProgramRoleSpec{{ID: "producer", Label: "Producer", Primary: true, SystemPrompt: "Safe"}},
		Stages: []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
	}}
	handler.SetProjectTemplateResolver(func(_, _ string) (projecttemplates.Template, error) { return declaration, nil })

	before := httptest.NewRecorder()
	handler.GetAssistantProgram(before, assistantProgramRequest(http.MethodGet, "/assistant", legacy.ID, ""))
	if summary := decodeAssistantSummary(t, before); summary.Available || !summary.ActivationNeeded {
		t.Fatalf("legacy pre-activation summary = %+v", summary)
	}
	activate := httptest.NewRecorder()
	handler.ActivateAssistantProgram(activate, assistantProgramRequest(http.MethodPost, "/activate", legacy.ID, ""))
	if activate.Code != http.StatusOK {
		t.Fatalf("activate status=%d body=%s", activate.Code, activate.Body.String())
	}
	after := decodeAssistantSummary(t, activate)
	if !after.Available || after.StationID == "" || after.Hired {
		t.Fatalf("legacy activation summary = %+v", after)
	}
}

func jsonNumber(value int64) string {
	return strconv.FormatInt(value, 10)
}
