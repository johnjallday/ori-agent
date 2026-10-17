package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Copy to workspace through the real routes: an approved global fact lands in
// a project workspace as an ordinary "global:<item-id>" entry (once), the copy
// shows in that workspace's next chat memory, Forget leaves it in place and
// lists it, and Remove copy deletes only that line.
func TestCopyGlobalFactToAWorkspaceAndRemoveTheCopy(t *testing.T) {
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
	expect(call(http.MethodPost, "/api/personal-assistant/hire", []byte(`{"request_id":"copy-hire","if_version":0,"display_name":"Atlas","mandate":"Help plan.","focus_areas":["plan_my_day"]}`)), http.StatusCreated)
	state, err := builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	hq, err := json.Marshal(map[string]any{"request_id": "copy-hq", "if_version": state.StateVersion, "name": "My HQ", "timezone": "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	expect(call(http.MethodPost, "/api/personal-assistant/hq", hq), http.StatusCreated)
	state, err = builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range []*workspace.Workspace{{ID: "album-3", Name: "Album 3"}, {ID: "ep-1", Name: "EP 1"}} {
		if err := builder.workspaceStore.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	explicit, err := json.Marshal(map[string]any{"state_version": state.StateVersion, "request_id": "copy-fact", "category": "how_you_work", "text": "John delivers stems at 48 kHz."})
	if err != nil {
		t.Fatal(err)
	}
	expect(call(http.MethodPost, "/api/personal-assistant/knowledge/explicit", explicit), http.StatusOK)
	fact := readGlobalMemoryItem(t, handler, "John delivers stems at 48 kHz.")
	base := "/api/personal-assistant/knowledge/" + fact.ID

	copyBody := func(workspaceID string, version int64) []byte {
		body, err := json.Marshal(map[string]any{"workspace_id": workspaceID, "if_version": version})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	var copied struct {
		Created     bool   `json:"created"`
		WorkspaceID string `json:"workspace_id"`
		Href        string `json:"href"`
	}
	first := call(http.MethodPost, base+"/copy", copyBody("album-3", fact.Version))
	expect(first, http.StatusOK)
	if err := json.Unmarshal(first.Body.Bytes(), &copied); err != nil || !copied.Created || copied.WorkspaceID != "album-3" ||
		!strings.HasPrefix(copied.Href, "/workspaces/") || !strings.HasSuffix(copied.Href, "#memory") {
		t.Fatalf("copy = %s", first.Body.String())
	}
	again := call(http.MethodPost, base+"/copy", copyBody("album-3", fact.Version))
	expect(again, http.StatusOK)
	if err := json.Unmarshal(again.Body.Bytes(), &copied); err != nil || copied.Created {
		t.Fatalf("repeat copy = %s", again.Body.String())
	}
	expect(call(http.MethodPost, base+"/copy", copyBody(state.HQWorkspaceID, fact.Version)), http.StatusBadRequest)
	expect(call(http.MethodPost, base+"/copy", copyBody("album-3", fact.Version+1)), http.StatusConflict)
	expect(call(http.MethodPost, base+"/copy", copyBody("no-such-workspace", fact.Version)), http.StatusNotFound)
	expect(call(http.MethodPost, "/api/personal-assistant/knowledge/not-a-uuid/copy", copyBody("album-3", 1)), http.StatusBadRequest)

	memory := workspace.NewMemoryStore(builder.workspaceFileStore)
	entries, err := memory.EntriesWithProvenance("album-3", "global:"+fact.ID)
	if err != nil || len(entries) != 1 || entries[0].Text != "John delivers stems at 48 kHz." || entries[0].Type != workspace.MemoryTypeFact {
		t.Fatalf("copied entry = %+v %v", entries, err)
	}
	// The copy is ordinary workspace memory: Album 3's prompt section carries it.
	raw, err := memory.ReadPromptRaw("album-3")
	if err != nil {
		t.Fatal(err)
	}
	if section := workspace.RenderMemoryPromptSection(workspace.ParseMemoryDocument(raw), true); !strings.Contains(section, "## Workspace Memory") ||
		!strings.Contains(section, "John delivers stems at 48 kHz.") {
		t.Fatalf("copied fact missing from the workspace memory section: %q", section)
	}

	var listed struct {
		Copies []struct {
			WorkspaceID string `json:"workspace_id"`
			Name        string `json:"name"`
			Href        string `json:"href"`
		} `json:"copies"`
	}
	readCopies := func() {
		t.Helper()
		result := call(http.MethodGet, base+"/copies", nil)
		expect(result, http.StatusOK)
		listed.Copies = nil
		if err := json.Unmarshal(result.Body.Bytes(), &listed); err != nil {
			t.Fatal(err)
		}
	}
	readCopies()
	if len(listed.Copies) != 1 || listed.Copies[0].WorkspaceID != "album-3" || listed.Copies[0].Name != "Album 3" {
		t.Fatalf("copies = %+v", listed.Copies)
	}

	forget, err := json.Marshal(map[string]any{"version": fact.Version, "request_id": "copy-forget"})
	if err != nil {
		t.Fatal(err)
	}
	expect(call(http.MethodPost, base+"/forget", forget), http.StatusOK)
	readCopies()
	if len(listed.Copies) != 1 || listed.Copies[0].WorkspaceID != "album-3" {
		t.Fatalf("copies after forget = %+v", listed.Copies)
	}
	if err := memory.Append("album-3", workspace.MemoryEntry{Type: workspace.MemoryTypeFact, Date: "2026-10-17", Provenance: "user", Text: "Album keeps this"}); err != nil {
		t.Fatal(err)
	}
	expect(call(http.MethodDelete, base+"/copies/album-3", nil), http.StatusOK)
	readCopies()
	if len(listed.Copies) != 0 {
		t.Fatalf("copies after remove = %+v", listed.Copies)
	}
	doc, err := memory.Read("album-3")
	if err != nil || len(doc.Entries()) != 1 || doc.Entries()[0].Text != "Album keeps this" {
		t.Fatalf("remove copy touched other lines: %+v %v", doc.Entries(), err)
	}
	expect(call(http.MethodDelete, base+"/copies/album-3", nil), http.StatusNotFound)
	expect(call(http.MethodDelete, base+"/copies/ep-1", nil), http.StatusNotFound)
}
