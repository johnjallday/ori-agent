package server

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/progressionhttp"
	"github.com/johnjallday/ori-agent/internal/testutil/resetfixture"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/vault"
)

func TestReplaySetupHTTPChangesOnlyOnboardingSteps(t *testing.T) {
	fixture := resetfixture.NewSeeded(t)
	paths := fixture.Paths()
	manager := onboarding.NewManager(filepath.Join(paths.DataDir, "app_state.json"))
	at := time.Now().UTC().Truncate(time.Second)
	if err := manager.SetProgression(types.ProgressionState{
		CompletedQuests: map[string]time.Time{"t1-first-message": at},
		SkippedQuests:   map[string]time.Time{"t2-build-hq": at},
		Dismissed:       true,
	}); err != nil {
		t.Fatalf("seed progression: %v", err)
	}
	if err := manager.SetAssistantProgress(&types.AssistantProgress{Level: 4, Experience: 120, Rank: "captain"}); err != nil {
		t.Fatalf("seed assistant progression: %v", err)
	}
	filesBefore := hashGuidanceFiles(t, paths)
	secretsBefore := hashGuidanceSecrets(t, fixture.Secrets())

	recorder := httptest.NewRecorder()
	onboardinghttp.NewHandler(manager).Reset(recorder, httptest.NewRequest(http.MethodPost, "/api/onboarding/reset", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("Replay Setup status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response onboardinghttp.StatusResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	if !response.NeedsOnboarding || response.Completed || len(response.StepsCompleted) != 0 {
		t.Fatalf("Replay Setup response was not verified: %#v", response)
	}

	reloaded := onboarding.NewManager(manager.PersistencePath())
	userName, assistantName := reloaded.GetNames()
	if userName != "Fixture User" || assistantName != resetfixture.AgentName {
		t.Fatalf("Replay Setup changed identity: user=%q assistant=%q", userName, assistantName)
	}
	progress := reloaded.GetProgression()
	if len(progress.CompletedQuests) != 1 || len(progress.SkippedQuests) != 1 || !progress.Dismissed {
		t.Fatalf("Replay Setup changed Getting Started: %#v", progress)
	}
	if got := reloaded.GetAssistantProgress(); got.Level != 4 || got.Experience != 120 || got.Rank != "captain" {
		t.Fatalf("Replay Setup changed assistant progression: %#v", got)
	}
	assertGuidanceFilesAndSecrets(t, fixture.Secrets(), filesBefore, secretsBefore)
	fixture.AssertPreserved(t)
}

func TestResetGettingStartedHTTPChangesOnlyQuestState(t *testing.T) {
	fixture := resetfixture.NewSeeded(t)
	paths := fixture.Paths()
	manager := onboarding.NewManager(filepath.Join(paths.DataDir, "app_state.json"))
	if err := manager.CompleteStep("retained-setup-step"); err != nil {
		t.Fatalf("seed setup step: %v", err)
	}
	if err := manager.SetAssistantProgress(&types.AssistantProgress{Level: 5, Experience: 240, Rank: "captain"}); err != nil {
		t.Fatalf("seed assistant progression: %v", err)
	}
	engine := progression.New(manager)
	if !engine.Complete("t1-first-message") {
		t.Fatal("seed quest completion failed")
	}
	if err := engine.SetDismissed(true); err != nil {
		t.Fatalf("seed dismissal: %v", err)
	}
	setupBefore := manager.GetState()
	filesBefore := hashGuidanceFiles(t, paths)
	secretsBefore := hashGuidanceSecrets(t, fixture.Secrets())

	recorder := httptest.NewRecorder()
	progressionhttp.NewHandler(engine).Reset(recorder, httptest.NewRequest(http.MethodPost, "/api/progression/reset", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("Reset Getting Started status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response progression.Status
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode progression response: %v", err)
	}
	if response.CompletedCount != 0 || response.ResolvedCount != 0 || response.Dismissed {
		t.Fatalf("Getting Started response was not verified: %#v", response)
	}

	reloaded := onboarding.NewManager(manager.PersistencePath())
	if setupAfter := reloaded.GetState(); setupAfter.Completed != setupBefore.Completed || setupAfter.CurrentStep != setupBefore.CurrentStep || !slices.Equal(setupAfter.StepsCompleted, setupBefore.StepsCompleted) {
		t.Fatalf("Getting Started reset changed setup: before=%#v after=%#v", setupBefore, setupAfter)
	}
	userName, assistantName := reloaded.GetNames()
	if userName != "Fixture User" || assistantName != resetfixture.AgentName {
		t.Fatalf("Getting Started reset changed identity: user=%q assistant=%q", userName, assistantName)
	}
	if got := reloaded.GetAssistantProgress(); got.Level != 5 || got.Experience != 240 || got.Rank != "captain" {
		t.Fatalf("Getting Started reset changed assistant progression: %#v", got)
	}
	assertGuidanceFilesAndSecrets(t, fixture.Secrets(), filesBefore, secretsBefore)
	fixture.AssertPreserved(t)
}

func hashGuidanceFiles(t *testing.T, paths resetfixture.Paths) map[string][sha256.Size]byte {
	t.Helper()
	result := make(map[string][sha256.Size]byte)
	for _, path := range []string{
		filepath.Join(paths.DataDir, "settings.json"),
		filepath.Join(paths.DataDir, "agents.json"),
		filepath.Join(paths.DataDir, "sessions.db"),
	} {
		data, err := os.ReadFile(path) // #nosec G304 -- every path is rooted in the test-owned fixture.
		if err != nil {
			t.Fatalf("read fixture file %s: %v", filepath.Base(path), err)
		}
		result[path] = sha256.Sum256(data)
	}
	return result
}

func hashGuidanceSecrets(t *testing.T, store vault.SecretStore) map[vault.SecretKey][sha256.Size]byte {
	t.Helper()
	result := make(map[vault.SecretKey][sha256.Size]byte)
	for _, key := range []vault.SecretKey{
		vault.SecretKeyOpenAIAPIKey,
		vault.SecretKeyAnthropicAPIKey,
		vault.SecretKeyGeminiAPIKey,
		vault.SecretKeyBraveAPIKey,
		vault.SecretKeyVaultDEK,
	} {
		value, err := store.Get(key)
		if err != nil {
			t.Fatalf("read fixture secret slot %s: %v", key, err)
		}
		result[key] = sha256.Sum256([]byte(value))
	}
	return result
}

func assertGuidanceFilesAndSecrets(t *testing.T, store vault.SecretStore, files map[string][sha256.Size]byte, secrets map[vault.SecretKey][sha256.Size]byte) {
	t.Helper()
	for path, expected := range files {
		data, err := os.ReadFile(path) // #nosec G304 -- every path is rooted in the test-owned fixture.
		actual := sha256.Sum256(data)
		if err != nil || actual != expected {
			t.Fatalf("guidance reset changed fixture file %s", filepath.Base(path))
		}
	}
	for key, expected := range secrets {
		value, err := store.Get(key)
		actual := sha256.Sum256([]byte(value))
		if err != nil || actual != expected {
			t.Fatalf("guidance reset changed fixture secret slot %s", key)
		}
	}
}
