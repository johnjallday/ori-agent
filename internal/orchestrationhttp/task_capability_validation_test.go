package orchestrationhttp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type canonicalTaskStore struct{ *workspace.InMemoryStore }

type taskValidationRuntimeAdapter struct{}

func (taskValidationRuntimeAdapter) ID() string { return "test_runtime" }
func (taskValidationRuntimeAdapter) EvaluateDurable(context.Context, runtimecapability.EvaluationRequest) (runtimecapability.DurableResult, error) {
	return runtimecapability.DurableResult{State: runtimecapability.DurableConfigured}, nil
}

func (s *canonicalTaskStore) GetFolderWorkspace(id string) (*workspace.Workspace, error) {
	return s.Get(id)
}

func createCapabilityTask(t *testing.T, store *canonicalTaskStore, validator workspace.TaskCapabilityValidator, body string) *httptest.ResponseRecorder {
	t.Helper()
	handler := NewTaskHandler(store, nil, nil, nil)
	handler.SetCapabilityValidator(validator)
	request := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.handleCreateTask(recorder, request)
	return recorder
}

func taskValidationWorkspace(id string, runtimeContract bool) *workspace.Workspace {
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: id})
	ws.ID = id
	ws.AgentInstances = []workspace.AgentInstance{{ID: "agent-1", Name: "Lead", EntryPoint: true}}
	if runtimeContract {
		ws.SetTemplateProvenance(&workspace.TemplateProvenance{
			TemplateID: "runtime",
			RuntimeRequirements: &workspace.RuntimeRequirementsContract{
				SchemaVersion:  workspace.RuntimeRequirementsSchemaVersion,
				OperatingModes: []workspace.RuntimeOperatingMode{{ID: "assisted", Label: "Assisted", Description: "Use live control.", Requires: []string{"test_runtime"}}},
				Requirements:   []workspace.RuntimeRequirement{{Key: "test_runtime", Label: "Test runtime", Description: "Configure it.", Adapter: "test_runtime"}},
			},
		})
	}
	return ws
}

func TestCreateTaskValidatesKnownRuntimeKeysBeforeWrite(t *testing.T) {
	store := &canonicalTaskStore{InMemoryStore: workspace.NewInMemoryStore()}
	plain := taskValidationWorkspace("ws-plain", false)
	if err := store.Save(plain); err != nil {
		t.Fatal(err)
	}
	registry, err := runtimecapability.NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(taskValidationRuntimeAdapter{}); err != nil {
		t.Fatal(err)
	}
	validator := runtimecapability.NewService(store, registry)

	recorder := createCapabilityTask(t, store, validator, `{"workspace_id":"ws-plain","description":"Live task","required_capabilities":["test_runtime"]}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("undeclared runtime key = %d: %s", recorder.Code, recorder.Body.String())
	}
	stored, err := store.Get(plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Tasks) != 0 {
		t.Fatalf("invalid runtime task was partially written: %+v", stored.Tasks)
	}

	// Ordinary non-runtime planning/toolbox vocabulary remains authorable.
	recorder = createCapabilityTask(t, store, validator, `{"workspace_id":"ws-plain","description":"Plan task","required_capabilities":["planning","citations"]}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("ordinary capabilities = %d: %s", recorder.Code, recorder.Body.String())
	}
	stored, _ = store.Get(plain.ID)
	if len(stored.Tasks) != 1 || len(stored.Tasks[0].RequiredCapabilities) != 2 {
		t.Fatalf("ordinary capability task changed: %+v", stored.Tasks)
	}
}

func TestCreateTaskRoundTripsDeclaredRuntimeKey(t *testing.T) {
	store := &canonicalTaskStore{InMemoryStore: workspace.NewInMemoryStore()}
	ws := taskValidationWorkspace("ws-runtime", true)
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	registry, _ := runtimecapability.NewBuiltinRegistry()
	if err := registry.Register(taskValidationRuntimeAdapter{}); err != nil {
		t.Fatal(err)
	}
	validator := runtimecapability.NewService(store, registry)
	recorder := createCapabilityTask(t, store, validator, `{"workspace_id":"ws-runtime","description":"Live task","required_capabilities":[" TEST_RUNTIME ","test_runtime"],"file_fallback_for":["test_runtime"]}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("declared runtime key = %d: %s", recorder.Code, recorder.Body.String())
	}
	stored, _ := store.Get(ws.ID)
	if len(stored.Tasks) != 1 || len(stored.Tasks[0].RequiredCapabilities) != 1 || stored.Tasks[0].RequiredCapabilities[0] != "test_runtime" || len(stored.Tasks[0].FileFallbackFor) != 1 {
		t.Fatalf("runtime task key did not normalize/round-trip: %+v", stored.Tasks)
	}

	invalid := createCapabilityTask(t, store, validator, `{"workspace_id":"ws-runtime","description":"Invalid fallback","file_fallback_for":["test_runtime"]}`)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("fallback without matching requirement = %d: %s", invalid.Code, invalid.Body.String())
	}
}
