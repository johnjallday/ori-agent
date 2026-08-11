package agenthttp

import (
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/types"
)

// assignableCharacter returns a real working catalog entry, so these tests
// exercise the same assignability rules the handler does rather than a fixture
// that could drift from the shipped catalog.
func assignableCharacter(t *testing.T) charactercatalog.Character {
	t.Helper()
	cat, err := charactercatalog.Load()
	if err != nil {
		t.Fatalf("catalog unavailable: %v", err)
	}
	working := cat.Working()
	if len(working) == 0 {
		t.Fatal("catalog has no assignable working characters")
	}
	return working[0]
}

func createPlainAgent(t *testing.T, ts *TestServer, name string) {
	t.Helper()
	rr := ts.doRequest(t, http.MethodPost, "/api/agents", map[string]any{
		"name":  name,
		"type":  "tool-calling",
		"model": "gpt-4o-mini",
	})
	assertStatus(t, rr, http.StatusOK)
}

func appearanceOf(t *testing.T, ts *TestServer, name string) map[string]any {
	t.Helper()
	rr := ts.doRequest(t, http.MethodGet, "/api/agents/"+name+"/detail", nil)
	assertStatus(t, rr, http.StatusOK)
	var detail map[string]any
	decodeResponse(t, rr, &detail)
	appearance, ok := detail["appearance"].(map[string]any)
	if !ok {
		t.Fatalf("detail response for %q carries no appearance: %#v", name, detail)
	}
	return appearance
}

func TestCreateDefaultsToGeneratedAppearance(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	createPlainAgent(t, ts, "plain")

	appearance := appearanceOf(t, ts, "plain")
	if appearance["mode"] != "generated" {
		t.Fatalf("an omitted appearance must default to generated, got %v", appearance["mode"])
	}
	// The generated object ships even when empty, so a client never has to guess
	// whether a missing key means "no override" or "unsupported build" (FR-2).
	if _, ok := appearance["generated"].(map[string]any); !ok {
		t.Fatalf("generated must always be present, got %#v", appearance["generated"])
	}
	ag, ok := ts.store.GetAgent("plain")
	if !ok || ag == nil || ag.Appearance == nil {
		t.Fatal("a created agent must have a non-nil stored appearance")
	}
}

func TestCreateAndPatchRejectRetiredRequestFields(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "strict")

	// Silently ignoring these is the worse failure: a caller would appear to
	// succeed while changing nothing (FR-51).
	for _, field := range []string{"avatar_color", "avatar_image", "character", "display_mode"} {
		t.Run("create/"+field, func(t *testing.T) {
			rr := ts.doRequest(t, http.MethodPost, "/api/agents", map[string]any{
				"name":  "rejected-" + field,
				"model": "gpt-4o-mini",
				field:   "whatever",
			})
			assertStatus(t, rr, http.StatusBadRequest)
			if _, exists := ts.store.GetAgent("rejected-" + field); exists {
				t.Error("a rejected create must not leave an agent behind")
			}
		})
		t.Run("patch/"+field, func(t *testing.T) {
			rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=strict", map[string]any{
				field: "whatever",
			})
			assertStatus(t, rr, http.StatusBadRequest)
		})
	}
}

func TestPatchRejectsServerManagedAppearanceValues(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "managed")
	entry := assignableCharacter(t)

	cases := []struct {
		name string
		body map[string]any
	}{
		{
			// Allowing JSON to name a file would let a caller point an agent at
			// any filename in the avatar directory (FR-8/FR-55).
			name: "uploaded image",
			body: map[string]any{"uploaded": map[string]any{"image": "someone-else.png"}},
		},
		{
			name: "catalog version",
			body: map[string]any{"character": map[string]any{"catalog_id": string(entry.ID), "catalog_version": 99}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=managed", map[string]any{"appearance": tc.body})
			assertStatus(t, rr, http.StatusBadRequest)

			ag, _ := ts.store.GetAgent("managed")
			if ag.Appearance.UploadedImage() != "" || ag.Appearance.CharacterCatalogID() != "" {
				t.Fatalf("a rejected request must not mutate the stored appearance: %+v", ag.Appearance)
			}
		})
	}
}

