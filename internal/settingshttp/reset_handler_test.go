package settingshttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

// Any call to ClearAgents is observable; admission must never clear a live cache.
type fakeAgentStore struct {
	clearCalls int
	clearErr   error
}

func (f *fakeAgentStore) ListAgents() []string                               { return nil }
func (f *fakeAgentStore) CreateAgent(string, *store.CreateAgentConfig) error { return nil }
func (f *fakeAgentStore) DeleteAgent(string) error                           { return nil }
func (f *fakeAgentStore) GetAgent(string) (*agent.Agent, bool)               { return nil, false }
func (f *fakeAgentStore) SetAgent(string, *agent.Agent) error                { return nil }
func (f *fakeAgentStore) UpdateAgent(string, func(*agent.Agent) error) error { return nil }
func (f *fakeAgentStore) Save() error                                        { return nil }
func (f *fakeAgentStore) ClearAgents() error                                 { f.clearCalls++; return f.clearErr }

func postReset(t *testing.T, h *ResetHandler, req ResetRequest) (int, ResetResponse) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/reset", bytes.NewReader(body))
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleReset(w, r)
	var response ResetResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return w.Code, response
}

func TestResetHandler_DataDir_ReturnsConstructedValue(t *testing.T) {
	path := t.TempDir()
	if NewResetHandler(nil, nil, path).DataDir() != path {
		t.Fatal("installation root changed")
	}
}

// Replace the former live-deletion characterization with the actual contract.
// Each old category and select-all must require review, retain setup/cache/data,
// and preserve outside/nested sentinels even if the exact RESET word is supplied.
func TestHandleResetLegacySelectionsRequirePreviewWithoutEffects(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	p := f.Paths()
	mgr := onboarding.NewManager(filepath.Join(p.DataDir, "app_state.json"))
	agents := &fakeAgentStore{}
	h := NewResetHandler(mgr, agents, p.DataDir)
	before := make(map[string][]byte)
	for _, name := range []string{"settings.json", "agents.json", "app_state.json", "sessions.db", "session_files/fixture-upload.txt"} {
		data, err := os.ReadFile(filepath.Join(p.DataDir, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = data
	}
	complete := mgr.IsOnboardingComplete()
	for _, req := range []ResetRequest{{Settings: true}, {Agents: true}, {Sessions: true}, {Onboarding: true}, {Settings: true, Agents: true, Sessions: true, Onboarding: true}} {
		req.Confirmation = "RESET"
		status, result := postReset(t, h, req)
		if status != http.StatusConflict || result.Code != "preview_required" || result.Success || len(result.ResetItems) != 0 {
			t.Fatal("legacy selection bypassed review:", result)
		}
	}
	for name, expected := range before {
		actual, err := os.ReadFile(filepath.Join(p.DataDir, name))
		if err != nil || !bytes.Equal(expected, actual) {
			t.Fatal("legacy request changed live data:", name, err)
		}
	}
	if agents.clearCalls != 0 || mgr.IsOnboardingComplete() != complete {
		t.Fatal("legacy request changed cached state")
	}
	f.AssertPreserved(t)
}

func TestHandleResetRejectsWithoutExactConfirmation(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	h := NewResetHandler(nil, nil, f.Paths().DataDir)
	for _, confirmation := range []string{"", "reset", "nope", " RESET", "RESET "} {
		status, response := postReset(t, h, ResetRequest{Settings: true, Confirmation: confirmation})
		if status != http.StatusBadRequest || response.Success {
			t.Fatal("confirmation bypassed")
		}
	}
	f.AssertPreserved(t)
}
