package externalmemoryhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/externalmemory"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type fakeGlobal struct {
	requests []personalassistant.ExternalPromoteRequest
	result   personalassistant.WorkspacePromoteResult
	itemID   string
	err      error
}

func (f *fakeGlobal) PromoteExternal(_ context.Context, req personalassistant.ExternalPromoteRequest) (personalassistant.WorkspacePromoteResult, personalassistant.KnowledgeItem, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return "", personalassistant.KnowledgeItem{}, f.err
	}
	if req.PromotedItemID != "" {
		return personalassistant.WorkspacePromoteAlreadyKnown, personalassistant.KnowledgeItem{ID: req.PromotedItemID}, nil
	}
	return f.result, personalassistant.KnowledgeItem{ID: f.itemID}, nil
}

func syncedHandler(t *testing.T) (*Handler, *externalmemory.Runner, *http.ServeMux) {
	t.Helper()
	h, runner, _ := newTestHandler(t)
	if _, err := runner.Store().UpdateSources(func(doc *externalmemory.SourcesDocument) error {
		doc.Sources[externalmemory.SourceCodex] = externalmemory.SourceState{Enabled: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), externalmemory.ReasonManual); err != nil {
		t.Fatal(err)
	}
	mux := routes(h)
	mux.HandleFunc("GET /api/external-memory/entries", h.GetEntries)
	mux.HandleFunc("POST /api/external-memory/entries/{id}/promote", h.PromoteEntry)
	return h, runner, mux
}

func getEntries(t *testing.T, mux *http.ServeMux, query string) entriesResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/external-memory/entries?"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("entries %q: %d %s", query, rec.Code, rec.Body.String())
	}
	var resp entriesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestGetEntries_SearchesVisibleEntries(t *testing.T) {
	_, runner, mux := syncedHandler(t)
	resp := getEntries(t, mux, "q=fee+sensitivity&limit=5")
	if resp.Total == 0 || len(resp.Entries) == 0 || resp.Limit != 5 {
		t.Fatalf("search = %+v", resp)
	}
	first := resp.Entries[0]
	if first.Source != "codex" || first.SourceLabel != "Codex" || first.Excerpt == "" || first.SuggestedText == "" || first.SuggestedCategory == "" {
		t.Fatalf("first hit = %+v", first)
	}
	if first.FolderDisplay != "" && !strings.HasPrefix(first.FolderDisplay, "~/") {
		t.Fatalf("folder display = %q", first.FolderDisplay)
	}
	if resp.Facets.Sources["codex"] != resp.Total {
		t.Fatalf("facets = %+v", resp.Facets)
	}

	filtered := getEntries(t, mux, "folder=~/Projects/notes-app&limit=100")
	for _, e := range filtered.Entries {
		if !strings.HasPrefix(e.Folder, "/Users/example/Projects/notes-app") {
			t.Fatalf("folder filter leaked %q", e.Folder)
		}
	}
	if filtered.Total == 0 {
		t.Fatal("folder filter found nothing")
	}

	// A disabled source is hidden from the browser at once.
	if _, err := runner.Store().UpdateSources(func(doc *externalmemory.SourcesDocument) error {
		doc.Sources[externalmemory.SourceCodex] = externalmemory.SourceState{Enabled: false}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if off := getEntries(t, mux, ""); off.Total != 0 || len(off.Entries) != 0 {
		t.Fatalf("disabled source still browsable: %d", off.Total)
	}
}

func TestGetEntries_PagesNewestFirst(t *testing.T) {
	_, _, mux := syncedHandler(t)
	all := getEntries(t, mux, "limit=100")
	page := getEntries(t, mux, "limit=5&offset=5")
	if all.Total != 23 || len(page.Entries) != 5 || page.Entries[0].ID != all.Entries[5].ID {
		t.Fatalf("paging: total %d, page %d", all.Total, len(page.Entries))
	}
	for i := 1; i < len(all.Entries); i++ {
		if all.Entries[i].Date != "" && all.Entries[i-1].Date != "" && all.Entries[i].Date > all.Entries[i-1].Date {
			t.Fatalf("not newest first at %d: %s after %s", i, all.Entries[i].Date, all.Entries[i-1].Date)
		}
	}
}

func promote(t *testing.T, mux *http.ServeMux, id string, body any) (*httptest.ResponseRecorder, promoteResponse) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/external-memory/entries/"+id+"/promote", reader))
	var resp promoteResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
	}
	return rec, resp
}

