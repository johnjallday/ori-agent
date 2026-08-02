package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

// Task 6.13 / FR-157, FR-162: every state the Workshop can be in has to be
// reachable and describable, and none of them may print a credential.

func TestWorkshopConfigRedactsSecretBearingKeys(t *testing.T) {
	ws := newToolboxTestWorkspace()
	ws.SkillBindings = []SkillBinding{{
		ID:        "bind-notes",
		SkillName: "notes",
		Enabled:   true,
		Config: map[string]any{
			"endpoint":      "https://notes.example.com",
			"api_key":       "sk-live-must-not-appear",
			"apiKey":        "sk-camel-must-not-appear",
			"AUTH_TOKEN":    "tok-must-not-appear",
			"max_results":   25,
			"clientSecret":  "cs-must-not-appear",
			"nested_config": map[string]any{"password": "pw-must-not-appear", "retries": 3},
		},
	}}

	instance := ws.AgentInstances[0]
	inventory := BuildWorkshopInventory(ws, &instance, nil, nil, nil, 4, false)

	encoded, err := json.Marshal(inventory)
	if err != nil {
		t.Fatalf("marshal inventory: %v", err)
	}
	// Serialize the whole payload and search it. Asserting field-by-field would
	// pass while a secret leaked through some other field added later.
	for _, secret := range []string{
		"sk-live-must-not-appear",
		"sk-camel-must-not-appear",
		"tok-must-not-appear",
		"cs-must-not-appear",
		"pw-must-not-appear",
	} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("workshop inventory leaked a secret value: %s", secret)
		}
	}

	// The non-secret settings still have to be visible, or redaction has just
	// made the surface useless.
	for _, kept := range []string{"https://notes.example.com", "max_results"} {
		if !strings.Contains(string(encoded), kept) {
			t.Fatalf("workshop inventory dropped a non-secret setting: %s", kept)
		}
	}
	// The KEY stays. Knowing a binding is configured with an api_key is the
	// legible half; the value is the part that must not travel.
	if !strings.Contains(string(encoded), "api_key") {
		t.Fatalf("redaction removed the key name as well as the value")
	}
	if !strings.Contains(string(encoded), redactedConfigValue) {
		t.Fatalf("redacted values are not marked as hidden")
	}
}

func TestRedactWorkshopConfigCopiesRatherThanAliases(t *testing.T) {
	original := map[string]any{"endpoint": "https://example.com"}
	safe := redactWorkshopConfig(original)
	safe["endpoint"] = "mutated"

	if original["endpoint"] != "https://example.com" {
		t.Fatalf("redactWorkshopConfig aliased the binding's live config map")
	}
	if redactWorkshopConfig(nil) != nil {
		t.Fatalf("an empty config should stay empty rather than becoming {}")
	}
}

func TestIsSecretConfigKeyMatchesCommonShapes(t *testing.T) {
	secret := []string{
		"api_key", "apiKey", "APIKEY", "token", "AUTH_TOKEN", "password",
		"client_secret", "privateKey", "access_key_id", "session_cookie",
		"webhook_signature", " Bearer ",
	}
	for _, key := range secret {
		if !isSecretConfigKey(key) {
			t.Errorf("expected %q to be treated as secret-bearing", key)
		}
	}

	ordinary := []string{"endpoint", "max_results", "model", "timeout_seconds", "region", ""}
	for _, key := range ordinary {
		if isSecretConfigKey(key) {
			t.Errorf("expected %q to be treated as an ordinary setting", key)
		}
	}
}

// Each readiness state must be a distinct, non-empty sentence. The UI prints
// this string verbatim, so an empty or duplicated state would leave a user
// staring at a preview that will not submit and will not say why (FR-162).
func TestReadinessStatesAreDistinctAndHumanReadable(t *testing.T) {
	states := []string{
		ReadinessReady,
		ReadinessNeedsConnection,
		ReadinessNeedsApproval,
		ReadinessMissingCapability,
		ReadinessToolboxFull,
		ReadinessNeedsRepair,
		ReadinessArchived,
	}

	seen := make(map[string]bool, len(states))
	for _, state := range states {
		if strings.TrimSpace(state) == "" {
			t.Fatalf("a readiness state is empty")
		}
		if seen[state] {
			t.Fatalf("readiness state %q is duplicated — two conditions would be indistinguishable", state)
		}
		seen[state] = true
		if state != ReadinessReady && strings.EqualFold(state, "ready") {
			t.Fatalf("a non-ready state reads as ready: %q", state)
		}
	}
}
