package pluginhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/plugin"
)

type stubReg struct{ added map[string]mcp.ServerConfig }

func (s *stubReg) AddServer(c mcp.ServerConfig) error {
	if s.added == nil {
		s.added = map[string]mcp.ServerConfig{}
	}
	s.added[c.Name] = c
	return nil
}
func (s *stubReg) RemoveServer(name string) error { delete(s.added, name); return nil }

type stubSkills struct{}

func (stubSkills) InstallSkill(_, _, _ string) error { return nil }
func (stubSkills) RemoveSkill(_, _ string) error     { return nil }

func testHandler(t *testing.T) *Handler {
	t.Helper()
	mgr := plugin.NewManager(&stubReg{}, stubSkills{}, t.TempDir(), "")
	return newHandlerWithManager(mgr)
}

func claudeBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.1.0"}`)
	mustWrite(t, filepath.Join(root, ".mcp.json"), `{"ori-reaper":{"command":"/usr/bin/true"}}`)
	mustWrite(t, filepath.Join(root, "skills", "s1", "SKILL.md"), "---\nname: s1\n---\n")
	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func postInstall(t *testing.T, h *Handler, source string, confirm bool) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"source":"` + source + `","confirm":` + map[bool]string{true: "true", false: "false"}[confirm] + `}`
	rr := httptest.NewRecorder()
	h.InstallHandler(rr, httptest.NewRequest(http.MethodPost, "/api/plugins/install", strings.NewReader(body)))
	return rr
}

func listBody(t *testing.T, h *Handler) string {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ListHandler(rr, httptest.NewRequest(http.MethodGet, "/api/plugins", nil))
	return rr.Body.String()
}

func TestInstallPreviewMakesNoChanges(t *testing.T) {
	h := testHandler(t)
	rr := postInstall(t, h, claudeBundle(t), false)
	if rr.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"installed":false`) {
		t.Errorf("expected installed:false, got %s", rr.Body.String())
	}
	if strings.Contains(listBody(t, h), "reaper") {
		t.Error("preview must not record a plugin")
	}
}

