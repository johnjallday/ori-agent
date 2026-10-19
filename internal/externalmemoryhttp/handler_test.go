package externalmemoryhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/externalmemory"
)

func newTestHandler(t *testing.T) (*Handler, *externalmemory.Runner, *http.ServeMux) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "externalmemory", "testdata", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	store := externalmemory.NewStore(t.TempDir())
	runner := externalmemory.NewRunner(store, externalmemory.NewRegistry(externalmemory.NewCodexSource(root)),
		externalmemory.WithHome("/Users/example"))
	t.Cleanup(runner.WaitIdle)
	h := NewHandler(runner)
	return h, runner, routes(h)
}

func routes(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/external-memory/sources", h.GetSources)
	mux.HandleFunc("PATCH /api/external-memory/sources/{source}", h.PatchSource)
	mux.HandleFunc("POST /api/external-memory/sync", h.Sync)
	return mux
}

func do(t *testing.T, mux *http.ServeMux, method, path string, body any) (*httptest.ResponseRecorder, sourcesResponse) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var resp sourcesResponse
	if rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode %s %s: %v\n%s", method, path, err, rec.Body.String())
		}
	}
	return rec, resp
}

func codexCard(t *testing.T, resp sourcesResponse) sourceDTO {
	t.Helper()
	for _, s := range resp.Sources {
		if s.ID == externalmemory.SourceCodex {
			return s
		}
	}
	t.Fatalf("no codex card in %+v", resp.Sources)
	return sourceDTO{}
}