func TestPatchAssignsTheCatalogVersionServerSide(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "chooser")
	entry := assignableCharacter(t)

	rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=chooser", map[string]any{
		"appearance": map[string]any{"character": map[string]any{"catalog_id": string(entry.ID)}},
	})
	assertStatus(t, rr, http.StatusOK)

	appearance := appearanceOf(t, ts, "chooser")
	if appearance["mode"] != "character" {
		t.Fatalf("choosing a character must activate it, got %v", appearance["mode"])
	}
	character, ok := appearance["character"].(map[string]any)
	if !ok {
		t.Fatalf("expected a character object, got %#v", appearance["character"])
	}
	if character["catalog_id"] != string(entry.ID) {
		t.Errorf("catalog_id = %v, want %v", character["catalog_id"], entry.ID)
	}
	if got, _ := character["catalog_version"].(float64); int(got) != entry.EntryVersion {
		t.Errorf("catalog_version = %v, want the live catalog value %d", character["catalog_version"], entry.EntryVersion)
	}
}

func TestActivatingASourceWithoutItsAssetFailsAtomically(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "empty")

	for _, mode := range []string{"uploaded", "character"} {
		t.Run(mode, func(t *testing.T) {
			rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=empty", map[string]any{
				"appearance": map[string]any{"mode": mode},
			})
			assertStatus(t, rr, http.StatusBadRequest)

			ag, _ := ts.store.GetAgent("empty")
			if ag.Appearance.Mode != types.AppearanceModeGenerated {
				t.Fatalf("a rejected activation must leave the mode alone, got %q", ag.Appearance.Mode)
			}
		})
	}
}

func TestUnknownModeAndBadColourAreRejected(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "invalid")

	bodies := []map[string]any{
		{"mode": "hologram"},
		// "fallback" was a saved mode in the old schema; it must not be one now.
		{"mode": "fallback"},
		{"generated": map[string]any{"color": "chartreuse"}},
		{"generated": map[string]any{"color": "#12345"}},
	}
	for _, body := range bodies {
		rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=invalid", map[string]any{"appearance": body})
		assertStatus(t, rr, http.StatusBadRequest)
	}

	ag, _ := ts.store.GetAgent("invalid")
	if ag.Appearance.Mode != types.AppearanceModeGenerated || ag.Appearance.GeneratedColor() != "" {
		t.Fatalf("rejected requests must not mutate anything: %+v", ag.Appearance)
	}
}

func TestReservedGuideCharacterCannotBeAssigned(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "aspirant")

	cat, err := charactercatalog.Load()
	if err != nil {
		t.Fatalf("catalog unavailable: %v", err)
	}
	rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=aspirant", map[string]any{
		"appearance": map[string]any{"character": map[string]any{"catalog_id": string(cat.ReservedGuideID)}},
	})
	assertStatus(t, rr, http.StatusBadRequest)

	ag, _ := ts.store.GetAgent("aspirant")
	if ag.Appearance.CharacterCatalogID() != "" {
		t.Fatalf("the reserved guide identity must never reach an agent, got %q", ag.Appearance.CharacterCatalogID())
	}
}

