package memoryhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeGlobalMemory struct {
	hq       string
	err      error
	result   personalassistant.WorkspacePromoteResult
	promoted []personalassistant.WorkspacePromoteRequest
}

func (f *fakeGlobalMemory) Workspace(context.Context) (string, error) { return f.hq, f.err }

func (f *fakeGlobalMemory) Promote(_ context.Context, req personalassistant.WorkspacePromoteRequest) (personalassistant.WorkspacePromoteResult, personalassistant.KnowledgeItem, error) {
	f.promoted = append(f.promoted, req)
	if f.err != nil {
		return "", personalassistant.KnowledgeItem{}, f.err
	}
	return f.result, personalassistant.KnowledgeItem{ID: "item-1"}, nil
}

func serveGlobal(h *Handler, method, target, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{workspaceID}/memory", h.GetMemory)
	mux.HandleFunc("PUT /api/workspaces/{workspaceID}/memory/entries/{index}", h.UpdateEntry)
	mux.HandleFunc("DELETE /api/workspaces/{workspaceID}/memory/entries/{index}", h.DeleteEntry)
	mux.HandleFunc("POST /api/workspaces/{workspaceID}/memory/entries/{index}/promote", h.PromoteEntry)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

// seedMemory writes MEMORY.md by hand: managed and copied lines cannot be
// created through the API.
func seedMemory(t *testing.T, fs *workspace.FileStore, workspaceID string, lines ...string) {
	t.Helper()
	folder, err := fs.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	content := "# Workspace Memory\n\n" + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(folder, workspace.MemoryFileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func apiErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error body %s: %v", rec.Body.String(), err)
	}
	return payload.Code
}

func TestGetMemory_MarksManagedLinesAndReportsGlobalMemory(t *testing.T) {
	h, fs := newTestHandler(t)
	seedMemory(t, fs, "ws1",
		"- [fact, 2026-10-17, ori-hq:item-1:rev-1] Reviewed global fact",
		"- [fact, 2026-10-17, agent:Scout] Ordinary entry")
	resp := decodeMemory(t, serveGlobal(h, http.MethodGet, "/api/workspaces/ws1/memory", ""))
	if len(resp.Entries) != 2 || !resp.Entries[0].Managed || resp.Entries[1].Managed {
		t.Fatalf("managed flags = %+v", resp.Entries)
	}
	if resp.GlobalMemory.Available || resp.GlobalMemory.Message != personalassistant.GlobalMemoryUnavailableMessage ||
		resp.GlobalMemory.SetupHref == "" {
		t.Fatalf("no seam must explain global memory is unavailable: %+v", resp.GlobalMemory)
	}

	h.SetGlobalMemory(&fakeGlobalMemory{hq: "ws1"})
	resp = decodeMemory(t, serveGlobal(h, http.MethodGet, "/api/workspaces/ws1/memory", ""))
	if !resp.GlobalMemory.Available || !resp.GlobalMemory.IsPersonalHQ || resp.GlobalMemory.ReviewHref != "/profile#personalHQKnowledge" {
		t.Fatalf("HQ workspace global memory = %+v", resp.GlobalMemory)
	}
	h.SetGlobalMemory(&fakeGlobalMemory{err: personalassistant.ErrGlobalMemoryUnavailable})
	resp = decodeMemory(t, serveGlobal(h, http.MethodGet, "/api/workspaces/ws1/memory", ""))
	if resp.GlobalMemory.Available || resp.GlobalMemory.Message == "" {
		t.Fatalf("no HQ = %+v", resp.GlobalMemory)
	}
}

func TestEditOrDeleteOfAManagedLineIsAConflictNotAServerError(t *testing.T) {
	h, fs := newTestHandler(t)
	seedMemory(t, fs, "ws1", "- [fact, 2026-10-17, ori-hq:item-1:rev-1] Reviewed global fact")
	for _, rec := range []*httptest.ResponseRecorder{
		serveGlobal(h, http.MethodPut, "/api/workspaces/ws1/memory/entries/0", `{"text":"changed","type":"fact"}`),
		serveGlobal(h, http.MethodDelete, "/api/workspaces/ws1/memory/entries/0", ""),
	} {
		if rec.Code != http.StatusConflict || apiErrorCode(t, rec) != "memory_entry_managed" {
			t.Fatalf("managed mutation = %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestPromoteEntry(t *testing.T) {
	h, fs := newTestHandler(t)
	seedMemory(t, fs, "ws1",
		"- [feedback, 2026-10-17, user] Prefers short replies",
		"- [fact, 2026-10-17, agent:Scout] Album masters at -14 LUFS",
		"- [fact, 2026-10-17, ori-hq:item-1:rev-1] Reviewed global fact",
		"- [fact, 2026-10-17, global:item-2] Copied from global")
	before, err := os.ReadFile(filepath.Join(mustFolder(t, fs), workspace.MemoryFileName))
	if err != nil {
		t.Fatal(err)
	}

	if rec := serveGlobal(h, http.MethodPost, "/api/workspaces/ws1/memory/entries/0/promote", `{}`); rec.Code != http.StatusConflict ||
		apiErrorCode(t, rec) != "global_memory_unavailable" || !strings.Contains(rec.Body.String(), "Set one up from Home") {
		t.Fatalf("no seam = %d %s", rec.Code, rec.Body.String())
	}

	global := &fakeGlobalMemory{hq: "hq", result: personalassistant.WorkspacePromoted}
	h.SetGlobalMemory(global)
	rec := serveGlobal(h, http.MethodPost, "/api/workspaces/ws1/memory/entries/0/promote", `{}`)
	var resp promoteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || rec.Code != http.StatusOK ||
		resp.Status != "promoted" || !resp.Created || resp.ItemID != "item-1" || resp.Href != "/profile#personalHQKnowledge" {
		t.Fatalf("promote = %d %s", rec.Code, rec.Body.String())
	}
	// Defaults: the entry's own text, and how_you_work for feedback.
	if got := global.promoted[0]; got.WorkspaceID != "ws1" || got.Category != "how_you_work" ||
		got.Text != "Prefers short replies" || got.Original != "Prefers short replies" {
		t.Fatalf("promote request = %+v", got)
	}
	// Edited wording and an explicit category; projects is the default otherwise.
	serveGlobal(h, http.MethodPost, "/api/workspaces/ws1/memory/entries/1/promote", `{"text":"John masters at -14 LUFS","category":"routines"}`)
	serveGlobal(h, http.MethodPost, "/api/workspaces/ws1/memory/entries/1/promote", `{}`)
	if got := global.promoted[1]; got.Text != "John masters at -14 LUFS" || got.Original != "Album masters at -14 LUFS" || got.Category != "routines" {
		t.Fatalf("edited promote = %+v", got)
	}
	if got := global.promoted[2]; got.Category != "projects" {
		t.Fatalf("default category = %+v", got)
	}

	for _, index := range []string{"2", "3"} {
		rec := serveGlobal(h, http.MethodPost, "/api/workspaces/ws1/memory/entries/"+index+"/promote", `{}`)
		if rec.Code != http.StatusConflict || apiErrorCode(t, rec) != "memory_entry_not_promotable" {
			t.Fatalf("entry %s = %d %s", index, rec.Code, rec.Body.String())
		}
	}
	if rec := serveGlobal(h, http.MethodPost, "/api/workspaces/ws1/memory/entries/9/promote", `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("out of range = %d", rec.Code)
	}

	global.result = personalassistant.WorkspacePromoteAlreadyKnown
	rec = serveGlobal(h, http.MethodPost, "/api/workspaces/ws1/memory/entries/0/promote", `{}`)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Status != "already_known" || resp.Created ||
		!strings.Contains(resp.Message, "Already in global memory") {
		t.Fatalf("already known = %d %s", rec.Code, rec.Body.String())
	}
	for _, tc := range []struct {
		err  error
		code int
	}{
		{personalassistant.ErrGlobalMemoryUnavailable, http.StatusConflict},
		{personalassistant.ErrValidation, http.StatusBadRequest},
		{personalassistant.ErrConflict, http.StatusConflict},
		{errors.New("disk on fire"), http.StatusServiceUnavailable},
	} {
		global.err = tc.err
		if rec := serveGlobal(h, http.MethodPost, "/api/workspaces/ws1/memory/entries/0/promote", `{}`); rec.Code != tc.code {
			t.Fatalf("%v = %d %s", tc.err, rec.Code, rec.Body.String())
		}
	}

	// Promote copies; it never moves or rewrites the workspace entry.
	after, err := os.ReadFile(filepath.Join(mustFolder(t, fs), workspace.MemoryFileName))
	if err != nil || string(after) != string(before) {
		t.Fatalf("promote changed the workspace MEMORY.md:\n%s", after)
	}
}

func mustFolder(t *testing.T, fs *workspace.FileStore) string {
	t.Helper()
	folder, err := fs.GetFolderPath("ws1")
	if err != nil {
		t.Fatal(err)
	}
	return folder
}
