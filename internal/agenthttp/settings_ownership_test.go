package agenthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const ownershipTestSecret = "sk-http-ownership-secret"

// ownershipWriteSpy fails the test on any workspace write.
type ownershipWriteSpy struct {
	workspace.Store
	t      *testing.T
	mu     sync.Mutex
	writes int
}

func (s *ownershipWriteSpy) fail(what string) {
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()
	s.t.Errorf("inspection wrote: %s", what)
}
func (s *ownershipWriteSpy) Save(ws *workspace.Workspace) error { s.fail("Save"); return nil }
func (s *ownershipWriteSpy) Delete(string) error                { s.fail("Delete"); return nil }
func (s *ownershipWriteSpy) Update(string, func(*workspace.Workspace) error) error {
	s.fail("Update")
	return nil
}
func (s *ownershipWriteSpy) SaveWorkspaceAgent(string, string, *agent.Agent) error {
	s.fail("SaveWorkspaceAgent")
	return nil
}

// librarySpy fails the test on any library write.
type librarySpy struct {
	store.Store
	t *testing.T
}

func (s librarySpy) SetAgent(string, *agent.Agent) error {
	s.t.Error("inspection SetAgent")
	return nil
}
func (s librarySpy) UpdateAgent(string, func(*agent.Agent) error) error {
	s.t.Error("inspection UpdateAgent")
	return nil
}
func (s librarySpy) CreateAgent(string, *store.CreateAgentConfig) error {
	s.t.Error("inspection CreateAgent")
	return nil
}
func (s librarySpy) DeleteAgent(string) error { s.t.Error("inspection DeleteAgent"); return nil }
func (s librarySpy) Save() error              { s.t.Error("inspection Save"); return nil }

type fakeTracking struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeTracking) CopyTracking(_, workspaceID string, _, _ *agent.Agent) (string, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if workspaceID == "ws-custom" {
		return "customized", nil
	}
	return "untracked", nil
}

