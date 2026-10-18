package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The Memory tab's Promote to global goes through the real route and seam:
// an ordinary entry becomes an approved global fact, repeating it changes
// nothing, and the Personal HQ's reviewed lines are read-only (409, not 500).
func TestPromoteWorkspaceEntryToGlobalMemoryThroughTheServer(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	ctx := context.Background()
	call := func(method, path string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		return result
	}
	expect := func(result *httptest.ResponseRecorder, want int) {
		t.Helper()
		if result.Code != want {
			t.Fatalf("status %d, want %d: %s", result.Code, want, result.Body.String())
		}
	}
	expect(call(http.MethodPost, "/api/settings/workspace-root", []byte(`{"workspace_root":""}`)), http.StatusOK)
	if err := builder.workspaceStore.Save(&workspace.Workspace{ID: "album-3", Name: "Album 3"}); err != nil {
		t.Fatal(err)
	}
	memory := workspace.NewMemoryStore(builder.workspaceFileStore)
	if err := memory.Append("album-3", workspace.MemoryEntry{
		Type: workspace.MemoryTypeFeedback, Date: "2026-10-17", Provenance: "user", Text: "John prefers short replies.",
	}); err != nil {
		t.Fatal(err)
	}
	promote := "/api/workspaces/album-3/memory/entries/0/promote"

	// Before an assistant is hired: the shared explanation, and GET says so.
	unavailable := call(http.MethodPost, promote, []byte(`{}`))
	expect(unavailable, http.StatusConflict)
	var refusal struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(unavailable.Body.Bytes(), &refusal); err != nil || refusal.Code != "global_memory_unavailable" {
		t.Fatalf("no assistant = %s", unavailable.Body.String())
	}
	var tab struct {
		Entries []struct {
			Managed bool   `json:"managed"`
			Text    string `json:"text"`
		} `json:"entries"`
		GlobalMemory struct {
			Available    bool   `json:"available"`
			IsPersonalHQ bool   `json:"is_personal_hq"`
			Message      string `json:"message"`
		} `json:"global_memory"`
	}
	readTab := func(workspaceID string) {
		t.Helper()
		result := call(http.MethodGet, "/api/workspaces/"+workspaceID+"/memory", nil)
		expect(result, http.StatusOK)
		tab.Entries = nil
		if err := json.Unmarshal(result.Body.Bytes(), &tab); err != nil {
			t.Fatal(err)
		}
	}
	readTab("album-3")
	if tab.GlobalMemory.Available || tab.GlobalMemory.Message == "" {
		t.Fatalf("Memory tab before hire = %+v", tab.GlobalMemory)
	}

	expect(call(http.MethodPost, "/api/personal-assistant/hire", []byte(`{"request_id":"promote-hire","if_version":0,"display_name":"Atlas","mandate":"Help plan.","focus_areas":["plan_my_day"]}`)), http.StatusCreated)
	state, err := builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	hq, err := json.Marshal(map[string]any{"request_id": "promote-hq", "if_version": state.StateVersion, "name": "My HQ", "timezone": "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	expect(call(http.MethodPost, "/api/personal-assistant/hq", hq), http.StatusCreated)
	state, err = builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}

	readTab("album-3")
	if !tab.GlobalMemory.Available || tab.GlobalMemory.IsPersonalHQ {
		t.Fatalf("Memory tab after HQ = %+v", tab.GlobalMemory)
	}
	var promoted struct {
		Status  string `json:"status"`
		Created bool   `json:"created"`
		ItemID  string `json:"item_id"`
		Href    string `json:"href"`
	}
	first := call(http.MethodPost, promote, []byte(`{}`))
	expect(first, http.StatusOK)
	if err := json.Unmarshal(first.Body.Bytes(), &promoted); err != nil || promoted.Status != "promoted" || !promoted.Created ||
		promoted.ItemID == "" || promoted.Href != "/profile#personalHQKnowledge" {
		t.Fatalf("promote = %s", first.Body.String())
	}
	approved := readGlobalMemoryItem(t, handler, "John prefers short replies.")
	if approved.State != "approved" || approved.ID != promoted.ItemID {
		t.Fatalf("promoted fact in the review = %+v", approved)
	}
	again := call(http.MethodPost, promote, []byte(`{}`))
	expect(again, http.StatusOK)
	if err := json.Unmarshal(again.Body.Bytes(), &promoted); err != nil || promoted.Status != "already_known" || promoted.Created {
		t.Fatalf("repeat promote = %s", again.Body.String())
	}
	readTab("album-3")
	if len(tab.Entries) != 1 || tab.Entries[0].Text != "John prefers short replies." {
		t.Fatalf("promote moved or changed the workspace entry: %+v", tab.Entries)
	}

	// The HQ's own Memory tab: its reviewed line is managed and read-only.
	readTab(state.HQWorkspaceID)
	managed := -1
	for i, entry := range tab.Entries {
		if entry.Managed && entry.Text == "John prefers short replies." {
			managed = i
		}
	}
	if !tab.GlobalMemory.IsPersonalHQ || managed < 0 {
		t.Fatalf("HQ Memory tab = %+v", tab)
	}
	entry := "/api/workspaces/" + state.HQWorkspaceID + "/memory/entries/" + jsonInt(managed)
	expect(call(http.MethodPut, entry, []byte(`{"text":"changed","type":"fact"}`)), http.StatusConflict)
	expect(call(http.MethodDelete, entry, nil), http.StatusConflict)
	expect(call(http.MethodPost, entry+"/promote", []byte(`{}`)), http.StatusConflict)
}

func jsonInt(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
