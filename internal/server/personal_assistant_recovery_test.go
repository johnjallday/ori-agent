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
	hireAssistantWithHQ(t, handler)

	profilesBefore := builder.st.ListAgents()
	workspacesBefore, err := builder.sessionStore.ListWorkspaces(t.Context())
	if err != nil {
		t.Fatalf("list workspaces before reset: %v", err)
	}
	deleteRelationshipRow(t, builder)

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

// TestPersonalAssistantRecovery_FixesAnHQKeptFromAnEarlierHire is the reported
// case: the assistant profile comes from the latest hire, but the Personal HQ
// folder still names the assistant it was built for. Recovery used to stop at
// "records do not agree" with nothing to press; the diagnosis now names the
// mismatch, and the one reviewed fix rewrites the HQ marker in both stores and
// reconnects, creating nothing.
func TestPersonalAssistantRecovery_FixesAnHQKeptFromAnEarlierHire(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	workspaceID := hireAssistantWithHQ(t, handler)
	profilesBefore := len(builder.st.ListAgents())
	deleteRelationshipRow(t, builder)
	setHQMarkerAssistant(t, builder, workspaceID, "earlier-hire-assistant")

	var blocked struct {
		PersonalAssistant struct {
			RepairStep string `json:"repair_step"`
		} `json:"personal_assistant"`
	}
	serveJSON(t, handler, http.MethodGet, "/api/personal-assistant", "", http.StatusOK, &blocked)
	if blocked.PersonalAssistant.RepairStep != "relationship_recovery_blocked" {
		t.Fatalf("repair step = %q; want relationship_recovery_blocked", blocked.PersonalAssistant.RepairStep)
	}

	var diagnosed struct {
		Diagnosis struct {
			Issue string `json:"issue"`
			Fixes []struct {
				ID          string `json:"id"`
				Kind        string `json:"kind"`
				Recommended bool   `json:"recommended"`
			} `json:"fixes"`
			Digest string `json:"digest"`
		} `json:"diagnosis"`
	}
	serveJSON(t, handler, http.MethodGet, "/api/personal-assistant/repair/diagnosis", "", http.StatusOK, &diagnosed)
	diagnosis := diagnosed.Diagnosis
	if diagnosis.Issue != "assistant_mismatch" || len(diagnosis.Fixes) != 1 ||
		diagnosis.Fixes[0].ID != "link_hq:"+workspaceID || !diagnosis.Fixes[0].Recommended || diagnosis.Digest == "" {
		t.Fatalf("diagnosis = %#v", diagnosis)
	}

	body := `{"fix_id":"` + diagnosis.Fixes[0].ID + `","evidence_digest":"` + diagnosis.Digest + `"}`
	// A form post (what a page on another site could send) is refused.
	formReq := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/repair/resolve", bytes.NewBufferString(body))
	formReq.Header.Set("Content-Type", "text/plain")
	formRec := httptest.NewRecorder()
	handler.ServeHTTP(formRec, formReq)
	if formRec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("non-JSON resolve status = %d body=%s", formRec.Code, formRec.Body.String())
	}
	// A fix reviewed against other records is refused, and nothing changes.
	stale := `{"fix_id":"` + diagnosis.Fixes[0].ID + `","evidence_digest":"reviewed-something-else"}`
	serveJSON(t, handler, http.MethodPost, "/api/personal-assistant/repair/resolve", stale, http.StatusConflict, nil)

	var resolved struct {
		Reconnected       bool   `json:"reconnected"`
		Applied           string `json:"applied"`
		PersonalAssistant struct {
			State       string `json:"state"`
			AssistantID string `json:"assistant_id"`
			WorkspaceID string `json:"hq_workspace_id"`
		} `json:"personal_assistant"`
	}
	serveJSON(t, handler, http.MethodPost, "/api/personal-assistant/repair/resolve", body, http.StatusOK, &resolved)
	if !resolved.Reconnected || resolved.Applied != "link_hq" || resolved.PersonalAssistant.State != "paused" ||
		resolved.PersonalAssistant.WorkspaceID != workspaceID {
		t.Fatalf("resolve = %#v", resolved)
	}
	assistantID := resolved.PersonalAssistant.AssistantID

	// Both copies of the marker now name the reconnected assistant: the
	// database row, and workspace.json, which a fresh database is rebuilt from.
	stored, err := builder.sessionStore.GetWorkspace(t.Context(), workspaceID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if got := markerAssistant(stored.SharedData); got != assistantID {
		t.Fatalf("database marker names %q; want %q", got, assistantID)
	}
	folder, err := builder.workspaceFileStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("get folder workspace: %v", err)
	}
	if got := markerAssistant(folder.SharedData); got != assistantID {
		t.Fatalf("folder marker names %q; want %q", got, assistantID)
	}
	if got := len(builder.st.ListAgents()); got != profilesBefore {
		t.Fatalf("the fix created profiles: %d -> %d", profilesBefore, got)
	}

	// Nothing is left to fix, so the diagnosis now says the relationship exists.
	serveJSON(t, handler, http.MethodGet, "/api/personal-assistant/repair/diagnosis", "", http.StatusConflict, nil)
}

