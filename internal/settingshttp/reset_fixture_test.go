package settingshttp

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Exercise the real handler's compatibility refusal, not a mock success.
// The unsafe legacy live-deletion implementation is no longer in the product.
func TestResetFixtureSettingsRequestUsesOnlyOwnedInstallation(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	p := f.Paths()
	mgr := onboarding.NewManager(filepath.Join(p.DataDir, "app_state.json"))
	agents, err := store.NewFileStore(filepath.Join(p.DataDir, "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	h := NewResetHandler(mgr, agents, p.DataDir)
	s := f.StartHTTP(t, http.HandlerFunc(h.HandleReset))
	settingsPath := filepath.Join(p.DataDir, "settings.json")

	rejected, err := s.Do(t.Context(), http.MethodPost, "/api/reset", strings.NewReader(`{"settings":true,"confirmation":"no"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := rejected.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if rejected.StatusCode != http.StatusBadRequest {
		t.Fatalf("unconfirmed reset status = %d", rejected.StatusCode)
	}
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatal("unconfirmed reset removed fixture settings:", err)
	}

	accepted, err := s.Do(t.Context(), http.MethodPost, "/api/reset", strings.NewReader(`{"settings":true,"confirmation":"RESET"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := accepted.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	var result ResetResponse
	if err := json.NewDecoder(accepted.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if accepted.StatusCode != http.StatusConflict || result.Success || len(result.ResetItems) != 0 || result.Code != "preview_required" {
		t.Fatalf("unexpected reset outcome: status=%d result=%+v", accepted.StatusCode, result)
	}
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatal("legacy confirmation deleted live settings without preview:", err)
	}
	for _, name := range []string{"agents.json", "app_state.json", "sessions.db", "session_files/fixture-upload.txt"} {
		if _, err := os.Stat(filepath.Join(p.DataDir, name)); err != nil {
			t.Errorf("settings reset affected unselected %s: %v", name, err)
		}
	}
	f.AssertPreserved(t)
}
