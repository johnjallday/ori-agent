package types

import (
	"encoding/json"
	"strings"
	"testing"
)

/* ---- backward compatibility ---------------------------------------------- */

// The single most important property of this change: an agent record written
// before the character system must deserialize unchanged and re-serialize
// without acquiring a character key (FR-58/FR-69/FR-75).
func TestLegacyMetadataRoundTripsWithoutGainingCharacterFields(t *testing.T) {
	legacy := `{"description":"Finds sources","tags":["research"],"avatar_color":"#4f744a","avatar_image":"sable.png","favorite":true}`

	var md AgentMetadata
	if err := json.Unmarshal([]byte(legacy), &md); err != nil {
		t.Fatalf("legacy metadata failed to decode: %v", err)
	}
	if md.Character != nil {
		t.Fatal("decoding legacy metadata must not synthesize a character identity")
	}
	if md.Description != "Finds sources" || md.AvatarImage != "sable.png" || !md.Favorite {
		t.Fatalf("legacy fields did not survive: %+v", md)
	}

	out, err := json.Marshal(&md)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "character") {
		t.Fatalf("re-serialized legacy metadata gained a character key: %s", out)
	}
}

func TestEmptyCharacterIdentityIsOmitted(t *testing.T) {
	md := AgentMetadata{Description: "x"}
	out, _ := json.Marshal(&md)
	for _, key := range []string{"character", "display_mode", "catalog_id", "voice_enabled"} {
		if strings.Contains(string(out), key) {
			t.Errorf("empty metadata emitted %q: %s", key, out)
		}
	}
}

func TestCharacterIdentitySurvivesRoundTrip(t *testing.T) {
	md := AgentMetadata{
		Character: &AgentCharacterIdentity{
			DisplayMode:    DisplayModeCharacter,
			CatalogID:      "sable",
			CatalogVersion: 1,
			VoiceEnabled:   true,
		},
	}
	raw, err := json.Marshal(&md)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back AgentMetadata
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Character == nil {
		t.Fatal("character identity was lost")
	}
	if *back.Character != *md.Character {
		t.Fatalf("identity changed across round trip: %+v vs %+v", back.Character, md.Character)
	}
}

/* ---- display-mode resolution ---------------------------------------------- */

