package settingshttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
)

func TestTemplatesRootSettingsHandler_Post(t *testing.T) {
	tmpDir := t.TempDir()
	configManager := config.NewManager(filepath.Join(tmpDir, "settings.json"))
	_ = configManager.Load()

	handler := NewHandler(nil, configManager, nil, llm.NewFactory())

	customRoot := filepath.Join(tmpDir, "custom-templates")
	body, _ := json.Marshal(map[string]string{"templates_root": customRoot})

	req := httptest.NewRequest(http.MethodPost, "/api/settings/templates-root", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.TemplatesRootSettingsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Success                bool   `json:"success"`
		TemplatesRoot          string `json:"templates_root"`
		EffectiveTemplatesRoot string `json:"effective_templates_root"`
		Source                 string `json:"source"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if !resp.Success || resp.TemplatesRoot != customRoot || resp.Source != "settings" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	// Changing the root materializes the library there (starters included).
	if _, err := os.Stat(filepath.Join(customRoot, "reaper-song")); err != nil {
		t.Fatalf("expected starter templates in new root: %v", err)
	}

	// GET reflects the persisted value.
	getReq := httptest.NewRequest(http.MethodGet, "/api/settings/templates-root", nil)
	getRec := httptest.NewRecorder()
	handler.TemplatesRootSettingsHandler(getRec, getReq)
	var getResp TemplatesRootResponse
	if err := json.NewDecoder(getRec.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if getResp.TemplatesRoot != customRoot || getResp.EffectiveTemplatesRoot != customRoot {
		t.Fatalf("unexpected GET response: %+v", getResp)
	}

	// Clearing falls back to env/default.
	envRoot := t.TempDir()
	t.Setenv("ORI_TEMPLATES_DIR", envRoot)
	clearBody, _ := json.Marshal(map[string]string{"templates_root": ""})
	clearReq := httptest.NewRequest(http.MethodPost, "/api/settings/templates-root", bytes.NewReader(clearBody))
	clearRec := httptest.NewRecorder()
	handler.TemplatesRootSettingsHandler(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear failed: %d %s", clearRec.Code, clearRec.Body.String())
	}
	var clearResp struct {
		EffectiveTemplatesRoot string `json:"effective_templates_root"`
		Source                 string `json:"source"`
	}
	if err := json.NewDecoder(clearRec.Body).Decode(&clearResp); err != nil {
		t.Fatal(err)
	}
	if clearResp.Source != "environment" || clearResp.EffectiveTemplatesRoot != envRoot {
		t.Fatalf("unexpected clear response: %+v", clearResp)
	}
}
