package orchestrationhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