func TestInstallConfirmThenList(t *testing.T) {
	h := testHandler(t)
	rr := postInstall(t, h, claudeBundle(t), true)
	if rr.Code != http.StatusOK {
		t.Fatalf("install status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"installed":true`) {
		t.Errorf("expected installed:true, got %s", rr.Body.String())
	}
	if !strings.Contains(listBody(t, h), "reaper") {
		t.Errorf("expected reaper in list, got %s", listBody(t, h))
	}
}

func TestMarketplacesOfficialStatus(t *testing.T) {
	h := testHandler(t)

	getMarketplaces := func() string {
		t.Helper()
		rr := httptest.NewRecorder()
		h.MarketplacesHandler(rr, httptest.NewRequest(http.MethodGet, "/api/plugins/marketplaces", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET marketplaces: %d %s", rr.Code, rr.Body.String())
		}
		return rr.Body.String()
	}

	// Before adding: official block present, source exposed, added=false.
	body := getMarketplaces()
	if !strings.Contains(body, plugin.OfficialMarketplaceSource) {
		t.Errorf("official source not exposed: %s", body)
	}
	if !strings.Contains(body, `"added":false`) {
		t.Errorf("expected added:false, got %s", body)
	}

	// Add a local catalog whose name matches the official marketplace name; the
	// status must flip to added=true (idempotent add keyed by name).
	catalog := t.TempDir()
	mustWrite(t, filepath.Join(catalog, "marketplace.json"),
		`{"name":"`+plugin.OfficialMarketplaceName+`","plugins":[]}`)
	addBody := `{"source":"` + catalog + `"}`
	rr := httptest.NewRecorder()
	h.MarketplacesHandler(rr, httptest.NewRequest(http.MethodPost, "/api/plugins/marketplaces", strings.NewReader(addBody)))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST marketplace: %d %s", rr.Code, rr.Body.String())
	}
	// Re-add to prove idempotency (no duplicate record).
	rr = httptest.NewRecorder()
	h.MarketplacesHandler(rr, httptest.NewRequest(http.MethodPost, "/api/plugins/marketplaces", strings.NewReader(addBody)))
	if rr.Code != http.StatusOK {
		t.Fatalf("re-add marketplace: %d %s", rr.Code, rr.Body.String())
	}

	// Each added marketplace record carries a "dir" field (the official status
	// block does not), so this counts records — proving the re-add didn't dupe.
	if got := strings.Count(getMarketplaces(), `"dir":`); got != 1 {
		t.Errorf("expected exactly 1 marketplace record, got %d: %s", got, getMarketplaces())
	}
	if !strings.Contains(getMarketplaces(), `"added":true`) {
		t.Errorf("expected added:true after add, got %s", getMarketplaces())
	}
}

func cachedUpdates(t *testing.T, h *Handler) plugin.UpdateSnapshot {
	t.Helper()
	rr := httptest.NewRecorder()
	h.UpdateStatusHandler(rr, httptest.NewRequest(http.MethodGet, "/api/plugins/updates", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("update status: %d %s", rr.Code, rr.Body.String())
	}
	var snapshot plugin.UpdateSnapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode update status: %v", err)
	}
	return snapshot
}

func primeAvailableUpdate(t *testing.T, h *Handler, source string) {
	t.Helper()
	mustWrite(t, filepath.Join(source, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.2.0"}`)
	h.UpdateChecker().Start(time.Hour)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := h.UpdateChecker().Snapshot()
		if len(snapshot.Updates) == 1 && snapshot.Updates[0].Available {
			h.UpdateChecker().Stop()
			return
		}
		time.Sleep(time.Millisecond)
	}
	h.UpdateChecker().Stop()
	t.Fatalf("available update was not cached: %+v", h.UpdateChecker().Snapshot())
}

func TestUpdateStatusHandlerReadsCacheOnly(t *testing.T) {
	h := testHandler(t)
	source := claudeBundle(t)
	if rr := postInstall(t, h, source, true); rr.Code != http.StatusOK {
		t.Fatalf("install: %d %s", rr.Code, rr.Body.String())
	}
	mustWrite(t, filepath.Join(source, ".claude-plugin", "plugin.json"), `{"name":"reaper","version":"0.2.0"}`)

	// Repeated reads before the checker runs stay empty even though the source
	// changed, proving this handler does not resolve the source itself.
	for range 2 {
		snapshot := cachedUpdates(t, h)
		if snapshot.Checking || snapshot.LastSuccessfulCheckAt != nil || len(snapshot.Updates) != 0 {
			t.Fatalf("cold cached snapshot = %+v", snapshot)
		}
	}

	rr := httptest.NewRecorder()
	h.UpdateStatusHandler(rr, httptest.NewRequest(http.MethodPost, "/api/plugins/updates", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST update status = %d, want 405", rr.Code)
	}
}

func TestSuccessfulPluginMutationsInvalidateCachedUpdate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *Handler, string) *httptest.ResponseRecorder
	}{
		{
			name: "install",
			mutate: func(t *testing.T, h *Handler, source string) *httptest.ResponseRecorder {
				return postInstall(t, h, source, true)
			},
		},
		{
			name: "marketplace install",
			mutate: func(t *testing.T, h *Handler, source string) *httptest.ResponseRecorder {
				catalog := t.TempDir()
				mustWrite(t, filepath.Join(catalog, "marketplace.json"), `{"name":"local","plugins":[{"name":"reaper","source":"`+source+`"}]}`)
				add := httptest.NewRecorder()
				h.MarketplacesHandler(add, httptest.NewRequest(http.MethodPost, "/api/plugins/marketplaces", strings.NewReader(`{"source":"`+catalog+`"}`)))
				if add.Code != http.StatusOK {
					t.Fatalf("add marketplace: %d %s", add.Code, add.Body.String())
				}
				rr := httptest.NewRecorder()
				h.MarketplaceInstallHandler(rr, httptest.NewRequest(http.MethodPost, "/api/plugins/marketplaces/install", strings.NewReader(`{"marketplace":"local","plugin":"reaper","confirm":true}`)))
				return rr
			},
		},
		{
			name: "update",
			mutate: func(t *testing.T, h *Handler, _ string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, "/api/plugins/reaper/update", strings.NewReader(`{"confirm":true}`))
				req.SetPathValue("name", "reaper")
				rr := httptest.NewRecorder()
				h.UpdateHandler(rr, req)
				return rr
			},
		},
		{
			name: "uninstall",
			mutate: func(t *testing.T, h *Handler, _ string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodDelete, "/api/plugins/reaper", nil)
				req.SetPathValue("name", "reaper")
				rr := httptest.NewRecorder()
				h.UninstallHandler(rr, req)
				return rr
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandler(t)
			source := claudeBundle(t)
			if rr := postInstall(t, h, source, true); rr.Code != http.StatusOK {
				t.Fatalf("initial install: %d %s", rr.Code, rr.Body.String())
			}
			primeAvailableUpdate(t, h, source)
			if rr := tc.mutate(t, h, source); rr.Code != http.StatusOK {
				t.Fatalf("mutation: %d %s", rr.Code, rr.Body.String())
			}
			if snapshot := cachedUpdates(t, h); len(snapshot.Updates) != 0 {
				t.Fatalf("successful mutation retained cached update: %+v", snapshot)
			}
		})
	}
}