func TestPartialUpdatePreservesOmittedSources(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "partial")
	entry := assignableCharacter(t)

	// Build up all three sources: a colour, a character, and an upload the
	// server records for us.
	rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=partial", map[string]any{
		"appearance": map[string]any{
			"generated": map[string]any{"color": "#6D5DFC"},
			"character": map[string]any{"catalog_id": string(entry.ID)},
		},
	})
	assertStatus(t, rr, http.StatusOK)

	ag, _ := ts.store.GetAgent("partial")
	ag.Appearance.SetUpload("atlas.webp")
	if err := ts.store.SetAgent("partial", ag); err != nil {
		t.Fatalf("seed upload: %v", err)
	}

	// A mode-only change must not disturb the other two sources (FR-53).
	rr = ts.doRequest(t, http.MethodPatch, "/api/agents?name=partial", map[string]any{
		"appearance": map[string]any{"mode": "character"},
	})
	assertStatus(t, rr, http.StatusOK)

	appearance := appearanceOf(t, ts, "partial")
	if appearance["mode"] != "character" {
		t.Errorf("mode = %v, want character", appearance["mode"])
	}
	generated, _ := appearance["generated"].(map[string]any)
	if generated["color"] != "#6d5dfc" {
		t.Errorf("the colour must survive and be normalized, got %v", generated["color"])
	}
	uploaded, _ := appearance["uploaded"].(map[string]any)
	if uploaded == nil || uploaded["image"] != "atlas.webp" {
		t.Errorf("the inactive upload must survive, got %#v", appearance["uploaded"])
	}
}

func TestExplicitNullClearsOnlyItsOwnSource(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "clearer")
	entry := assignableCharacter(t)

	rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=clearer", map[string]any{
		"appearance": map[string]any{
			"generated": map[string]any{"color": "#6d5dfc"},
			"character": map[string]any{"catalog_id": string(entry.ID)},
		},
	})
	assertStatus(t, rr, http.StatusOK)

	// Clearing the colour must not disturb the active character (FR-54).
	rr = ts.doRequest(t, http.MethodPatch, "/api/agents?name=clearer", map[string]any{
		"appearance": map[string]any{"generated": map[string]any{"color": nil}},
	})
	assertStatus(t, rr, http.StatusOK)

	appearance := appearanceOf(t, ts, "clearer")
	generated, _ := appearance["generated"].(map[string]any)
	if _, present := generated["color"]; present {
		t.Errorf("the colour override must be gone, got %#v", generated)
	}
	if appearance["mode"] != "character" {
		t.Errorf("clearing the colour must not change the active mode, got %v", appearance["mode"])
	}

	// Clearing the active character returns to generated (FR-33).
	rr = ts.doRequest(t, http.MethodPatch, "/api/agents?name=clearer", map[string]any{
		"appearance": map[string]any{"character": nil},
	})
	assertStatus(t, rr, http.StatusOK)

	appearance = appearanceOf(t, ts, "clearer")
	if appearance["mode"] != "generated" {
		t.Errorf("removing the active character must fall back to generated, got %v", appearance["mode"])
	}
	if _, present := appearance["character"]; present {
		t.Errorf("the character selection must be gone, got %#v", appearance["character"])
	}
}

func TestAppearanceChangeLeavesTheRestOfTheDefinitionUntouched(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	rr := ts.doRequest(t, http.MethodPost, "/api/agents", map[string]any{
		"name":          "isolated",
		"type":          "tool-calling",
		"model":         "gpt-4o-mini",
		"system_prompt": "You are precise.",
		"role":          "analyzer",
		"description":   "unchanged",
		"tags":          []string{"a", "b"},
	})
	assertStatus(t, rr, http.StatusOK)

	before, _ := ts.store.GetAgent("isolated")
	prompt, model, role, agentType := before.Settings.SystemPrompt, before.Settings.Model, before.Role, before.Type
	description := before.Metadata.Description
	tags := append([]string{}, before.Metadata.Tags...)
	entry := assignableCharacter(t)

	rr = ts.doRequest(t, http.MethodPatch, "/api/agents?name=isolated", map[string]any{
		"appearance": map[string]any{"character": map[string]any{"catalog_id": string(entry.ID)}},
	})
	assertStatus(t, rr, http.StatusOK)

	after, _ := ts.store.GetAgent("isolated")
	// Appearance is presentation only. If any of these can move, the feature has
	// reintroduced exactly the promise it exists to remove (FR-17).
	if after.Settings.SystemPrompt != prompt {
		t.Errorf("system prompt changed: %q -> %q", prompt, after.Settings.SystemPrompt)
	}
	if after.Settings.Model != model {
		t.Errorf("model changed: %q -> %q", model, after.Settings.Model)
	}
	if after.Role != role {
		t.Errorf("role changed: %q -> %q", role, after.Role)
	}
	if after.Type != agentType {
		t.Errorf("type changed: %q -> %q", agentType, after.Type)
	}
	if after.Metadata.Description != description {
		t.Errorf("description changed: %q -> %q", description, after.Metadata.Description)
	}
	if len(after.Metadata.Tags) != len(tags) {
		t.Errorf("tags changed: %v -> %v", tags, after.Metadata.Tags)
	}
}

