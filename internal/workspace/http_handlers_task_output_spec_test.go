package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPHandler_TaskOutputSpecDraftApproveDiscard(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, NewEventBus(10, 10))
	ws := &Workspace{
		ID:     "ws-output-spec",
		Name:   "Output Spec",
		Status: StatusActive,
		Tasks:  []Task{{ID: "task-1", Description: "check pollen"}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	body := `{"output_spec":{
		"source":"ai_suggested",
		"schema":{"name":"pollen","strict":true,"fields":[
			{"name":"forecast_date","type":"string","required":true},
			{"name":"pollen_count","type":"number","required":true}
		]},
		"contract":{"source":"ai_suggested","columns":[
			{"name":"forecast_date","type":"date","required":true},
			{"name":"pollen_count","type":"number","required":true}
		]},
		"mappings":[
			{"schema_field":"forecast_date","csv_column":"forecast_date","transform":"identity"},
			{"schema_field":"pollen_count","csv_column":"pollen_count","transform":"identity"}
		]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-output-spec/tasks/task-1/output-spec/draft", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.SaveTaskOutputSpecDraft(rec, withTaskPath(req))
	if rec.Code != http.StatusOK {
		t.Fatalf("save draft status=%d body=%s", rec.Code, rec.Body.String())
	}
	var draftResp struct {
		Draft *TaskOutputSpec `json:"draft_output_spec"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&draftResp); err != nil {
		t.Fatalf("decode draft response: %v", err)
	}
	if draftResp.Draft == nil || draftResp.Draft.Approval != nil || draftResp.Draft.Version != "" {
		t.Fatalf("unexpected draft: %+v", draftResp.Draft)
	}

	conflictReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-output-spec/tasks/task-1/output-spec/draft", strings.NewReader(body))
	conflictReq.Header.Set("Content-Type", "application/json")
	conflictRec := httptest.NewRecorder()
	handler.SaveTaskOutputSpecDraft(conflictRec, withTaskPath(conflictReq))
	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("draft conflict status=%d body=%s", conflictRec.Code, conflictRec.Body.String())
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-output-spec/tasks/task-1/output-spec/approve", nil)
	approveRec := httptest.NewRecorder()
	handler.ApproveTaskOutputSpecDraft(approveRec, withTaskPath(approveReq))
	if approveRec.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approveRec.Code, approveRec.Body.String())
	}
	updated, err := store.Get("ws-output-spec")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	task := updated.Tasks[0]
	if task.DraftOutputSpec != nil {
		t.Fatalf("draft was not cleared: %+v", task.DraftOutputSpec)
	}
	if task.OutputSpec == nil || task.OutputSpec.Version == "" || task.OutputSpec.Approval == nil {
		t.Fatalf("active spec not approved: %+v", task.OutputSpec)
	}
	if task.OutputSchema == nil || task.OutputContract == nil {
		t.Fatalf("legacy output fields were not mirrored: schema=%+v contract=%+v", task.OutputSchema, task.OutputContract)
	}

	discardReq := httptest.NewRequest(http.MethodDelete, "/api/workspaces/ws-output-spec/tasks/task-1/output-spec/discard", nil)
	discardRec := httptest.NewRecorder()
	handler.DiscardTaskOutputSpecDraft(discardRec, withTaskPath(discardReq))
	if discardRec.Code != http.StatusOK {
		t.Fatalf("discard status=%d body=%s", discardRec.Code, discardRec.Body.String())
	}
}

func TestHTTPHandler_SaveTaskOutputSpecDraftRejectsInvalidSpec(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)
	ws := &Workspace{
		ID:     "ws-output-spec-invalid",
		Name:   "Output Spec",
		Status: StatusActive,
		Tasks:  []Task{{ID: "task-1", Description: "check pollen"}},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	body := `{"output_spec":{
		"schema":{"fields":[{"name":"value","type":"number","required":true}]},
		"contract":{"columns":[{"name":"missing","type":"number","required":true}]},
		"mappings":[{"schema_field":"value","csv_column":"missing","transform":"unknown"}]
	}}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws-output-spec-invalid/tasks/task-1/output-spec/draft", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.SaveTaskOutputSpecDraft(rec, withTaskPath(req))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}