func TestPromoteEntry_PromotesThenLinksTheSameItem(t *testing.T) {
	h, runner, mux := syncedHandler(t)
	global := &fakeGlobal{result: personalassistant.WorkspacePromoted, itemID: "item-1"}
	h.SetGlobalMemory(global)
	target := getEntries(t, mux, "q=EMA20+close+below+EMA50").Entries[0]

	rec, resp := promote(t, mux, target.ID, map[string]string{"text": "Exits on EMA20 below EMA50 beat a 3% stop after fees.", "category": "projects"})
	if rec.Code != http.StatusOK || !resp.Created || resp.ItemID != "item-1" || resp.Entry.PromotedItemID != "item-1" || resp.Href != profileGlobalMemoryHref {
		t.Fatalf("promote: %d %+v", rec.Code, resp)
	}
	sent := global.requests[0]
	if sent.Source != "codex" || sent.EntryID != target.ID || sent.Category != "projects" || sent.Title != target.Title {
		t.Fatalf("request = %+v", sent)
	}
	stored := findStored(t, runner, target.ID)
	if stored.PromotedItemID != "item-1" {
		t.Fatalf("mirror entry not linked: %+v", stored)
	}

	// Promoting again links the same item and passes the link along.
	rec, resp = promote(t, mux, target.ID, nil)
	if rec.Code != http.StatusOK || resp.Created || resp.ItemID != "item-1" || resp.Status != string(personalassistant.WorkspacePromoteAlreadyKnown) {
		t.Fatalf("second promote: %d %+v", rec.Code, resp)
	}
	if global.requests[1].PromotedItemID != "item-1" {
		t.Fatalf("second request did not carry the link: %+v", global.requests[1])
	}
	// The link survives a re-sync.
	if _, err := runner.Run(context.Background(), externalmemory.ReasonManual); err != nil {
		t.Fatal(err)
	}
	if findStored(t, runner, target.ID).PromotedItemID != "item-1" {
		t.Fatal("re-sync dropped the promoted link")
	}
}

func TestPromoteEntry_DefaultsWordingAndCategory(t *testing.T) {
	h, _, mux := syncedHandler(t)
	global := &fakeGlobal{result: personalassistant.WorkspacePromoted, itemID: "item-2"}
	h.SetGlobalMemory(global)
	pref := getEntries(t, mux, "category=user-preferences&q=planning+implementation").Entries[0]
	if rec, _ := promote(t, mux, pref.ID, nil); rec.Code != http.StatusOK {
		t.Fatalf("promote with defaults: %d", rec.Code)
	}
	sent := global.requests[0]
	if sent.Category != "how_you_work" || sent.Text != pref.SuggestedText || sent.Text == "" {
		t.Fatalf("defaults = %+v (suggested %q)", sent, pref.SuggestedText)
	}
}

func TestPromoteEntry_Errors(t *testing.T) {
	h, _, mux := syncedHandler(t)
	target := getEntries(t, mux, "q=notarization").Entries[0]

	if rec, _ := promote(t, mux, target.ID, nil); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "global_memory_unavailable") {
		t.Fatalf("no global memory wired: %d %s", rec.Code, rec.Body.String())
	}
	if rec, _ := promote(t, mux, "nope", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown entry: %d", rec.Code)
	}
	cases := map[error]int{
		personalassistant.ErrGlobalMemoryUnavailable: http.StatusConflict,
		personalassistant.ErrValidation:              http.StatusBadRequest,
		personalassistant.ErrConflict:                http.StatusConflict,
		personalassistant.ErrKnowledgeLimit:          http.StatusConflict,
		context.DeadlineExceeded:                     http.StatusServiceUnavailable,
	}
	for err, want := range cases {
		h.SetGlobalMemory(&fakeGlobal{err: err})
		if rec, _ := promote(t, mux, target.ID, nil); rec.Code != want {
			t.Fatalf("%v: status %d, want %d", err, rec.Code, want)
		}
	}
}

func TestSuggestPromotion(t *testing.T) {
	long := strings.Repeat("word ", 200)
	text, category := suggestPromotion(externalmemory.Entry{Source: "claude_code", Category: "feedback", Body: "\n# Heading\n\n- " + long})
	if len(text) > maxSuggestedBytes+len("…") || !strings.HasPrefix(text, "Heading") {
		t.Fatalf("suggested text = %q (%d bytes)", text, len(text))
	}
	if category != "how_you_work" {
		t.Fatalf("feedback category = %q", category)
	}
	for _, tc := range []struct {
		source, category, want string
	}{
		{"codex", "user-profile", "how_you_work"},
		{"codex", "failures-and-how-to-do-differently", "how_you_work"},
		{"codex", "task", "projects"},
		{"claude_code", "project", "projects"},
		{"gemini_cli", "instruction", "how_you_work"},
		{"claude_account", "personal-identity", "how_you_work"},
		{"claude_account", "projects", "projects"},
	} {
		if got := suggestedCategory(externalmemory.Entry{Source: tc.source, Category: tc.category}); got != tc.want {
			t.Errorf("%s/%s = %q, want %q", tc.source, tc.category, got, tc.want)
		}
	}
}

func findStored(t *testing.T, runner *externalmemory.Runner, id string) externalmemory.Entry {
	t.Helper()
	for _, e := range runner.Store().LoadEntries(externalmemory.SourceCodex) {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("entry %s not stored", id)
	return externalmemory.Entry{}
}