func TestResolveDisplayModeUsesTheExplicitChoice(t *testing.T) {
	cases := []struct {
		name string
		md   AgentMetadata
		want AgentDisplayMode
	}{
		{
			// The case that proves mode is not inferred from field presence:
			// an uploaded image is present, but the user chose the character.
			name: "explicit character wins over a present upload",
			md: AgentMetadata{
				AvatarImage: "sable.png",
				Character:   &AgentCharacterIdentity{DisplayMode: DisplayModeCharacter, CatalogID: "sable"},
			},
			want: DisplayModeCharacter,
		},
		{
			// And the reverse: a chosen character is retained while the user
			// displays their upload.
			name: "explicit upload wins over a stored character",
			md: AgentMetadata{
				AvatarImage: "sable.png",
				Character:   &AgentCharacterIdentity{DisplayMode: DisplayModeUploaded, CatalogID: "sable"},
			},
			want: DisplayModeUploaded,
		},
		{
			name: "explicit fallback is honoured even with both present",
			md: AgentMetadata{
				AvatarImage: "sable.png",
				Character:   &AgentCharacterIdentity{DisplayMode: DisplayModeFallback, CatalogID: "sable"},
			},
			want: DisplayModeFallback,
		},
		{
			name: "legacy record with an upload keeps rendering the upload",
			md:   AgentMetadata{AvatarImage: "sable.png"},
			want: DisplayModeUploaded,
		},
		{
			name: "legacy record with nothing falls back",
			md:   AgentMetadata{},
			want: DisplayModeFallback,
		},
		{
			name: "blank avatar image is not an upload",
			md:   AgentMetadata{AvatarImage: "   "},
			want: DisplayModeFallback,
		},
		{
			name: "an unrecognized stored mode degrades to the legacy rule",
			md: AgentMetadata{
				AvatarImage: "sable.png",
				Character:   &AgentCharacterIdentity{DisplayMode: "hologram"},
			},
			want: DisplayModeUploaded,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.md.ResolveDisplayMode(); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveDisplayModeOnNilMetadata(t *testing.T) {
	var md *AgentMetadata
	if got := md.ResolveDisplayMode(); got != DisplayModeFallback {
		t.Fatalf("nil metadata should fall back, got %q", got)
	}
}

// Switching away from a character must not discard it, or "switch back" would
// mean "pick again" (FR-68 applied to characters).
func TestCatalogIDIsRetainedAcrossModeSwitches(t *testing.T) {
	md := AgentMetadata{
		AvatarImage: "sable.png",
		Character:   &AgentCharacterIdentity{DisplayMode: DisplayModeUploaded, CatalogID: "sable", CatalogVersion: 1},
	}
	if got := md.CharacterCatalogID(); got != "sable" {
		t.Fatalf("expected the stored character to be retained, got %q", got)
	}

	md.Character.DisplayMode = DisplayModeCharacter
	if got := md.CharacterCatalogID(); got != "sable" {
		t.Fatalf("switching back lost the character: %q", got)
	}
}

func TestCharacterCatalogIDIsEmptyWhenNeverChosen(t *testing.T) {
	for _, md := range []AgentMetadata{
		{},
		{AvatarImage: "x.png"},
		{Character: &AgentCharacterIdentity{DisplayMode: DisplayModeFallback}},
		{Character: &AgentCharacterIdentity{CatalogID: "   "}},
	} {
		if got := md.CharacterCatalogID(); got != "" {
			t.Errorf("expected no catalog id for %+v, got %q", md, got)
		}
	}
}

/* ---- voice gating ---------------------------------------------------------- */

// Tone is opt-in and only applies while the character is actually displayed,
// so a user who switches to their upload does not keep an invisible character's
// voice (FR-60/FR-61).
func TestVoiceRequiresOptInAndAnActiveCharacter(t *testing.T) {
	cases := []struct {
		name string
		md   AgentMetadata
		want bool
	}{
		{
			name: "enabled with an active character",
			md:   AgentMetadata{Character: &AgentCharacterIdentity{DisplayMode: DisplayModeCharacter, CatalogID: "sable", VoiceEnabled: true}},
			want: true,
		},
		{
			name: "off by default",
			md:   AgentMetadata{Character: &AgentCharacterIdentity{DisplayMode: DisplayModeCharacter, CatalogID: "sable"}},
			want: false,
		},
		{
			name: "enabled but displaying the upload instead",
			md:   AgentMetadata{AvatarImage: "s.png", Character: &AgentCharacterIdentity{DisplayMode: DisplayModeUploaded, CatalogID: "sable", VoiceEnabled: true}},
			want: false,
		},
		{
			name: "enabled but displaying the fallback",
			md:   AgentMetadata{Character: &AgentCharacterIdentity{DisplayMode: DisplayModeFallback, CatalogID: "sable", VoiceEnabled: true}},
			want: false,
		},
		{
			name: "enabled with no character selected",
			md:   AgentMetadata{Character: &AgentCharacterIdentity{DisplayMode: DisplayModeCharacter, VoiceEnabled: true}},
			want: false,
		},
		{name: "legacy record", md: AgentMetadata{AvatarImage: "s.png"}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.md.IsCharacterVoiceEnabled(); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsValidDisplayMode(t *testing.T) {
	for _, m := range []AgentDisplayMode{DisplayModeFallback, DisplayModeUploaded, DisplayModeCharacter} {
		if !IsValidDisplayMode(m) {
			t.Errorf("%q should be valid", m)
		}
	}
	for _, m := range []AgentDisplayMode{"", "hologram", "Character", "CHARACTER", "uploaded ", "guide"} {
		if IsValidDisplayMode(m) {
			t.Errorf("%q should be rejected", m)
		}
	}
}

func TestCloneIsIndependent(t *testing.T) {
	orig := &AgentCharacterIdentity{DisplayMode: DisplayModeCharacter, CatalogID: "sable", VoiceEnabled: true}
	clone := orig.Clone()
	clone.CatalogID = "moss"
	clone.VoiceEnabled = false
	if orig.CatalogID != "sable" || !orig.VoiceEnabled {
		t.Fatalf("mutating the clone changed the original: %+v", orig)
	}
	if (*AgentCharacterIdentity)(nil).Clone() != nil {
		t.Fatal("cloning nil should return nil")
	}
}

// A character is presentation and tone. If it ever gains a field that could
// carry instructions or capability, the tone layer stops being bounded.
func TestCharacterIdentityCarriesNoFreeformInstructionField(t *testing.T) {
	raw, _ := json.Marshal(&AgentCharacterIdentity{
		DisplayMode: DisplayModeCharacter, CatalogID: "sable", CatalogVersion: 1, VoiceEnabled: true,
	})
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	allowed := map[string]bool{
		"display_mode": true, "catalog_id": true, "catalog_version": true, "voice_enabled": true,
	}
	for k := range fields {
		if !allowed[k] {
			t.Errorf("unexpected character field %q — character metadata must not carry free-form text", k)
		}
	}
}