func ownershipHandler(t *testing.T) (*DashboardHandler, *ownershipWriteSpy, *fakeTracking) {
	t.Helper()
	lib, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{Model: "default-model"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.CreateAgent("Writer Ü", &store.CreateAgentConfig{Model: "library-model", SystemPrompt: "Library prompt.", LLMProvider: "openai"}); err != nil {
		t.Fatal(err)
	}
	if err := lib.UpdateAgent("Writer Ü", func(ag *agent.Agent) error {
		ag.Settings.APIKey = ownershipTestSecret
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mem := workspace.NewInMemoryStore()
	now := time.Now()
	for _, id := range []string{"ws-custom", "ws-fallback"} {
		ws := &workspace.Workspace{ID: id, Name: id + " <b>", FolderSlug: id, Status: workspace.StatusActive, CreatedAt: now, UpdatedAt: now,
			AgentInstances: []workspace.AgentInstance{{ID: "i-" + id, Name: "Writer Ü", InstanceNumber: 1}}}
		if err := mem.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	if err := mem.SaveWorkspaceAgent("ws-custom", "Writer Ü", &agent.Agent{Settings: types.Settings{Model: "custom-model", APIKey: ownershipTestSecret}}); err != nil {
		t.Fatal(err)
	}
	spy := &ownershipWriteSpy{Store: mem, t: t}
	tracking := &fakeTracking{}
	h := NewDashboardHandler(librarySpy{Store: lib, t: t})
	h.SetWorkspaceStore(spy)
	h.SetCopyTrackingReader(tracking)
	return h, spy, tracking
}

func getOwnership(t *testing.T, h *DashboardHandler, method, target string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.GetSettingsOwnership(rec, httptest.NewRequest(method, target, nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

func modelOf(t *testing.T, body map[string]any) (any, string) {
	t.Helper()
	def, _ := body["definition"].(map[string]any)
	fields, _ := def["fields"].([]any)
	for _, raw := range fields {
		f := raw.(map[string]any)
		if f["key"] == "model" {
			return f["value"], f["comparison"].(string)
		}
	}
	t.Fatalf("no model field in %v", body)
	return nil, ""
}

const writerPath = "/api/agents/Writer%20%C3%9C/settings-ownership"

func TestSettingsOwnershipHTTPContexts(t *testing.T) {
	h, _, tracking := ownershipHandler(t)

	rec, body := getOwnership(t, h, http.MethodGet, writerPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("library status = %d %s", rec.Code, rec.Body)
	}
	if v, cmp := modelOf(t, body); v != "library-model" || cmp != "not_compared" {
		t.Fatalf("library model = %v %s", v, cmp)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}

	rec, body = getOwnership(t, h, http.MethodGet, writerPath+"?studio_id=ws-custom")
	if v, cmp := modelOf(t, body); rec.Code != 200 || v != "custom-model" || cmp != "different" {
		t.Fatalf("custom copy = %d %v %s", rec.Code, v, cmp)
	}
	if tr := body["tracking"].(map[string]any)["state"]; tr != "customized" || tracking.calls != 1 {
		t.Fatalf("tracking = %v (calls %d)", tr, tracking.calls)
	}

	rec, body = getOwnership(t, h, http.MethodGet, writerPath+"?studio_id=ws-fallback")
	if def := body["definition"].(map[string]any); rec.Code != 200 || def["source"] != "library_fallback" {
		t.Fatalf("fallback = %d %v", rec.Code, def)
	}

	// A workspace that is gone is a recoverable state, not another context.
	rec, body = getOwnership(t, h, http.MethodGet, writerPath+"?studio_id=gone")
	if ctx := body["context"].(map[string]any); rec.Code != 200 || ctx["state"] != "not_found" || ctx["workspace"] != nil {
		t.Fatalf("gone workspace = %d %v", rec.Code, ctx)
	}
}

func TestSettingsOwnershipHTTPRejectsBadInput(t *testing.T) {
	h, _, _ := ownershipHandler(t)
	cases := []struct {
		method, target string
		want           int
	}{
		{http.MethodPost, writerPath, http.StatusMethodNotAllowed},
		{http.MethodPut, writerPath, http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/agents/%20/settings-ownership", http.StatusBadRequest},
		{http.MethodGet, "/api/agents/a%2Fb/settings-ownership", http.StatusBadRequest},
		{http.MethodGet, writerPath + "?instance_id=i-ws-custom", http.StatusBadRequest},
		{http.MethodGet, writerPath + "?studio_id=../etc", http.StatusBadRequest},
		{http.MethodGet, writerPath + "?studio_id=" + strings.Repeat("x", 201), http.StatusBadRequest},
		{http.MethodGet, "/api/agents/Nobody/settings-ownership", http.StatusNotFound},
	}
	for _, tc := range cases {
		rec, _ := getOwnership(t, h, tc.method, tc.target)
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.target, rec.Code, tc.want)
		}
	}
}

func TestSettingsOwnershipHTTPNeverLeaksCredentials(t *testing.T) {
	h, _, _ := ownershipHandler(t)
	for _, q := range []string{"", "?studio_id=ws-custom", "?studio_id=ws-fallback"} {
		rec, _ := getOwnership(t, h, http.MethodGet, writerPath+q)
		for _, leaked := range []string{ownershipTestSecret, "api_key", "allow_web_search", "Settings"} {
			if strings.Contains(rec.Body.String(), leaked) {
				t.Fatalf("%s leaked %q: %s", q, leaked, rec.Body)
			}
		}
		// Names are JSON data, escaped by the encoder; the page renders them as text.
		if strings.Contains(rec.Body.String(), "<b>") {
			t.Fatalf("unescaped markup in %s", rec.Body)
		}
	}
}

func TestSettingsOwnershipHTTPConcurrentReadsWriteNothing(t *testing.T) {
	h, spy, _ := ownershipHandler(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, q := range []string{"", "?studio_id=ws-custom", "?studio_id=ws-fallback", "?studio_id=gone"} {
				rec, _ := getOwnership(t, h, http.MethodGet, writerPath+q)
				if rec.Code != http.StatusOK {
					t.Errorf("%s = %d", q, rec.Code)
				}
			}
		}()
	}
	wg.Wait()
	if spy.writes != 0 {
		t.Fatalf("%d workspace writes", spy.writes)
	}
}

// A different letter case of a library agent inspects that agent, never a
// "workspace-only" fiction.
func TestSettingsOwnershipHTTPCanonicalizesLibraryCase(t *testing.T) {
	h, _, _ := ownershipHandler(t)
	rec, body := getOwnership(t, h, http.MethodGet, "/api/agents/writer%20%C3%BC/settings-ownership")
	if rec.Code != http.StatusOK || body["agent"] != "Writer Ü" {
		t.Fatalf("case variant = %d %v", rec.Code, body["agent"])
	}
	if ctx := body["context"].(map[string]any); ctx["state"] != "ok" {
		t.Fatalf("case variant context = %v", ctx)
	}
}

func TestIsSettingsOwnershipPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/agents/Writer/settings-ownership":       true,
		"/api/agents/Writer Ü/settings-ownership":     true,
		"/api/agents//settings-ownership":             false,
		"/api/agents/a/b/settings-ownership":          false,
		"/api/agents/Writer/settings-ownership/extra": false,
		"/api/agents/Writer/detail":                   false,
	} {
		if got := IsSettingsOwnershipPath(path); got != want {
			t.Errorf("IsSettingsOwnershipPath(%q) = %v", path, got)
		}
	}
}
