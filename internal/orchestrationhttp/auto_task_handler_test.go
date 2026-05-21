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
}

func TestParseOutputContractSuggestionRejectsMissingContract(t *testing.T) {
	if _, err := parseOutputContractSuggestion(`{"reasoning":"no columns"}`); err == nil {
		t.Fatal("expected missing contract to fail")
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
