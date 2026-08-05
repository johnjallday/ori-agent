package agenthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func characterTestHandlers(t *testing.T, agentName string) (*Handler, store.Store) {
	t.Helper()
	st, err := store.NewFileStore(
		filepath.Join(t.TempDir(), "agents_index.json"),
		types.Settings{Model: "gpt-4o-mini", Temperature: 1.0},
	)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := st.CreateAgent(agentName, &store.CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return New(st), st
}

func patchAgent(t *testing.T, h *Handler, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/agents/"+name, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// firstWorkingID returns a real assignable catalog ID, so the tests exercise the
// actual shipped catalog rather than a fixture that could drift from it.
func firstWorkingID(t *testing.T) string {
	t.Helper()
	working := charactercatalog.MustLoad().Working()
	if len(working) == 0 {
		t.Fatal("catalog has no working characters")
	}
	return string(working[0].ID)
}

/* ---- the reserved guide identity ------------------------------------------ */

// The headline guarantee: a direct API call cannot hand Ori's identity to a
// working agent (FR-19/FR-71). This is the API-level counterpart to the
// catalog-level test in internal/charactercatalog.
func TestPatchRejectsTheReservedGuideCharacter(t *testing.T) {
	h, st := characterTestHandlers(t, "Solo")
	reserved := string(charactercatalog.MustLoad().ReservedGuideID)

	rec := patchAgent(t, h, "Solo", `{"character":{"catalog_id":"`+reserved+`","display_mode":"character"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "reserved") {
		t.Errorf("error should explain the identity is reserved, got: %s", rec.Body.String())
	}

	ag, _ := st.GetAgent("Solo")
	if ag.Metadata != nil && ag.Metadata.CharacterCatalogID() != "" {
		t.Fatalf("a rejected request must not persist anything, got %q", ag.Metadata.CharacterCatalogID())
	}
}

func TestPatchRejectsUnknownCharacter(t *testing.T) {
	h, st := characterTestHandlers(t, "Solo")
	for _, id := range []string{"does-not-exist", "../escape", "Sable", "ori"} {
		t.Run(id, func(t *testing.T) {
			rec := patchAgent(t, h, "Solo", `{"character":{"catalog_id":"`+id+`","display_mode":"character"}}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for %q, got %d", id, rec.Code)
			}
			ag, _ := st.GetAgent("Solo")
			if ag.Metadata != nil && ag.Metadata.CharacterCatalogID() != "" {
				t.Fatalf("rejected id %q was persisted", id)
			}
		})
	}
}

