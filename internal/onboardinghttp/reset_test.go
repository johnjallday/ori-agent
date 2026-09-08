package onboardinghttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/onboarding"
)

func TestResetOnboardingReturnsVerifiedReplayStateAndPreservedIdentity(t *testing.T) {
	manager := onboarding.NewManager(filepath.Join(t.TempDir(), "app_state.json"))
	if err := manager.SetNames("Jules", "Ari"); err != nil {
		t.Fatalf("SetNames: %v", err)
	}
	if err := manager.SetTimezone("America/New_York"); err != nil {
		t.Fatalf("SetTimezone: %v", err)
	}
	if err := manager.CompleteStep("profile"); err != nil {
		t.Fatalf("CompleteStep: %v", err)
	}
	if err := manager.CompleteOnboarding(); err != nil {
		t.Fatalf("CompleteOnboarding: %v", err)
	}

	recorder := httptest.NewRecorder()
	NewHandler(manager).Reset(recorder, httptest.NewRequest(http.MethodPost, "/api/onboarding/reset", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response StatusResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.NeedsOnboarding || response.Completed || response.Skipped || response.CurrentStep != 0 || len(response.StepsCompleted) != 0 || len(response.StepsSkipped) != 0 {
		t.Fatalf("response does not verify canonical replay state: %#v", response)
	}
	if response.UserName != "Jules" || response.AssistantName != "Ari" || response.Timezone != "America/New_York" {
		t.Fatalf("response does not verify preserved identity: %#v", response)
	}
}
