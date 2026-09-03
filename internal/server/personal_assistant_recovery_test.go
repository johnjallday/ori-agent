package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
)

// TestPersonalAssistantRecovery_RepairsDeletedRelationshipWithoutDuplicatingResources
// covers the database-reset split state end to end. The relationship row is
// missing while the file-backed assistant profile and Personal HQ still exist;
// GET must detect that evidence and POST repair must bind those server-selected
// identities without creating another profile or workspace.
func TestPersonalAssistantRecovery_RepairsDeletedRelationshipWithoutDuplicatingResources(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)

	hireReq := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hire", bytes.NewBufferString(
		`{"request_id":"recovery-hire","if_version":0,"display_name":"Assistant","mandate":"Keep my work moving.","focus_areas":["plan_my_day"]}`,
	))
	hireReq.Header.Set("Content-Type", "application/json")
	hireRec := httptest.NewRecorder()
	handler.ServeHTTP(hireRec, hireReq)
	if hireRec.Code != http.StatusCreated {
		t.Fatalf("hire status = %d body=%s", hireRec.Code, hireRec.Body.String())
	}
	var hired struct {
		PersonalAssistant struct {
			StateVersion int64 `json:"state_version"`
		} `json:"personal_assistant"`
	}
	if err := json.Unmarshal(hireRec.Body.Bytes(), &hired); err != nil {
		t.Fatalf("decode hire response: %v", err)
	}

	hqBody, err := json.Marshal(map[string]any{
		"request_id": "recovery-hq",
		"if_version": hired.PersonalAssistant.StateVersion,
		"name":       "My HQ",
		"timezone":   "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	hqReq := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hq", bytes.NewReader(hqBody))
	hqReq.Header.Set("Content-Type", "application/json")
	hqRec := httptest.NewRecorder()
	handler.ServeHTTP(hqRec, hqReq)
	if hqRec.Code != http.StatusCreated {
		t.Fatalf("hq status = %d body=%s", hqRec.Code, hqRec.Body.String())
	}

	profilesBefore := builder.st.ListAgents()
	workspacesBefore, err := builder.sessionStore.ListWorkspaces(t.Context())
	if err != nil {
		t.Fatalf("list workspaces before reset: %v", err)
	}

	dbOwner, ok := builder.sessionStore.(interface{ DB() *database.DB })
	if !ok || dbOwner.DB() == nil {
		t.Fatal("test session store does not expose its database")
	}
	if _, err := dbOwner.DB().ExecContext(t.Context(), `DELETE FROM personal_assistant_state WHERE user_id = ?`, "local"); err != nil {
		t.Fatalf("delete relationship row: %v", err)
	}

	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/personal-assistant", nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("recovery GET status = %d body=%s", getRec.Code, getRec.Body.String())
	}
	var orphanResponse struct {
		PersonalAssistant struct {
			State        string `json:"state"`
			RepairStep   string `json:"repair_step"`
			StateVersion int64  `json:"state_version"`
			AssistantID  string `json:"assistant_id"`
			WorkspaceID  string `json:"hq_workspace_id"`
		} `json:"personal_assistant"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &orphanResponse); err != nil {
		t.Fatalf("decode recovery projection: %v", err)
	}
	orphan := orphanResponse.PersonalAssistant
	if orphan.State != "repair_needed" || orphan.RepairStep != "relationship_recovery" || orphan.StateVersion != 0 {
		t.Fatalf("orphan projection = %#v", orphan)
	}
	if orphan.AssistantID == "" || orphan.WorkspaceID == "" {
		t.Fatalf("orphan projection omitted validated identity = %#v", orphan)
	}

	repairReq := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/repair", bytes.NewBufferString(`{"if_version":0}`))
	repairReq.Header.Set("Content-Type", "application/json")
	repairRec := httptest.NewRecorder()
	handler.ServeHTTP(repairRec, repairReq)
	if repairRec.Code != http.StatusOK {
		t.Fatalf("repair status = %d body=%s", repairRec.Code, repairRec.Body.String())
	}
	var repaired struct {
		PersonalAssistant struct {
			State        string `json:"state"`
			AssistantID  string `json:"assistant_id"`
			WorkspaceID  string `json:"hq_workspace_id"`
			StateVersion int64  `json:"state_version"`
		} `json:"personal_assistant"`
	}
	if err := json.Unmarshal(repairRec.Body.Bytes(), &repaired); err != nil {
		t.Fatalf("decode repair response: %v", err)
	}
	if repaired.PersonalAssistant.State != "paused" || repaired.PersonalAssistant.StateVersion != 1 {
		t.Fatalf("repair result = %#v", repaired.PersonalAssistant)
	}
	if repaired.PersonalAssistant.AssistantID != orphan.AssistantID || repaired.PersonalAssistant.WorkspaceID != orphan.WorkspaceID {
		t.Fatalf("repair changed identity: before=%#v after=%#v", orphan, repaired.PersonalAssistant)
	}

	profilesAfter := builder.st.ListAgents()
	workspacesAfter, err := builder.sessionStore.ListWorkspaces(t.Context())
	if err != nil {
		t.Fatalf("list workspaces after repair: %v", err)
	}
	if len(profilesAfter) != len(profilesBefore) || len(workspacesAfter) != len(workspacesBefore) {
		t.Fatalf("repair created resources: profiles %d -> %d, workspaces %d -> %d", len(profilesBefore), len(profilesAfter), len(workspacesBefore), len(workspacesAfter))
	}
}
