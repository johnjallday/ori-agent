package settingshttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
)

type utilitySettingsEnvelope struct {
	Utility utilitySettingsResponse `json:"utility"`
}

func TestUtilitySettingsHandler_BrowserControlProviderPersisted(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "settings.json")
	configManager := config.NewManager(tmpFile)
	if err := configManager.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	body := bytes.NewBufferString(`{"browser_control_provider":"browserbase","playwright_browser":"webkit","playwright_executable_path":"/tmp/custom-browser"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/utility", body)
	rec := httptest.NewRecorder()
	handler.UtilitySettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var postResp utilitySettingsEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&postResp); err != nil {
		t.Fatalf("decode post response: %v", err)
	}
	if postResp.Utility.BrowserControlProvider != "browserbase" {
		t.Fatalf("expected browser_control_provider=browserbase, got %q", postResp.Utility.BrowserControlProvider)
	}
	if postResp.Utility.PlaywrightBrowser != "webkit" {
		t.Fatalf("expected playwright_browser=webkit, got %q", postResp.Utility.PlaywrightBrowser)
	}
	if postResp.Utility.PlaywrightExecutable != "/tmp/custom-browser" {
		t.Fatalf("expected playwright_executable_path to persist, got %q", postResp.Utility.PlaywrightExecutable)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/settings/utility", nil)
	getRec := httptest.NewRecorder()
	handler.UtilitySettingsHandler(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected status 200 from GET, got %d: %s", getRec.Code, getRec.Body.String())
	}

	var getResp utilitySettingsEnvelope
	if err := json.NewDecoder(getRec.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if getResp.Utility.BrowserControlProvider != "browserbase" {
		t.Fatalf("expected persisted browser_control_provider=browserbase, got %q", getResp.Utility.BrowserControlProvider)
	}
	if getResp.Utility.PlaywrightBrowser != "webkit" {
		t.Fatalf("expected persisted playwright_browser=webkit, got %q", getResp.Utility.PlaywrightBrowser)
	}
	if getResp.Utility.PlaywrightExecutable != "/tmp/custom-browser" {
		t.Fatalf("expected persisted playwright_executable_path, got %q", getResp.Utility.PlaywrightExecutable)
	}
}

func TestUtilitySettingsHandler_InvalidBrowserControlProviderFallsBackAuto(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "settings.json")
	configManager := config.NewManager(tmpFile)
	if err := configManager.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	body := bytes.NewBufferString(`{"browser_control_provider":"not-real","playwright_browser":"not-real"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/utility", body)
	rec := httptest.NewRecorder()
	handler.UtilitySettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp utilitySettingsEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Utility.BrowserControlProvider != "auto" {
		t.Fatalf("expected browser_control_provider fallback to auto, got %q", resp.Utility.BrowserControlProvider)
	}
	if resp.Utility.PlaywrightBrowser != "auto" {
		t.Fatalf("expected playwright_browser fallback to auto, got %q", resp.Utility.PlaywrightBrowser)
	}
}
