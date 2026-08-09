package types

import (
	"encoding/json"
	"strings"
	"testing"
)

// These tests pin the canonical model's contract. They supersede the
// character-only type tests: the concepts those covered (an explicit stored
// mode, a retained selection, a server-assigned version) all still exist, but
// they now belong to Appearance rather than to a nested character record.

func TestNewAgentAppearanceDefaultsToGenerated(t *testing.T) {
	a := NewAgentAppearance()
	if a.Mode != AppearanceModeGenerated {
		t.Fatalf("a new agent must default to generated, got %q", a.Mode)
	}
	// Non-nil Generated is what makes Generated the always-available fallback:
	// a renderer never has to handle "no source at all" (FR-4/FR-13).
	if a.Generated == nil {
		t.Fatal("generated must be non-nil on a new appearance")
	}
	if a.GeneratedColor() != "" {
		t.Fatalf("a new agent must have no colour override, got %q", a.GeneratedColor())
	}
	if a.Uploaded != nil || a.Character != nil {
		t.Fatal("a new agent must not carry empty upload/character objects")
	}
}

func TestIsValidAppearanceMode(t *testing.T) {
	for _, m := range []AppearanceMode{AppearanceModeGenerated, AppearanceModeCharacter, AppearanceModeUploaded} {
		if !IsValidAppearanceMode(m) {
			t.Errorf("%q must be a valid mode", m)
		}
	}
	// "fallback" is explicitly among the rejects: it was a saved mode in the old
	// schema and must never become one again (FR-15).
	for _, m := range []AppearanceMode{"", "fallback", "Generated", "GENERATED", "uploaded ", "guide", "hologram"} {
		if IsValidAppearanceMode(m) {
			t.Errorf("%q must not be a valid mode", m)
		}
	}
}

