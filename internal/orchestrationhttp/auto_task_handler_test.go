package orchestrationhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestParseOutputContractSuggestionNormalizesColumns(t *testing.T) {
	response, err := parseOutputContractSuggestion(`{
		"output_contract": {
			"source": "ai_suggested",
			"columns": [
				{"name": "date", "type": "date", "required": true, "description": "Run date"},
				{"name": "pollen_count", "type": "number", "required": true, "description": "Reported level"},
				{"name": "POLLEN_COUNT", "type": "number", "required": true}
			]
		},
		"reasoning": "Daily pollen runs need one row per observation."
	}`)
	if err != nil {
		t.Fatalf("parseOutputContractSuggestion returned error: %v", err)
	}
	if response.OutputContract == nil {
		t.Fatal("expected output contract")
	}
	if response.OutputContract.Source != "ai_suggested" {
		t.Fatalf("unexpected source: %q", response.OutputContract.Source)
	}
	if got := len(response.OutputContract.Columns); got != 2 {
		t.Fatalf("expected duplicate column to be removed, got %d columns", got)
	}
	if response.OutputContract.Columns[1].Name != "pollen_count" || response.OutputContract.Columns[1].Type != "number" {
		t.Fatalf("unexpected second column: %+v", response.OutputContract.Columns[1])
	}
	if response.OutputSpec == nil {
		t.Fatalf("expected legacy contract response to synthesize output spec")
	}
	if response.OutputSpec.Approval != nil || response.OutputSpec.Version != "" {
		t.Fatalf("suggested spec should be an unapproved draft: %+v", response.OutputSpec)
	}
}

func TestAutoTaskParseTimeoutUsesCodexHeadroom(t *testing.T) {
	if got := autoTaskParseTimeout("codex"); got != 120*time.Second {
		t.Fatalf("codex timeout = %v, want 120s", got)
	}
	if got := autoTaskParseTimeout("openai"); got != 60*time.Second {
		t.Fatalf("openai timeout = %v, want 60s", got)
	}
}

func TestClassifyAutoTaskErrorRecognizesWrappedDeadline(t *testing.T) {
	err := fmt.Errorf("fake codex run: %w", context.DeadlineExceeded)
	msg := classifyAutoTaskError(err)
	if !strings.Contains(msg, "request timed out") {
		t.Fatalf("expected timeout message, got %q", msg)
	}
}

func TestAutoTaskAgentContextScopesToWorkspaceEntryAgent(t *testing.T) {
	ws := &workspace.Workspace{
		ID: "workspace-1",
		AgentInstances: []workspace.AgentInstance{
			{Name: "test123 Manager", EntryPoint: true, Role: "Workspace Manager"},
		},
		Agents: []string{"test123 Manager"},
	}
	handler := &AutoTaskHandler{
		agentStore: &autoTaskAgentStore{
			order: []string{"Ori", "test123 Manager"},
			agents: map[string]*agent.Agent{
				"Ori": {Metadata: &types.AgentMetadata{Description: "Global general router"}},
				"test123 Manager": {Metadata: &types.AgentMetadata{
					Description: "Lead the test123 workspace.",
				}},
			},
		},
		workspaceStore: &autoTaskWorkspaceStore{
			workspaces: map[string]*workspace.Workspace{"workspace-1": ws},
		},
	}

	ctx := handler.autoTaskAgentContext("workspace-1")

	if ctx.DefaultAgentName != "test123 Manager" {
		t.Fatalf("default agent = %q, want test123 Manager", ctx.DefaultAgentName)
	}
	if len(ctx.Agents) != 1 || ctx.Agents[0] != "test123 Manager" {
		t.Fatalf("agents = %#v, want only workspace entry agent", ctx.Agents)
	}
	if _, exists := ctx.AgentDescriptions["Ori"]; exists {
		t.Fatalf("global Ori leaked into workspace-scoped descriptions: %#v", ctx.AgentDescriptions)
	}
	if got := ctx.AgentDescriptions["test123 Manager"]; !strings.Contains(got, "Workspace Manager") || !strings.Contains(got, "Lead the test123 workspace") {
		t.Fatalf("entry agent description missing workspace/global context: %q", got)
	}
}

func TestAutoTaskAgentContextNoDefaultWhenCoordinatorMissing(t *testing.T) {
	// Multi-agent workspace with no explicit entry agent: the coordinator
	// resolver reports "missing", so there must be no default agent — auto-parse
	// must not silently fall back to the first agent.
	ws := &workspace.Workspace{
		ID: "workspace-2",
		AgentInstances: []workspace.AgentInstance{
			{Name: "Writer"},
			{Name: "Researcher"},
		},
		Agents: []string{"Writer", "Researcher"},
	}
	handler := &AutoTaskHandler{
		workspaceStore: &autoTaskWorkspaceStore{
			workspaces: map[string]*workspace.Workspace{"workspace-2": ws},
		},
	}

	ctx := handler.autoTaskAgentContext("workspace-2")

	if ctx.DefaultAgentName != "" {
		t.Fatalf("default agent = %q, want empty (no explicit entry agent in a multi-agent workspace)", ctx.DefaultAgentName)
	}
	if len(ctx.Agents) != 2 {
		t.Fatalf("agents = %#v, want both workspace agents", ctx.Agents)
	}
}