func TestGetSources_ShowsAvailableOffBeforeAnythingIsRead(t *testing.T) {
	_, _, mux := newTestHandler(t)
	rec, resp := do(t, mux, http.MethodGet, "/api/external-memory/sources", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	card := codexCard(t, resp)
	if card.Enabled || !card.Available || card.EntryCount != 0 || card.Kind != "files" || card.Label != "Codex" {
		t.Fatalf("fresh card = %+v", card)
	}
	if card.Exclusions == nil || card.NextDue != nil || resp.SyncIntervalHours != 24 {
		t.Fatalf("fresh card should have an empty exclusion list and no next due: %+v", card)
	}
}

func TestPatchSource_EnablingSyncsInTheBackground(t *testing.T) {
	_, runner, mux := newTestHandler(t)
	rec, resp := do(t, mux, http.MethodPatch, "/api/external-memory/sources/codex", map[string]any{"enabled": true})
	if rec.Code != http.StatusOK || !codexCard(t, resp).Enabled {
		t.Fatalf("enable: %d %+v", rec.Code, resp)
	}
	waitIdleWithResult(t, runner)
	_, resp = do(t, mux, http.MethodGet, "/api/external-memory/sources", nil)
	card := codexCard(t, resp)
	if card.EntryCount != 23 || card.FolderCount != 2 || card.LastResult != externalmemory.ResultOK || card.NextDue == nil {
		t.Fatalf("after enabling: %+v", card)
	}
	if card.Folders[0].Display != "~/Projects/chart-lab" {
		t.Fatalf("folders = %+v", card.Folders)
	}
}

func TestPatchSource_DisablingHidesAtOnceAndKeepsTheMirror(t *testing.T) {
	_, runner, mux := newTestHandler(t)
	do(t, mux, http.MethodPatch, "/api/external-memory/sources/codex", map[string]any{"enabled": true})
	waitIdleWithResult(t, runner)
	_, resp := do(t, mux, http.MethodPatch, "/api/external-memory/sources/codex", map[string]any{"enabled": false})
	if codexCard(t, resp).Enabled {
		t.Fatal("still enabled")
	}
	if visible := externalmemory.VisibleEntries(runner.Store(), runner.Home()); len(visible) != 0 {
		t.Fatalf("disabled source still visible: %d entries", len(visible))
	}
	if stored := runner.Store().LoadEntries(externalmemory.SourceCodex); len(stored) != 23 {
		t.Fatalf("disabling should not delete the mirror: %d entries", len(stored))
	}
}

func TestPatchSource_ExclusionsAreValidatedAndApplied(t *testing.T) {
	_, runner, mux := newTestHandler(t)
	do(t, mux, http.MethodPatch, "/api/external-memory/sources/codex", map[string]any{"enabled": true})
	waitIdleWithResult(t, runner)

	rec, _ := do(t, mux, http.MethodPatch, "/api/external-memory/sources/codex", map[string]any{"exclusions": []string{"relative/dir"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("relative exclusion: status %d", rec.Code)
	}
	rec, resp := do(t, mux, http.MethodPatch, "/api/external-memory/sources/codex",
		map[string]any{"exclusions": []string{" ~/Projects/notes-app ", "~/Projects/notes-app/", ""}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	card := codexCard(t, resp)
	if len(card.Exclusions) != 1 || card.Exclusions[0] != "~/Projects/notes-app" {
		t.Fatalf("exclusions = %v", card.Exclusions)
	}
	// Hidden at once, before the queued sync marks them removed.
	if card.FolderCount != 1 {
		t.Fatalf("excluded folder still counted: %+v", card.Folders)
	}
}

func TestPatchSource_UnknownSourceAndBadBody(t *testing.T) {
	_, _, mux := newTestHandler(t)
	if rec, _ := do(t, mux, http.MethodPatch, "/api/external-memory/sources/nope", map[string]any{"enabled": true}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown source: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/external-memory/sources/codex", bytes.NewBufferString("{"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", rec.Code)
	}
}

func TestSync_RunsAndReturnsTheCards(t *testing.T) {
	_, runner, mux := newTestHandler(t)
	if _, err := runner.Store().UpdateSources(func(doc *externalmemory.SourcesDocument) error {
		doc.Sources[externalmemory.SourceCodex] = externalmemory.SourceState{Enabled: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec, resp := do(t, mux, http.MethodPost, "/api/external-memory/sync", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	card := codexCard(t, resp)
	if card.EntryCount != 23 || card.LastChanges.Added != 23 || resp.LastCompletedSync == nil || resp.Sync.Running {
		t.Fatalf("after sync: %+v", card)
	}
	_, resp = do(t, mux, http.MethodPost, "/api/external-memory/sync", nil)
	if changes := codexCard(t, resp).LastChanges; changes != (changesDTO{}) {
		t.Fatalf("second sync changes = %+v", changes)
	}
}

func TestSync_ReportsTheSyncInFlight(t *testing.T) {
	store := externalmemory.NewStore(t.TempDir())
	source := &gatedSource{gate: make(chan struct{}), entered: make(chan struct{})}
	if _, err := store.UpdateSources(func(doc *externalmemory.SourcesDocument) error {
		doc.Sources[externalmemory.SourceCodex] = externalmemory.SourceState{Enabled: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := externalmemory.NewRunner(store, externalmemory.NewRegistry(source))
	t.Cleanup(runner.WaitIdle)
	mux := routes(NewHandler(runner))
	go func() { _, _ = runner.Run(context.Background(), externalmemory.ReasonSchedule) }()
	<-source.entered
	rec, resp := do(t, mux, http.MethodPost, "/api/external-memory/sync", nil)
	close(source.gate)
	if rec.Code != http.StatusAccepted || !resp.Sync.Running || resp.Sync.Reason != externalmemory.ReasonSchedule {
		t.Fatalf("in-flight sync: %d %+v", rec.Code, resp.Sync)
	}
}

func TestHandler_NilRunnerIsUnavailable(t *testing.T) {
	mux := routes(NewHandler(nil))
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/external-memory/sources"},
		{http.MethodPost, "/api/external-memory/sync"},
	} {
		rec, _ := do(t, mux, tc.method, tc.path, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: %d", tc.method, tc.path, rec.Code)
		}
	}
}

type gatedSource struct {
	gate    chan struct{}
	entered chan struct{}
}

func (g *gatedSource) ID() string       { return externalmemory.SourceCodex }
func (g *gatedSource) Location() string { return "/fake" }
func (g *gatedSource) Available() bool  { return true }
func (g *gatedSource) Read(context.Context) ([]externalmemory.Entry, []externalmemory.ParseError, error) {
	close(g.entered)
	<-g.gate
	return nil, nil, nil
}

func waitIdleWithResult(t *testing.T, runner *externalmemory.Runner) {
	t.Helper()
	runner.WaitIdle()
	if runner.Store().LoadSources().Source(externalmemory.SourceCodex).LastResult == "" {
		t.Fatal("background sync did not record a result")
	}
}
