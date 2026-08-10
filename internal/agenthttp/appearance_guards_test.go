package agenthttp

import (
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

// Appearance is a shared-definition property, so it has to inherit the guards
// that protect every other shared field. The risk this covers is specific: a
// new mutation surface that skips them becomes the one path through which a
// concurrent edit can be silently clobbered (PRD FR-16/FR-42/FR-43).

func TestConcurrentAppearanceEditsRejectTheStaleOne(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "contested")
	entry := assignableCharacter(t)

	// Two clients load the same agent and hold the same token.
	first := agentVersion(t, ts, "contested")
	second := first

	// The first write wins and invalidates the token.
	rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=contested", map[string]any{
		"expected_version": first,
		"appearance":       map[string]any{"character": map[string]any{"catalog_id": string(entry.ID)}},
	})
	assertStatus(t, rr, http.StatusOK)

	// The second write is rejected rather than overwriting it.
	rr = ts.doRequest(t, http.MethodPatch, "/api/agents?name=contested", map[string]any{
		"expected_version": second,
		"appearance":       map[string]any{"generated": map[string]any{"color": "#112233"}},
	})
	assertStatus(t, rr, http.StatusConflict)

	ag, _ := ts.store.GetAgent("contested")
	if ag.Appearance.Mode != types.AppearanceModeCharacter {
		t.Errorf("the winning edit was overwritten: mode = %q", ag.Appearance.Mode)
	}
	if ag.Appearance.GeneratedColor() != "" {
		t.Errorf("the rejected edit was applied anyway: colour = %q", ag.Appearance.GeneratedColor())
	}
}

func TestEveryAppearanceMutationShapeHonoursTheVersionToken(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	entry := assignableCharacter(t)

	// One case per mutation shape, because the guard has to be on all of them —
	// a single unguarded shape is a complete bypass.
	shapes := map[string]map[string]any{
		"mode":      {"mode": "generated"},
		"colour":    {"generated": map[string]any{"color": "#112233"}},
		"reset":     {"generated": map[string]any{"color": nil}},
		"character": {"character": map[string]any{"catalog_id": string(entry.ID)}},
		"clear":     {"character": nil},
	}
	for name, patch := range shapes {
		t.Run(name, func(t *testing.T) {
			agentName := "guarded-" + name
			createPlainAgent(t, ts, agentName)

			rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name="+agentName, map[string]any{
				"expected_version": "a-token-from-another-era",
				"appearance":       patch,
			})
			assertStatus(t, rr, http.StatusConflict)

			ag, _ := ts.store.GetAgent(agentName)
			if ag.Appearance.Mode != types.AppearanceModeGenerated ||
				ag.Appearance.GeneratedColor() != "" ||
				ag.Appearance.CharacterCatalogID() != "" {
				t.Fatalf("a stale request mutated the record: %+v", ag.Appearance)
			}
		})
	}
}

func TestTheVersionTokenTracksInactiveSourceStateToo(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "inactive-tracked")

	before := agentVersion(t, ts, "inactive-tracked")

	// Save an upload without activating it — the kind of change that renders
	// identically but is genuinely a different definition. An edit based on the
	// old token must not be allowed to overwrite it (FR-16).
	ag, _ := ts.store.GetAgent("inactive-tracked")
	ag.Appearance.SetUpload("atlas.webp")
	ag.Appearance.Mode = types.AppearanceModeGenerated
	if err := ts.store.SetAgent("inactive-tracked", ag); err != nil {
		t.Fatalf("seed: %v", err)
	}

	after := agentVersion(t, ts, "inactive-tracked")
	if before == after {
		t.Fatal("a retained inactive upload must change the version token")
	}
}

func TestAppearanceOnlyChangesDoNotDisturbTheRestOfTheToken(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "stable")

	ag, _ := ts.store.GetAgent("stable")
	prompt := ag.Settings.SystemPrompt
	model := ag.Settings.Model

	rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=stable", map[string]any{
		"appearance": map[string]any{"generated": map[string]any{"color": "#112233"}},
	})
	assertStatus(t, rr, http.StatusOK)

	// The token changed, but nothing it hashes besides appearance did.
	after, _ := ts.store.GetAgent("stable")
	if after.Settings.SystemPrompt != prompt || after.Settings.Model != model {
		t.Fatalf("an appearance edit changed the configuration: %+v", after.Settings)
	}
}

func agentVersion(t *testing.T, ts *TestServer, name string) string {
	t.Helper()
	rr := ts.doRequest(t, http.MethodGet, "/api/agents/"+name+"/detail", nil)
	assertStatus(t, rr, http.StatusOK)
	var detail map[string]any
	decodeResponse(t, rr, &detail)
	version, _ := detail["version"].(string)
	if version == "" {
		t.Fatalf("no version token for %q", name)
	}
	return version
}