func TestValidateTaskConfigDefaultsInvalidAgentToEntryAgent(t *testing.T) {
	handler := &AutoTaskHandler{}
	config := AutoTaskResponse{
		Title:     "Suggest Vienna activities",
		AgentName: "Ori",
		Priority:  3,
		Tasks: []AutoTaskStep{
			{ID: "step1", Title: "Suggest activities", AgentName: "Ori", Priority: 3},
			{ID: "step2", Title: "Format answer", Priority: 3, DependsOn: []string{"step1"}},
		},
	}

	got := handler.validateTaskConfig(config, []string{"test123 Manager"}, "test123 Manager")

	if got.AgentName != "test123 Manager" {
		t.Fatalf("agent_name = %q, want test123 Manager", got.AgentName)
	}
	for _, step := range got.Tasks {
		if step.AgentName != "test123 Manager" {
			t.Fatalf("step %s agent_name = %q, want test123 Manager", step.ID, step.AgentName)
		}
	}
}

func TestValidateTaskConfigPreservesValidWorkspaceAgent(t *testing.T) {
	handler := &AutoTaskHandler{}
	config := AutoTaskResponse{
		Title:     "Research flights",
		AgentName: "researcher",
		Priority:  3,
	}

	got := handler.validateTaskConfig(config, []string{"test123 Manager", "Researcher"}, "test123 Manager")

	if got.AgentName != "Researcher" {
		t.Fatalf("agent_name = %q, want exact-case Researcher", got.AgentName)
	}
}

func TestParseOutputContractSuggestionRejectsMissingContract(t *testing.T) {
	if _, err := parseOutputContractSuggestion(`{"reasoning":"no columns"}`); err == nil {
		t.Fatal("expected missing contract to fail")
	}
}

func TestParseOutputContractSuggestionAcceptsStructuredSpec(t *testing.T) {
	response, err := parseOutputContractSuggestion(`{
		"output_spec": {
			"source": "ai_suggested",
			"version": "should_be_cleared",
			"schema": {
				"name": "pollen",
				"strict": true,
				"fields": [
					{"name": "forecast_date", "type": "string", "required": true},
					{"name": "top_allergens", "type": "array", "required": false}
				]
			},
			"contract": {
				"source": "manual",
				"columns": [
					{"name": "forecast_date", "type": "date", "required": true},
					{"name": "top_allergens", "type": "string", "required": false}
				]
			},
			"mappings": [
				{"schema_field": "forecast_date", "csv_column": "forecast_date"},
				{"schema_field": "top_allergens", "csv_column": "top_allergens", "transform": "json_string"}
			],
			"metadata_policy": {"fields": [{"name": "run_id", "include": false}]},
			"approval": {"approved_at": "2026-05-21T00:00:00Z"}
		},
		"reasoning": "Pollen reports produce one row per forecast."
	}`)
	if err != nil {
		t.Fatalf("parseOutputContractSuggestion returned error: %v", err)
	}
	if response.OutputSpec == nil {
		t.Fatal("expected output spec")
	}
	if response.OutputSpec.Version != "" || response.OutputSpec.Approval != nil {
		t.Fatalf("suggested spec should not be approved/versioned: %+v", response.OutputSpec)
	}
	if response.OutputSpec.Source != "ai_suggested" {
		t.Fatalf("source=%q, want ai_suggested", response.OutputSpec.Source)
	}
	if response.OutputContract == nil || len(response.OutputContract.Columns) != 2 {
		t.Fatalf("expected mirrored output contract, got %+v", response.OutputContract)
	}
	if response.OutputSpec.Mappings[1].Transform != workspace.TaskOutputMappingTransformJSONString {
		t.Fatalf("expected json_string transform, got %+v", response.OutputSpec.Mappings[1])
	}
}

func TestParseOutputContractSuggestionRejectsInvalidStructuredSpec(t *testing.T) {
	if _, err := parseOutputContractSuggestion(`{
		"output_spec": {
			"schema": {"fields": [{"name": "value", "type": "number", "required": true}]},
			"contract": {"columns": [{"name": "value", "type": "number", "required": true}]},
			"mappings": [{"schema_field": "value", "csv_column": "value", "transform": "not_supported"}]
		}
	}`); err == nil {
		t.Fatal("expected invalid output spec to fail")
	}
}

