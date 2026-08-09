package characterhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
)

// The catalog is the last place the removed promise could survive: leaving tone
// fields in the response would keep "choosing a character changes how my agent
// talks" alive even with every prompt path ignoring them (PRD FR-22/FR-67).

func serveCatalog(t *testing.T) (int, string) {
	t.Helper()
	h, err := New()
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeCatalog(rec, httptest.NewRequest(http.MethodGet, "/api/characters", nil))
	return rec.Code, rec.Body.String()
}

func TestCatalogResponseCarriesNoToneOrSpeech(t *testing.T) {
	code, body := serveCatalog(t)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	for _, retired := range []string{"tone_traits", "sample_line", "voice", "prompt", "instruction"} {
		if strings.Contains(body, retired) {
			t.Errorf("catalog response still mentions %q", retired)
		}
	}
}

func TestCatalogStillServesTheVisualMetadataThePickerNeeds(t *testing.T) {
	// Removing tone must not have hollowed out the entries: the picker still
	// needs a name, a family, a portrait, and the visual facts it shows.
	code, body := serveCatalog(t)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var payload struct {
		Characters []map[string]any `json:"characters"`
		Guide      map[string]any   `json:"guide"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Characters) < charactercatalog.MinWorkingCharacters {
		t.Fatalf("assignable set shrank to %d, below the floor of %d",
			len(payload.Characters), charactercatalog.MinWorkingCharacters)
	}
	for _, ch := range payload.Characters {
		for _, required := range []string{"id", "name", "family_label", "description", "silhouette", "signature_prop", "idle_behavior"} {
			if v, ok := ch[required].(string); !ok || strings.TrimSpace(v) == "" {
				t.Errorf("%v: %q is missing from the browser DTO", ch["id"], required)
			}
		}
		assets, _ := ch["assets"].(map[string]any)
		if portrait, _ := assets["portrait"].(string); strings.TrimSpace(portrait) == "" {
			t.Errorf("%v: portrait asset is missing", ch["id"])
		}
	}
	// The guide stays a separate object so a client cannot render Ori as a
	// selectable option for a working agent (FR-25).
	if payload.Guide == nil {
		t.Fatal("the reserved guide must remain a separate object")
	}
	for _, ch := range payload.Characters {
		if ch["id"] == payload.Guide["id"] {
			t.Fatal("the reserved guide leaked into the assignable list")
		}
	}
}

func TestCatalogRoleAffinityRemainsAPickerHintOnly(t *testing.T) {
	// Role affinity may rank suggestions, but it must not restrict anything —
	// every character stays selectable for every agent (FR-24/FR-48).
	cat := charactercatalog.MustLoad()
	for _, ch := range cat.Working() {
		if !cat.IsAssignable(ch.ID) {
			t.Errorf("%s is not assignable despite being a working character", ch.ID)
		}
	}
	// An entry with no declared roles is still assignable; an absent list must
	// never read as "compatible with nothing".
	found := false
	for _, ch := range cat.Working() {
		if len(ch.Roles) == 0 {
			found = true
			if !cat.IsAssignable(ch.ID) {
				t.Errorf("%s with no role affinity must still be assignable", ch.ID)
			}
		}
	}
	_ = found // Not every catalog build has such an entry; the loop is the assertion.
}