func TestDeclinedFailedAndEnableMutationsKeepCachedUpdate(t *testing.T) {
	h := testHandler(t)
	source := claudeBundle(t)
	if rr := postInstall(t, h, source, true); rr.Code != http.StatusOK {
		t.Fatalf("install: %d %s", rr.Code, rr.Body.String())
	}
	primeAvailableUpdate(t, h, source)

	previewReq := httptest.NewRequest(http.MethodPost, "/api/plugins/reaper/update", strings.NewReader(`{"confirm":false}`))
	previewReq.SetPathValue("name", "reaper")
	preview := httptest.NewRecorder()
	h.UpdateHandler(preview, previewReq)
	if preview.Code != http.StatusOK {
		t.Fatalf("update preview: %d %s", preview.Code, preview.Body.String())
	}

	enableReq := httptest.NewRequest(http.MethodPost, "/api/plugins/reaper/enable", nil)
	enableReq.SetPathValue("name", "reaper")
	enabled := httptest.NewRecorder()
	h.SetEnabledHandler(true)(enabled, enableReq)
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", enabled.Code, enabled.Body.String())
	}

	mustWrite(t, filepath.Join(source, ".claude-plugin", "plugin.json"), `{not json`)
	failedReq := httptest.NewRequest(http.MethodPost, "/api/plugins/reaper/update", strings.NewReader(`{"confirm":true}`))
	failedReq.SetPathValue("name", "reaper")
	failed := httptest.NewRecorder()
	h.UpdateHandler(failed, failedReq)
	if failed.Code == http.StatusOK {
		t.Fatalf("malformed update unexpectedly succeeded: %s", failed.Body.String())
	}

	if snapshot := cachedUpdates(t, h); len(snapshot.Updates) != 1 || !snapshot.Updates[0].Available {
		t.Fatalf("non-successful mutation cleared cached update: %+v", snapshot)
	}
}

func TestUninstall(t *testing.T) {
	h := testHandler(t)
	if rr := postInstall(t, h, claudeBundle(t), true); rr.Code != http.StatusOK {
		t.Fatalf("install: %d %s", rr.Code, rr.Body.String())
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/plugins/reaper", nil)
	req.SetPathValue("name", "reaper")
	rr := httptest.NewRecorder()
	h.UninstallHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("uninstall: %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(listBody(t, h), "reaper") {
		t.Error("reaper should be gone after uninstall")
	}
}
