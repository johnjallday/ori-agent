package settingshttp

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
)

// The legacy cache-clear API is gone, not hidden behind a test-only copy of its
// implementation. A failing live owner must not be called at all by admission.
// Once pre-start category apply exists, also test its real per-owner failures;
// these refusals do not satisfy that separate mixed-outcome delivery gate.
func TestResetOutcomeRejectsLegacyBeforeTouchingFailingCache(t *testing.T) {
	f := resetfixture.NewSeeded(t)
	agents := &fakeAgentStore{clearErr: errors.New("fixture cache failure")}
	h := NewResetHandler(nil, agents, f.Paths().DataDir)
	status, response := postReset(t, h, ResetRequest{Agents: true, Confirmation: "RESET"})
	if status != http.StatusConflict || response.Success || agents.clearCalls != 0 {
		t.Fatal("legacy request invoked a failing live owner")
	}
	if _, err := os.Stat(filepath.Join(f.Paths().DataDir, "agents.json")); err != nil {
		t.Fatal("failed/rejected request removed profiles:", err)
	}
}

func TestResetOutcomeUnavailableHostCannotClaimCategoryCompletion(t *testing.T) {
	resetfixture.New(t)
	agents := &fakeAgentStore{}
	for _, dataDir := range []string{"", " "} {
		status, response := postReset(t, NewResetHandler(nil, agents, dataDir), ResetRequest{Settings: true, Agents: true, Confirmation: "RESET"})
		if status != http.StatusConflict || response.Success || len(response.ResetItems) != 0 || agents.clearCalls != 0 {
			t.Fatal("unavailable installation produced completed categories")
		}
	}
}

// The replay regression uses real admitted HTTP requests and durable status in
// reset_admission_test.go; mixed verified-result projection is in reset_result_test.go.