func TestNormalizeAppearanceColor(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		valid bool
	}{
		{"#6D5DFC", "#6d5dfc", true},
		{"#abc", "#aabbcc", true},
		{"abc", "#aabbcc", true},
		{"6d5dfc", "#6d5dfc", true},
		{"  #6d5dfc  ", "#6d5dfc", true},
		// Absent is a legitimate state meaning "use the deterministic colour",
		// not an error (FR-6).
		{"", "", true},
		{"#12345", "", false},
		{"#gggggg", "", false},
		{"rgb(1,2,3)", "", false},
		{"#6d5dfcff", "", false},
	}
	for _, tc := range cases {
		got, ok := NormalizeAppearanceColor(tc.in)
		if ok != tc.valid {
			t.Errorf("NormalizeAppearanceColor(%q) validity = %v, want %v", tc.in, ok, tc.valid)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeAppearanceColor(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeFixesStructureWithoutChangingIntent(t *testing.T) {
	a := &AgentAppearance{
		Mode:      "hologram",
		Generated: &GeneratedAppearance{Color: "#ABC"},
		Uploaded:  &UploadedAppearance{Image: "   "},
		Character: &CharacterAppearance{CatalogID: "  ", CatalogVersion: 3},
	}
	a.Normalize()

	if a.Mode != AppearanceModeGenerated {
		t.Errorf("an unknown mode must normalize to generated, got %q", a.Mode)
	}
	if a.GeneratedColor() != "#aabbcc" {
		t.Errorf("colour must be normalized, got %q", a.GeneratedColor())
	}
	if a.Uploaded != nil {
		t.Error("an empty upload object must collapse to nil")
	}
	if a.Character != nil {
		t.Error("an empty character object must collapse to nil")
	}
}

func TestNormalizeDropsAColourThatIsNotAColour(t *testing.T) {
	a := &AgentAppearance{Mode: AppearanceModeGenerated, Generated: &GeneratedAppearance{Color: "chartreuse"}}
	a.Normalize()
	if a.GeneratedColor() != "" {
		t.Fatalf("an invalid stored colour must be dropped, got %q", a.GeneratedColor())
	}
	if a.Generated == nil {
		t.Fatal("dropping the colour must not drop the generated object")
	}
}

func TestNormalizeLeavesAnActiveModeAloneEvenWithoutItsAsset(t *testing.T) {
	// Structural normalization must not second-guess the user's saved choice.
	// A missing asset is a render-time condition the renderer reports and
	// recovers from; rewriting the saved mode here would turn a temporary
	// problem into permanent data loss (FR-84).
	a := &AgentAppearance{Mode: AppearanceModeUploaded}
	a.Normalize()
	if a.Mode != AppearanceModeUploaded {
		t.Fatalf("normalize must not downgrade a saved mode, got %q", a.Mode)
	}
}

func TestCloneIsolatesEveryNestedSource(t *testing.T) {
	orig := &AgentAppearance{
		Mode:      AppearanceModeCharacter,
		Generated: &GeneratedAppearance{Color: "#6d5dfc"},
		Uploaded:  &UploadedAppearance{Image: "atlas.webp"},
		Character: &CharacterAppearance{CatalogID: "sable", CatalogVersion: 2},
	}
	clone := orig.Clone()

	clone.Mode = AppearanceModeGenerated
	clone.Generated.Color = "#000000"
	clone.Uploaded.Image = "other.png"
	clone.Character.CatalogID = "other"
	clone.Character.CatalogVersion = 99

	if orig.Mode != AppearanceModeCharacter ||
		orig.GeneratedColor() != "#6d5dfc" ||
		orig.UploadedImage() != "atlas.webp" ||
		orig.CharacterCatalogID() != "sable" ||
		orig.CharacterCatalogVersion() != 2 {
		t.Fatalf("mutating a clone must not touch the original: %+v", orig)
	}

	if (*AgentAppearance)(nil).Clone() != nil {
		t.Error("cloning nil must return nil")
	}
}

// The switching matrix below is the heart of "reversible, non-destructive".
// Each case asserts both what changed and what must NOT have (FR-11/FR-12,
// FR-30, FR-33 through FR-40).

func TestChoosingASourceNeverDeletesTheOthers(t *testing.T) {
	a := &AgentAppearance{
		Mode:      AppearanceModeUploaded,
		Generated: &GeneratedAppearance{Color: "#6d5dfc"},
		Uploaded:  &UploadedAppearance{Image: "atlas.webp"},
		Character: &CharacterAppearance{CatalogID: "sable", CatalogVersion: 2},
	}

	a.Mode = AppearanceModeCharacter
	a.Normalize()
	if a.UploadedImage() != "atlas.webp" {
		t.Error("switching to character must keep the uploaded file")
	}

	a.Mode = AppearanceModeGenerated
	a.Normalize()
	if a.CharacterCatalogID() != "sable" || a.UploadedImage() != "atlas.webp" {
		t.Error("switching to generated must keep both other sources")
	}
}

func TestClearCharacterReturnsToGeneratedOnlyWhenItWasActive(t *testing.T) {
	active := &AgentAppearance{
		Mode:      AppearanceModeCharacter,
		Generated: &GeneratedAppearance{Color: "#6d5dfc"},
		Uploaded:  &UploadedAppearance{Image: "atlas.webp"},
		Character: &CharacterAppearance{CatalogID: "sable", CatalogVersion: 2},
	}
	active.ClearCharacter()
	if active.Mode != AppearanceModeGenerated {
		t.Errorf("removing the active character must fall back to generated, got %q", active.Mode)
	}
	if active.UploadedImage() != "atlas.webp" || active.GeneratedColor() != "#6d5dfc" {
		t.Error("character removal must not touch the upload or the colour")
	}

	inactive := &AgentAppearance{
		Mode:      AppearanceModeUploaded,
		Generated: &GeneratedAppearance{},
		Uploaded:  &UploadedAppearance{Image: "atlas.webp"},
		Character: &CharacterAppearance{CatalogID: "sable", CatalogVersion: 2},
	}
	inactive.ClearCharacter()
	if inactive.Mode != AppearanceModeUploaded {
		t.Errorf("removing an inactive character must not change the active mode, got %q", inactive.Mode)
	}
	if inactive.Character != nil {
		t.Error("the character selection must actually be gone")
	}
}

func TestClearUploadReturnsToGeneratedOnlyWhenItWasActive(t *testing.T) {
	active := &AgentAppearance{
		Mode:      AppearanceModeUploaded,
		Generated: &GeneratedAppearance{Color: "#6d5dfc"},
		Uploaded:  &UploadedAppearance{Image: "atlas.webp"},
		Character: &CharacterAppearance{CatalogID: "sable", CatalogVersion: 2},
	}
	active.ClearUpload()
	if active.Mode != AppearanceModeGenerated {
		t.Errorf("removing the active upload must fall back to generated, got %q", active.Mode)
	}
	if active.CharacterCatalogID() != "sable" || active.GeneratedColor() != "#6d5dfc" {
		t.Error("upload removal must not touch the character or the colour")
	}

	inactive := &AgentAppearance{
		Mode:      AppearanceModeCharacter,
		Generated: &GeneratedAppearance{},
		Uploaded:  &UploadedAppearance{Image: "atlas.webp"},
		Character: &CharacterAppearance{CatalogID: "sable", CatalogVersion: 2},
	}
	inactive.ClearUpload()
	if inactive.Mode != AppearanceModeCharacter {
		t.Errorf("removing an inactive upload must not change the active mode, got %q", inactive.Mode)
	}
}

func TestSetUploadActivatesInTheSameOperation(t *testing.T) {
	a := &AgentAppearance{Mode: AppearanceModeCharacter, Character: &CharacterAppearance{CatalogID: "sable", CatalogVersion: 1}}
	a.SetUpload("atlas.webp")
	if a.Mode != AppearanceModeUploaded {
		t.Fatalf("a saved upload must become the rendered source, got %q", a.Mode)
	}
	if a.CharacterCatalogID() != "sable" {
		t.Error("uploading must not discard the character selection")
	}
}

func TestSetCharacterActivatesAndTakesTheServerVersion(t *testing.T) {
	a := NewAgentAppearance()
	a.SetCharacter("  sable  ", 7)
	if a.Mode != AppearanceModeCharacter {
		t.Fatalf("choosing a character must activate it, got %q", a.Mode)
	}
	if a.CharacterCatalogID() != "sable" {
		t.Errorf("catalog id must be trimmed, got %q", a.CharacterCatalogID())
	}
	if a.CharacterCatalogVersion() != 7 {
		t.Errorf("catalog version must come from the caller, got %d", a.CharacterCatalogVersion())
	}
}

func TestClearGeneratedColorPreservesEverythingElse(t *testing.T) {
	a := &AgentAppearance{
		Mode:      AppearanceModeCharacter,
		Generated: &GeneratedAppearance{Color: "#6d5dfc"},
		Uploaded:  &UploadedAppearance{Image: "atlas.webp"},
		Character: &CharacterAppearance{CatalogID: "sable", CatalogVersion: 2},
	}
	a.ClearGeneratedColor()
	if a.GeneratedColor() != "" {
		t.Error("reset must drop the override")
	}
	if a.Mode != AppearanceModeCharacter || a.UploadedImage() == "" || a.CharacterCatalogID() == "" {
		t.Error("resetting the colour must not touch the active mode or the other sources")
	}
}

func TestSetGeneratedColorRejectsGarbageWithoutMutating(t *testing.T) {
	a := &AgentAppearance{Mode: AppearanceModeGenerated, Generated: &GeneratedAppearance{Color: "#6d5dfc"}}
	if a.SetGeneratedColor("not-a-colour") {
		t.Fatal("an invalid colour must be rejected")
	}
	if a.GeneratedColor() != "#6d5dfc" {
		t.Fatalf("a rejected colour must leave the stored one intact, got %q", a.GeneratedColor())
	}
}

func TestCanonicalJSONShape(t *testing.T) {
	a := &AgentAppearance{
		Mode:      AppearanceModeCharacter,
		Generated: &GeneratedAppearance{Color: "#6d5dfc"},
		Uploaded:  &UploadedAppearance{Image: "atlas.webp"},
		Character: &CharacterAppearance{CatalogID: "sable", CatalogVersion: 1},
	}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	want := `{"mode":"character","generated":{"color":"#6d5dfc"},"uploaded":{"image":"atlas.webp"},"character":{"catalog_id":"sable","catalog_version":1}}`
	if got != want {
		t.Fatalf("canonical shape drifted:\n got %s\nwant %s", got, want)
	}
	// The retired vocabulary must not appear anywhere in a serialized record.
	for _, retired := range []string{"avatar_color", "avatar_image", "display_mode", "voice_enabled", "fallback"} {
		if strings.Contains(got, retired) {
			t.Errorf("serialized appearance still mentions %q", retired)
		}
	}
}