func TestAppearanceParticipatesInTheStaleEditGuard(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "concurrent")

	rr := ts.doRequest(t, http.MethodGet, "/api/agents/concurrent/detail", nil)
	assertStatus(t, rr, http.StatusOK)
	var detail map[string]any
	decodeResponse(t, rr, &detail)
	staleVersion, _ := detail["version"].(string)
	if staleVersion == "" {
		t.Fatal("detail response must carry a version token")
	}

	// Someone else changes only the appearance.
	rr = ts.doRequest(t, http.MethodPatch, "/api/agents?name=concurrent", map[string]any{
		"appearance": map[string]any{"generated": map[string]any{"color": "#112233"}},
	})
	assertStatus(t, rr, http.StatusOK)

	// The first client's token must now be stale — an appearance-only change is
	// still a shared-definition change (FR-16).
	rr = ts.doRequest(t, http.MethodPatch, "/api/agents?name=concurrent", map[string]any{
		"expected_version": staleVersion,
		"description":      "late write",
	})
	assertStatus(t, rr, http.StatusConflict)
}

func TestListAndDetailAgreeAndOmitRetiredFields(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "consistent")
	entry := assignableCharacter(t)

	rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=consistent", map[string]any{
		"appearance": map[string]any{"character": map[string]any{"catalog_id": string(entry.ID)}},
	})
	assertStatus(t, rr, http.StatusOK)

	rr = ts.doRequest(t, http.MethodGet, "/api/agents/dashboard/list", nil)
	assertStatus(t, rr, http.StatusOK)
	var list struct {
		Agents []map[string]any `json:"agents"`
	}
	decodeResponse(t, rr, &list)

	var row map[string]any
	for _, item := range list.Agents {
		if item["name"] == "consistent" {
			row = item
			break
		}
	}
	if row == nil {
		t.Fatal("agent missing from dashboard list")
	}
	listAppearance, ok := row["appearance"].(map[string]any)
	if !ok {
		t.Fatalf("list rows must carry appearance, got %#v", row["appearance"])
	}
	detailAppearance := appearanceOf(t, ts, "consistent")
	if listAppearance["mode"] != detailAppearance["mode"] {
		t.Errorf("list and detail disagree: %v vs %v", listAppearance["mode"], detailAppearance["mode"])
	}

	if md, ok := row["metadata"].(map[string]any); ok {
		for _, retired := range []string{"avatar_color", "avatar_image", "character"} {
			if _, present := md[retired]; present {
				t.Errorf("list metadata still exposes %q", retired)
			}
		}
	}
}

func TestBuiltInAgentsResolveToGeneratedAppearance(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Read-only agents render through the same resolver; what differs is that
	// their editors offer no controls the server would reject (FR-44).
	rr := ts.doRequest(t, http.MethodGet, "/api/agents", nil)
	assertStatus(t, rr, http.StatusOK)
	var list struct {
		Agents []map[string]any `json:"agents"`
	}
	decodeResponse(t, rr, &list)
	for _, item := range list.Agents {
		appearance, ok := item["appearance"].(map[string]any)
		if !ok {
			t.Fatalf("every listed agent must carry appearance, %v does not", item["name"])
		}
		if _, ok := appearance["generated"].(map[string]any); !ok {
			t.Errorf("%v is missing its generated object", item["name"])
		}
	}
}
