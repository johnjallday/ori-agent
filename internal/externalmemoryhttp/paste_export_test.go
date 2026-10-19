package externalmemoryhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/externalmemory"
)

func pasteHandler(t *testing.T) (*Handler, *externalmemory.Runner, *http.ServeMux) {
	t.Helper()
	store := externalmemory.NewStore(t.TempDir())
	runner := externalmemory.NewRunner(store, externalmemory.NewRegistry(externalmemory.NewClaudeAccountSource(store)))
	t.Cleanup(runner.WaitIdle)
	h := NewHandler(runner)
	mux := routes(h)
	mux.HandleFunc("POST /api/external-memory/sources/claude_account/paste", h.PasteClaudeAccount)
	mux.HandleFunc("GET /api/external-memory/export/claude", h.GetClaudeExport)
	return h, runner, mux
}

func pasteFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "externalmemory", "testdata", "account", "export.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func postPaste(t *testing.T, mux *http.ServeMux, text string) (*httptest.ResponseRecorder, sourcesResponse) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"text": text})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/external-memory/sources/claude_account/paste", bytes.NewReader(body)))
	var resp sourcesResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
	}
	return rec, resp
}

func accountCard(t *testing.T, resp sourcesResponse) sourceDTO {
	t.Helper()
	for _, s := range resp.Sources {
		if s.ID == externalmemory.SourceClaudeAccount {
			return s
		}
	}
	t.Fatalf("no account card: %+v", resp.Sources)
	return sourceDTO{}
}

func TestPasteClaudeAccount_SplitsEnablesAndReplaces(t *testing.T) {
	_, runner, mux := pasteHandler(t)
	_, before := do(t, mux, http.MethodGet, "/api/external-memory/sources", nil)
	if card := accountCard(t, before); card.Kind != "paste" || card.Available || card.Enabled || card.NextDue != nil {
		t.Fatalf("account card before any paste: %+v", card)
	}

	rec, resp := postPaste(t, mux, pasteFixture(t))
	card := accountCard(t, resp)
	if rec.Code != http.StatusOK || !card.Enabled || !card.Available || card.EntryCount != 7 || card.PasteDate == nil || card.LastChanges.Added != 7 {
		t.Fatalf("first paste: %d %+v", rec.Code, card)
	}
	if card.NextDue != nil {
		t.Fatal("the paste source has no schedule")
	}
	visible := externalmemory.VisibleEntries(runner.Store(), runner.Home())
	if len(visible) != 7 {
		t.Fatalf("pasted entries not visible to readers: %d", len(visible))
	}

	// A second paste replaces the first: one line dropped, one added.
	edited := strings.Replace(pasteFixture(t), "[2026-07-20] - Prefers plain language over jargon.", "[2026-10-19] - Prefers short answers.", 1)
	_, resp = postPaste(t, mux, edited)
	if changes := accountCard(t, resp).LastChanges; changes.Added != 1 || changes.Removed != 1 || changes.Updated != 0 {
		t.Fatalf("re-paste changes = %+v", changes)
	}
	if store := runner.Store(); store.LoadSources().LastCompletedSync != nil {
		t.Fatal("a paste armed the daily file-source timer")
	}
}

func TestPasteClaudeAccount_RefusesEmptyAndOversized(t *testing.T) {
	_, runner, mux := pasteHandler(t)
	if rec, _ := postPaste(t, mux, "   "); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty paste: %d", rec.Code)
	}
	huge := strings.Repeat("- a remembered fact\n", externalmemory.MaxPasteBytes/19+10)
	rec, _ := postPaste(t, mux, huge)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "256 KB") {
		t.Fatalf("oversized paste: %d %s", rec.Code, rec.Body.String())
	}
	if runner.Store().LoadSources().Source(externalmemory.SourceClaudeAccount).Enabled {
		t.Fatal("a refused paste turned the source on")
	}
}

type fakeExport struct {
	profile externalmemory.ExportProfile
	facts   []externalmemory.ExportFact
	err     error
}

func (f fakeExport) ClaudeExport(context.Context) (externalmemory.ExportProfile, []externalmemory.ExportFact, error) {
	return f.profile, f.facts, f.err
}

func TestGetClaudeExport_ServesTheImportText(t *testing.T) {
	h, _, mux := pasteHandler(t)
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/external-memory/export/claude", nil))
		return rec
	}
	if rec := get(); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired export: %d", rec.Code)
	}
	h.SetExportSource(fakeExport{
		profile: externalmemory.ExportProfile{Name: "Sam", Date: "2026-09-30"},
		facts:   []externalmemory.ExportFact{{Category: "projects", Text: "chart-lab backtests.", Date: "2026-10-05"}},
	})
	rec := get()
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("export: %d %v", rec.Code, rec.Header())
	}
	body := rec.Body.String()
	for _, want := range []string{"## Instructions\n", "## Identity\n[2026-09-30] - Name: Sam\n", "## Projects\n[2026-10-05] - chart-lab backtests.\n", "## Preferences\n"} {
		if !strings.Contains(body, want) {
			t.Fatalf("export missing %q:\n%s", want, body)
		}
	}
	h.SetExportSource(fakeExport{err: errors.New("store down")})
	if rec := get(); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("failing export source: %d", rec.Code)
	}
}