func TestNormalizeOutputContractTelemetryAction(t *testing.T) {
	if got := normalizeOutputContractTelemetryAction(" Suggestion_Edited "); got != "suggestion_edited" {
		t.Fatalf("expected normalized action, got %q", got)
	}
	if got := normalizeOutputContractTelemetryAction("raw_output_uploaded"); got != "" {
		t.Fatalf("expected invalid action to be rejected, got %q", got)
	}
}

func TestHandleOutputContractTelemetryPublishesSanitizedEvent(t *testing.T) {
	eventBus := workspace.NewEventBus(10, 10)
	handler := NewAutoTaskHandler(nil, nil, nil, nil, eventBus)

	reqBody := `{
		"workspace_id":"workspace-telemetry",
		"task_id":"task-telemetry",
		"action":"suggestion_accepted",
		"source":"ai_suggested",
		"column_count":5,
		"result":"raw task output should be ignored"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/output-contract/telemetry", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleOutputContractTelemetry(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp["success"] {
		t.Fatalf("expected success response, got %#v", resp)
	}
	events := eventBus.GetHistory(func(event workspace.Event) bool {
		return event.Type == workspace.EventTaskOutput
	}, 1)
	if len(events) != 1 {
		t.Fatalf("expected one task output event, got %d", len(events))
	}
	event := events[0]
	if event.WorkspaceID != "workspace-telemetry" || event.Source != "task.output_contract" {
		t.Fatalf("unexpected event routing: %#v", event)
	}
	if event.Data["task_id"] != "task-telemetry" || event.Data["action"] != "suggestion_accepted" {
		t.Fatalf("unexpected event data: %#v", event.Data)
	}
	if event.Data["column_count"] != 5 {
		t.Fatalf("expected column_count=5, got %#v", event.Data["column_count"])
	}
	if _, ok := event.Data["result"]; ok {
		t.Fatalf("raw output leaked into telemetry event: %#v", event.Data)
	}
}

func TestHandleOutputContractTelemetryRejectsInvalidAction(t *testing.T) {
	handler := NewAutoTaskHandler(nil, nil, nil, nil, workspace.NewEventBus(10, 10))
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/tasks/output-contract/telemetry", strings.NewReader(`{
		"workspace_id":"workspace-telemetry",
		"action":"copy_raw_result"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleOutputContractTelemetry(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

type autoTaskAgentStore struct {
	order  []string
	agents map[string]*agent.Agent
}

func (s *autoTaskAgentStore) ListAgents() []string {
	return append([]string(nil), s.order...)
}

func (s *autoTaskAgentStore) CreateAgent(name string, cfg *store.CreateAgentConfig) error {
	return nil
}

func (s *autoTaskAgentStore) DeleteAgent(name string) error {
	return nil
}

func (s *autoTaskAgentStore) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := s.agents[name]
	return ag, ok
}

func (s *autoTaskAgentStore) SetAgent(name string, ag *agent.Agent) error {
	return nil
}

func (s *autoTaskAgentStore) UpdateAgent(name string, updateFn func(*agent.Agent) error) error {
	return nil
}

func (s *autoTaskAgentStore) ClearAgents() error {
	return nil
}

func (s *autoTaskAgentStore) Save() error {
	return nil
}

type autoTaskWorkspaceStore struct {
	workspaces map[string]*workspace.Workspace
}

func (s *autoTaskWorkspaceStore) Save(ws *workspace.Workspace) error {
	return nil
}

func (s *autoTaskWorkspaceStore) Get(id string) (*workspace.Workspace, error) {
	return s.workspaces[id], nil
}

func (s *autoTaskWorkspaceStore) List() ([]string, error) {
	return nil, nil
}

func (s *autoTaskWorkspaceStore) Delete(id string) error {
	return nil
}

func (s *autoTaskWorkspaceStore) ListActive() ([]*workspace.Workspace, error) {
	return nil, nil
}

func (s *autoTaskWorkspaceStore) GetFilesPath(workspaceID string) string {
	return ""
}

func (s *autoTaskWorkspaceStore) GetOutputsPath(workspaceID string) string {
	return ""
}

func (s *autoTaskWorkspaceStore) GetWorkspaceAgent(workspaceID, agentName string) (*agent.Agent, bool, error) {
	return nil, false, nil
}

func (s *autoTaskWorkspaceStore) SaveWorkspaceAgent(workspaceID, agentName string, ag *agent.Agent) error {
	return nil
}

func (s *autoTaskWorkspaceStore) Lock(wsID string) func() {
	return func() {}
}

func (s *autoTaskWorkspaceStore) Update(wsID string, fn func(*workspace.Workspace) error) error {
	if ws := s.workspaces[wsID]; ws != nil {
		return fn(ws)
	}
	return nil
}