func TestPatchRejectsUnknownDisplayMode(t *testing.T) {
	h, _ := characterTestHandlers(t, "Solo")
	for _, mode := range []string{"hologram", "guide", "Character", ""} {
		rec := patchAgent(t, h, "Solo", `{"character":{"display_mode":"`+mode+`"}}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for display mode %q, got %d", mode, rec.Code)
		}
	}
}

func TestCreateRejectsTheReservedGuideCharacter(t *testing.T) {
	h, st := characterTestHandlers(t, "Existing")
	reserved := string(charactercatalog.MustLoad().ReservedGuideID)

	body := `{"name":"Newbie","character":{"catalog_id":"` + reserved + `","display_mode":"character"}}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/agents", strings.NewReader(body)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	// The agent must not exist at all: validation runs before creation, so a
	// bad character never leaves a half-configured agent behind.
	if _, ok := st.GetAgent("Newbie"); ok {
		t.Fatal("a rejected character choice still created the agent")
	}
}

/* ---- the happy path -------------------------------------------------------- */

func TestPatchPersistsACharacterChoice(t *testing.T) {
	h, st := characterTestHandlers(t, "Solo")
	id := firstWorkingID(t)

	rec := patchAgent(t, h, "Solo", `{"character":{"catalog_id":"`+id+`","display_mode":"character","voice_enabled":true}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	ag, _ := st.GetAgent("Solo")
	if got := ag.Metadata.CharacterCatalogID(); got != id {
		t.Fatalf("expected character %q, got %q", id, got)
	}
	if got := ag.Metadata.ResolveDisplayMode(); got != types.DisplayModeCharacter {
		t.Fatalf("expected character display mode, got %q", got)
	}
	if !ag.Metadata.IsCharacterVoiceEnabled() {
		t.Error("expected the tone layer to be enabled")
	}
	// The version is server-assigned from the catalog, never client-supplied.
	entry, _ := charactercatalog.MustLoad().Get(charactercatalog.CharacterID(id))
	if ag.Metadata.Character.CatalogVersion != entry.EntryVersion {
		t.Errorf("expected catalog version %d, got %d", entry.EntryVersion, ag.Metadata.Character.CatalogVersion)
	}
}

func TestCreatePersistsACharacterChoice(t *testing.T) {
	h, st := characterTestHandlers(t, "Existing")
	id := firstWorkingID(t)

	body := `{"name":"Newbie","character":{"catalog_id":"` + id + `","display_mode":"character"}}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/agents", strings.NewReader(body)))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("expected success, got %d: %s", rec.Code, rec.Body.String())
	}

	ag, ok := st.GetAgent("Newbie")
	if !ok {
		t.Fatal("agent was not created")
	}
	if got := ag.Metadata.CharacterCatalogID(); got != id {
		t.Fatalf("expected character %q to persist through creation, got %q", id, got)
	}
}

// "Skip for now" is a first-class path (FR-56): creating without a character
// must succeed and leave the agent on the deterministic fallback.
func TestCreateWithoutACharacterSucceeds(t *testing.T) {
	h, st := characterTestHandlers(t, "Existing")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/agents", strings.NewReader(`{"name":"Plain"}`)))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("expected success, got %d: %s", rec.Code, rec.Body.String())
	}

	ag, _ := st.GetAgent("Plain")
	if ag.Metadata != nil && ag.Metadata.Character != nil {
		t.Fatalf("skipping the picker should leave no character identity, got %+v", ag.Metadata.Character)
	}
	if got := ag.Metadata.ResolveDisplayMode(); got != types.DisplayModeFallback {
		t.Fatalf("expected fallback, got %q", got)
	}
}

/* ---- uploads are never destroyed ------------------------------------------- */

// The promise behind FR-64/FR-68: trying a curated character is reversible and
// costs the user nothing. The uploaded filename must survive the switch.
func TestSwitchingToACharacterPreservesTheUploadedAvatar(t *testing.T) {
	h, st := characterTestHandlers(t, "Solo")
	id := firstWorkingID(t)

	ag, _ := st.GetAgent("Solo")
	ag.Metadata = &types.AgentMetadata{AvatarImage: "solo.png"}
	if err := st.SetAgent("Solo", ag); err != nil {
		t.Fatalf("seed avatar: %v", err)
	}

	rec := patchAgent(t, h, "Solo", `{"character":{"catalog_id":"`+id+`","display_mode":"character"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	ag, _ = st.GetAgent("Solo")
	if ag.Metadata.AvatarImage != "solo.png" {
		t.Fatalf("uploaded avatar was lost: %q", ag.Metadata.AvatarImage)
	}
	if ag.Metadata.ResolveDisplayMode() != types.DisplayModeCharacter {
		t.Fatal("expected the character to be displayed")
	}

	// ...and switching back restores the upload without re-uploading.
	rec = patchAgent(t, h, "Solo", `{"character":{"display_mode":"uploaded"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("switch back failed: %d %s", rec.Code, rec.Body.String())
	}
	ag, _ = st.GetAgent("Solo")
	if ag.Metadata.ResolveDisplayMode() != types.DisplayModeUploaded {
		t.Fatal("expected the upload to be displayed again")
	}
	if ag.Metadata.CharacterCatalogID() != id {
		t.Error("the chosen character should be retained while the upload is shown")
	}
}

func TestUploadedModeRequiresAnUploadedAvatar(t *testing.T) {
	h, _ := characterTestHandlers(t, "Solo")
	rec := patchAgent(t, h, "Solo", `{"character":{"display_mode":"uploaded"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when there is no uploaded avatar, got %d", rec.Code)
	}
}

func TestCharacterModeRequiresASelection(t *testing.T) {
	h, _ := characterTestHandlers(t, "Solo")
	rec := patchAgent(t, h, "Solo", `{"character":{"display_mode":"character"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when no character is selected, got %d", rec.Code)
	}
}

/* ---- isolation from the rest of the definition ----------------------------- */

// FR-64: changing a character changes *only* presentation.
func TestCharacterChangeLeavesTheRestOfTheAgentAlone(t *testing.T) {
	h, st := characterTestHandlers(t, "Solo")
	id := firstWorkingID(t)

	ag, _ := st.GetAgent("Solo")
	ag.Settings.SystemPrompt = "you are careful"
	ag.Settings.Model = "gpt-4o-mini"
	ag.Role = types.RoleGeneral
	ag.Metadata = &types.AgentMetadata{
		Description: "keeps notes",
		Tags:        []string{"research"},
		AvatarColor: "#4f744a",
		Favorite:    true,
	}
	if err := st.SetAgent("Solo", ag); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, _ := json.Marshal(ag.Settings)

	if rec := patchAgent(t, h, "Solo", `{"character":{"catalog_id":"`+id+`","display_mode":"character"}}`); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	after, _ := st.GetAgent("Solo")
	gotSettings, _ := json.Marshal(after.Settings)
	if string(gotSettings) != string(before) {
		t.Errorf("settings changed:\n before %s\n after  %s", before, gotSettings)
	}
	if after.Role != types.RoleGeneral {
		t.Errorf("role changed to %q", after.Role)
	}
	md := after.Metadata
	if md.Description != "keeps notes" || md.AvatarColor != "#4f744a" || !md.Favorite ||
		len(md.Tags) != 1 || md.Tags[0] != "research" {
		t.Errorf("unrelated metadata changed: %+v", md)
	}
}

// FR-92: an identity change participates in the existing stale-edit guard.
func TestCharacterChangeMovesTheVersionToken(t *testing.T) {
	h, st := characterTestHandlers(t, "Solo")
	id := firstWorkingID(t)

	ag, _ := st.GetAgent("Solo")
	before := agentConfigVersion(ag)

	if rec := patchAgent(t, h, "Solo", `{"character":{"catalog_id":"`+id+`","display_mode":"character"}}`); rec.Code != http.StatusOK {
		t.Fatalf("patch failed: %d", rec.Code)
	}

	after, _ := st.GetAgent("Solo")
	if agentConfigVersion(after) == before {
		t.Fatal("a character change must move the concurrency token")
	}
}

func TestStaleVersionBlocksACharacterChange(t *testing.T) {
	h, st := characterTestHandlers(t, "Solo")
	id := firstWorkingID(t)

	rec := patchAgent(t, h, "Solo",
		`{"character":{"catalog_id":"`+id+`","display_mode":"character"},"expected_version":"deadbeefdeadbeef"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a stale token, got %d: %s", rec.Code, rec.Body.String())
	}

	ag, _ := st.GetAgent("Solo")
	if ag.Metadata != nil && ag.Metadata.CharacterCatalogID() != "" {
		t.Fatal("a stale-rejected request must not persist the character")
	}
}

/* ---- clearing --------------------------------------------------------------- */

func TestClearingACharacterDisablesItsVoice(t *testing.T) {
	h, st := characterTestHandlers(t, "Solo")
	id := firstWorkingID(t)

	if rec := patchAgent(t, h, "Solo",
		`{"character":{"catalog_id":"`+id+`","display_mode":"character","voice_enabled":true}}`); rec.Code != http.StatusOK {
		t.Fatalf("setup patch failed: %d", rec.Code)
	}
	if rec := patchAgent(t, h, "Solo",
		`{"character":{"catalog_id":"","display_mode":"fallback"}}`); rec.Code != http.StatusOK {
		t.Fatalf("clear failed: %d", rec.Code)
	}

	ag, _ := st.GetAgent("Solo")
	if ag.Metadata.IsCharacterVoiceEnabled() {
		t.Error("clearing the character must disable its tone layer")
	}
	if ag.Metadata.CharacterCatalogID() != "" {
		t.Error("expected the character to be cleared")
	}
}

func TestVoiceCanBeToggledWithoutRestatingTheCharacter(t *testing.T) {
	h, st := characterTestHandlers(t, "Solo")
	id := firstWorkingID(t)

	if rec := patchAgent(t, h, "Solo",
		`{"character":{"catalog_id":"`+id+`","display_mode":"character","voice_enabled":true}}`); rec.Code != http.StatusOK {
		t.Fatalf("setup failed: %d", rec.Code)
	}
	if rec := patchAgent(t, h, "Solo", `{"character":{"voice_enabled":false}}`); rec.Code != http.StatusOK {
		t.Fatalf("toggle failed: %d", rec.Code)
	}

	ag, _ := st.GetAgent("Solo")
	if ag.Metadata.IsCharacterVoiceEnabled() {
		t.Error("expected the tone layer to be off")
	}
	if ag.Metadata.CharacterCatalogID() != id {
		t.Errorf("toggling voice must not clear the character, got %q", ag.Metadata.CharacterCatalogID())
	}
}

// A PATCH that says nothing about the character must leave it untouched.
// A round trip through the in-memory value is not the same as a round trip
// through the disk. This repo has a standing bug class where a field lives only
// in the folder store and a fresh read silently drops it, so the identity is
// checked against a store built anew from the same files.
func TestACharacterSurvivesAStoreReload(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, "agents_index.json")
	settings := types.Settings{Model: "gpt-4o-mini", Temperature: 1.0}

	st, err := store.NewFileStore(index, settings)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := st.CreateAgent("Solo", &store.CreateAgentConfig{Type: agent.TypeGeneral}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	h := New(st)
	id := firstWorkingID(t)

	if rec := patchAgent(t, h, "Solo",
		`{"character":{"catalog_id":"`+id+`","display_mode":"character","voice_enabled":true}}`); rec.Code != http.StatusOK {
		t.Fatalf("setup failed: %d %s", rec.Code, rec.Body.String())
	}

	reloaded, err := store.NewFileStore(index, settings)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	ag, ok := reloaded.GetAgent("Solo")
	if !ok {
		t.Fatal("agent missing after reload")
	}
	if got := ag.Metadata.CharacterCatalogID(); got != id {
		t.Errorf("catalog id lost on reload: %q", got)
	}
	if got := ag.Metadata.ResolveDisplayMode(); got != types.DisplayModeCharacter {
		t.Errorf("display mode lost on reload: %q", got)
	}
	if !ag.Metadata.IsCharacterVoiceEnabled() {
		t.Error("voice opt-in lost on reload")
	}
}

/* ---- no bulk path may set a character ------------------------------------- */

// Character choice is deliberately one-agent-at-a-time: assigning the same
// identity to a whole batch would produce exactly the interchangeable roster the
// feature exists to avoid (FR-102).
//
// Asserted structurally rather than by absence. "The UI has no bulk button" is
// not a guarantee — a direct POST is one curl away — and "the request struct has
// no character field" is only true until someone adds one. This pins both: the
// wire type cannot carry a character, and a request that smuggles one changes
// nothing.
func TestNoBulkOperationCanSetACharacter(t *testing.T) {
	rt := reflect.TypeFor[bulkRequest]()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		name := strings.ToLower(f.Name + " " + f.Tag.Get("json"))
		if strings.Contains(name, "character") || strings.Contains(name, "catalog") {
			t.Fatalf("bulkRequest.%s would let a batch assign an identity", f.Name)
		}
	}

	// And the same at the wire level: unknown fields are ignored, so a caller
	// who guesses the field name gets a no-op rather than a mass assignment.
	h, st := characterTestHandlers(t, "Solo")
	id := firstWorkingID(t)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/bulk", strings.NewReader(
		`{"agent_names":["Solo"],"operation":"set_favorite","favorite":true,`+
			`"character":{"catalog_id":"`+id+`","display_mode":"character"}}`))
	rec := httptest.NewRecorder()
	h.HandleBulk(rec, req)

	ag, ok := st.GetAgent("Solo")
	if !ok {
		t.Fatal("agent disappeared")
	}
	if got := ag.Metadata.CharacterCatalogID(); got != "" {
		t.Fatalf("a bulk request assigned character %q", got)
	}
}

func TestUnrelatedPatchLeavesTheCharacterAlone(t *testing.T) {
	h, st := characterTestHandlers(t, "Solo")
	id := firstWorkingID(t)

	if rec := patchAgent(t, h, "Solo",
		`{"character":{"catalog_id":"`+id+`","display_mode":"character","voice_enabled":true}}`); rec.Code != http.StatusOK {
		t.Fatalf("setup failed: %d", rec.Code)
	}
	if rec := patchAgent(t, h, "Solo", `{"description":"changed"}`); rec.Code != http.StatusOK {
		t.Fatalf("description patch failed: %d", rec.Code)
	}

	ag, _ := st.GetAgent("Solo")
	if ag.Metadata.CharacterCatalogID() != id || !ag.Metadata.IsCharacterVoiceEnabled() {
		t.Fatalf("an unrelated patch changed the identity: %+v", ag.Metadata.Character)
	}
	if ag.Metadata.Description != "changed" {
		t.Errorf("description did not apply: %q", ag.Metadata.Description)
	}
}