// hireAssistantWithHQ hires "Assistant" and builds "My HQ", returning the HQ's
// workspace ID.
func hireAssistantWithHQ(t *testing.T, handler http.Handler) string {
	t.Helper()
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
	var built struct {
		PersonalAssistant struct {
			WorkspaceID string `json:"hq_workspace_id"`
		} `json:"personal_assistant"`
	}
	if err := json.Unmarshal(hqRec.Body.Bytes(), &built); err != nil || built.PersonalAssistant.WorkspaceID == "" {
		t.Fatalf("decode hq response: %v body=%s", err, hqRec.Body.String())
	}
	return built.PersonalAssistant.WorkspaceID
}

// deleteRelationshipRow is the database-reset split state: the relationship row
// is gone while the assistant profile and the Personal HQ remain.
func deleteRelationshipRow(t *testing.T, builder *ServerBuilder) {
	t.Helper()
	dbOwner, ok := builder.sessionStore.(interface{ DB() *database.DB })
	if !ok || dbOwner.DB() == nil {
		t.Fatal("test session store does not expose its database")
	}
	if _, err := dbOwner.DB().ExecContext(t.Context(), `DELETE FROM personal_assistant_state WHERE user_id = ?`, "local"); err != nil {
		t.Fatalf("delete relationship row: %v", err)
	}
}

// setHQMarkerAssistant makes the HQ marker name another assistant in both the
// database and workspace.json, as an HQ kept from an earlier hire does.
func setHQMarkerAssistant(t *testing.T, builder *ServerBuilder, workspaceID, assistantID string) {
	t.Helper()
	rename := func(shared map[string]any) {
		marker, ok := shared["personal_assistant_presentation"].(map[string]any)
		if !ok {
			t.Fatalf("workspace has no Personal HQ marker: %#v", shared)
		}
		marker["assistant_id"] = assistantID
	}
	ws, err := builder.sessionStore.GetWorkspace(t.Context(), workspaceID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	rename(ws.SharedData)
	if err := builder.sessionStore.UpdateWorkspace(t.Context(), ws); err != nil {
		t.Fatalf("update workspace: %v", err)
	}
	folder, err := builder.workspaceFileStore.Get(workspaceID)
	if err != nil {
		t.Fatalf("get folder workspace: %v", err)
	}
	rename(folder.SharedData)
	if err := builder.workspaceFileStore.Save(folder); err != nil {
		t.Fatalf("save folder workspace: %v", err)
	}
}

func markerAssistant(shared map[string]any) string {
	marker, _ := shared["personal_assistant_presentation"].(map[string]any)
	id, _ := marker["assistant_id"].(string)
	return id
}

// serveJSON sends a request (JSON when body is set), checks the status, and
// decodes the response into out when out is non-nil.
func serveJSON(t *testing.T, handler http.Handler, method, path, body string, wantStatus int, out any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s status = %d; want %d body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v body=%s", path, err, rec.Body.String())
		}
	}
}
