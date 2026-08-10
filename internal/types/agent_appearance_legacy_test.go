package types

import (
	"encoding/json"
	"testing"
)

// testEnv is the migration environment used by most cases: one assignable
// character at version 4, one upload that exists.
func testEnv() AppearanceEnvironment {
	return AppearanceEnvironment{
		CharacterVersion: func(id string) (int, bool) {
			if id == "sable" {
				return 4, true
			}
			return 0, false
		},
		UploadExists: func(name string) bool { return name == "atlas.webp" },
	}
}

func decodeLegacyMetadata(t *testing.T, raw string) *AgentMetadata {
	t.Helper()
	var md AgentMetadata
	if err := json.Unmarshal([]byte(raw), &md); err != nil {
		t.Fatalf("decode legacy metadata: %v", err)
	}
	return &md
}

// TestLegacyFieldsAreCapturedButNeverReserialized is the property that makes
// "no permanent dual-write path" structural rather than a rule to remember: the
// old fields have nowhere to live on the struct, so they cannot come back out
// (FR-14/FR-77).
func TestLegacyFieldsAreCapturedButNeverReserialized(t *testing.T) {
	md := decodeLegacyMetadata(t, `{
		"description": "kept",
		"avatar_color": "#3366FF",
		"avatar_image": "atlas.webp",
		"character": {"display_mode": "character", "catalog_id": "sable", "catalog_version": 1, "voice_enabled": true}
	}`)

	if md.Description != "kept" {
		t.Errorf("unrelated metadata must survive decoding, got %q", md.Description)
	}

	raw, err := json.Marshal(md)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, retired := range []string{"avatar_color", "avatar_image", "character"} {
		if _, present := round[retired]; present {
			t.Errorf("re-serialized metadata still carries %q", retired)
		}
	}

	legacy := md.TakeLegacyAppearance()
	if legacy == nil {
		t.Fatal("legacy state must be captured for migration")
	}
	if legacy.AvatarColor != "#3366FF" || legacy.AvatarImage != "atlas.webp" ||
		legacy.CatalogID != "sable" || legacy.DisplayMode != "character" || !legacy.VoiceEnabled {
		t.Fatalf("legacy capture is incomplete: %+v", legacy)
	}
	// Draining rather than peeking is what makes a second pass a no-op (FR-76).
	if md.TakeLegacyAppearance() != nil {
		t.Error("legacy state must be drained on first take")
	}
}

// TestMigrationMappingTable walks the exact FR-70 table.
func TestMigrationMappingTable(t *testing.T) {
	cases := []struct {
		name        string
		legacy      LegacyAppearance
		wantMode    AppearanceMode
		wantColor   string
		wantImage   string
		wantCharID  string
		wantVersion int
		wantReasons []string
	}{
		{
			name:     "generated only",
			legacy:   LegacyAppearance{},
			wantMode: AppearanceModeGenerated,
		},
		{
			name:      "custom colour",
			legacy:    LegacyAppearance{AvatarColor: "#3366FF"},
			wantMode:  AppearanceModeGenerated,
			wantColor: "#3366ff",
		},
		{
			name:      "uploaded active via explicit mode",
			legacy:    LegacyAppearance{AvatarImage: "atlas.webp", DisplayMode: "uploaded", HadCharacter: true},
			wantMode:  AppearanceModeUploaded,
			wantImage: "atlas.webp",
		},
		{
			name:        "character active takes the current catalog version",
			legacy:      LegacyAppearance{DisplayMode: "character", CatalogID: "sable", CatalogVersion: 1, HadCharacter: true},
			wantMode:    AppearanceModeCharacter,
			wantCharID:  "sable",
			wantVersion: 4,
		},
		{
			name:        "fallback becomes generated",
			legacy:      LegacyAppearance{DisplayMode: "fallback", CatalogID: "sable", HadCharacter: true},
			wantMode:    AppearanceModeGenerated,
			wantCharID:  "sable",
			wantVersion: 4,
		},
		{
			name:      "no explicit mode with an upload",
			legacy:    LegacyAppearance{AvatarImage: "atlas.webp"},
			wantMode:  AppearanceModeUploaded,
			wantImage: "atlas.webp",
		},
		{
			name:     "no explicit mode and no upload",
			legacy:   LegacyAppearance{AvatarColor: "#abc"},
			wantMode: AppearanceModeGenerated,
			// Shorthand expands so exactly one form is ever stored (FR-7).
			wantColor: "#aabbcc",
		},
		{
			name: "every inactive source is retained",
			legacy: LegacyAppearance{
				AvatarColor: "#3366ff", AvatarImage: "atlas.webp",
				DisplayMode: "character", CatalogID: "sable", HadCharacter: true,
			},
			wantMode:    AppearanceModeCharacter,
			wantColor:   "#3366ff",
			wantImage:   "atlas.webp",
			wantCharID:  "sable",
			wantVersion: 4,
		},
		{
			name:        "voice is discarded, not mapped",
			legacy:      LegacyAppearance{DisplayMode: "character", CatalogID: "sable", VoiceEnabled: true, HadCharacter: true},
			wantMode:    AppearanceModeCharacter,
			wantCharID:  "sable",
			wantVersion: 4,
			wantReasons: []string{AppearanceReasonVoiceDiscarded},
		},
		{
			name:        "a missing upload cannot stay active",
			legacy:      LegacyAppearance{AvatarImage: "gone.png", DisplayMode: "uploaded", HadCharacter: true},
			wantMode:    AppearanceModeGenerated,
			wantImage:   "gone.png",
			wantReasons: []string{AppearanceReasonUploadMissing},
		},
		{
			name:        "a withdrawn character cannot stay active",
			legacy:      LegacyAppearance{DisplayMode: "character", CatalogID: "retired", CatalogVersion: 2, HadCharacter: true},
			wantMode:    AppearanceModeGenerated,
			wantCharID:  "retired",
			wantVersion: 2,
			wantReasons: []string{AppearanceReasonCharacterUnavailable},
		},
		{
			name:        "an unknown mode becomes generated",
			legacy:      LegacyAppearance{DisplayMode: "hologram", AvatarImage: "atlas.webp", HadCharacter: true},
			wantMode:    AppearanceModeGenerated,
			wantImage:   "atlas.webp",
			wantReasons: []string{AppearanceReasonInvalidMode},
		},
		{
			name:        "a colour that is not a colour is dropped",
			legacy:      LegacyAppearance{AvatarColor: "chartreuse"},
			wantMode:    AppearanceModeGenerated,
			wantReasons: []string{AppearanceReasonInvalidColor},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := tc.legacy
			result := MigrateAppearance(nil, &legacy, testEnv())
			got := result.Appearance
			if got.Mode != tc.wantMode {
				t.Errorf("mode = %q, want %q", got.Mode, tc.wantMode)
			}
			if got.GeneratedColor() != tc.wantColor {
				t.Errorf("colour = %q, want %q", got.GeneratedColor(), tc.wantColor)
			}
			if got.UploadedImage() != tc.wantImage {
				t.Errorf("image = %q, want %q", got.UploadedImage(), tc.wantImage)
			}
			if got.CharacterCatalogID() != tc.wantCharID {
				t.Errorf("catalog id = %q, want %q", got.CharacterCatalogID(), tc.wantCharID)
			}
			if got.CharacterCatalogVersion() != tc.wantVersion {
				t.Errorf("catalog version = %d, want %d", got.CharacterCatalogVersion(), tc.wantVersion)
			}
			if !sameReasons(result.Reasons, tc.wantReasons) {
				t.Errorf("reasons = %v, want %v", result.Reasons, tc.wantReasons)
			}
			if got.Generated == nil {
				t.Error("every migrated record must carry a generated object")
			}
		})
	}
}

func TestMigrationPrefersCanonicalOverLegacy(t *testing.T) {
	// A record carrying both — a downgrade, a hand-edit, an old snapshot — must
	// resolve to the canonical value, or a stale snapshot could quietly undo the
	// migration (FR-71).
	canonical := &AgentAppearance{
		Mode:      AppearanceModeGenerated,
		Generated: &GeneratedAppearance{Color: "#111111"},
	}
	legacy := &LegacyAppearance{
		AvatarColor: "#222222", AvatarImage: "atlas.webp",
		DisplayMode: "uploaded", CatalogID: "sable", HadCharacter: true,
	}

	result := MigrateAppearance(canonical, legacy, testEnv())
	if result.Appearance.Mode != AppearanceModeGenerated {
		t.Errorf("canonical mode must win, got %q", result.Appearance.Mode)
	}
	if result.Appearance.GeneratedColor() != "#111111" {
		t.Errorf("canonical colour must win, got %q", result.Appearance.GeneratedColor())
	}
	if result.Appearance.UploadedImage() != "" {
		t.Error("legacy fields must not be merged into a canonical record")
	}
	// Legacy fields were present, so the record still needs rewriting to shed
	// them, even though nothing about the appearance itself changed.
	if !result.Changed {
		t.Error("a record still carrying legacy fields must be rewritten")
	}
}

func TestMigrationIsIdempotent(t *testing.T) {
	legacy := LegacyAppearance{
		AvatarColor: "#3366ff", AvatarImage: "atlas.webp",
		DisplayMode: "character", CatalogID: "sable", CatalogVersion: 1, HadCharacter: true,
	}
	first := MigrateAppearance(nil, &legacy, testEnv())

	// The second pass sees the canonical result and no legacy state — exactly
	// what a restart looks like after the first migration was persisted (FR-76).
	second := MigrateAppearance(first.Appearance, nil, testEnv())

	if second.Changed {
		t.Error("a second pass over migrated data must report no change")
	}
	firstJSON, _ := json.Marshal(first.Appearance)
	secondJSON, _ := json.Marshal(second.Appearance)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("second pass drifted:\n first %s\nsecond %s", firstJSON, secondJSON)
	}
	// Catalog versions must not churn on every startup (FR-76).
	if second.Appearance.CharacterCatalogVersion() != 4 {
		t.Errorf("catalog version churned to %d", second.Appearance.CharacterCatalogVersion())
	}
}

func TestMigrationTrustsTheRecordWhenTheEnvironmentCannotTell(t *testing.T) {
	// Nil callbacks mean "cannot tell" — a transient catalog or filesystem
	// problem must not permanently downgrade a saved choice. The renderer's
	// runtime fallback still covers the case.
	legacy := LegacyAppearance{DisplayMode: "character", CatalogID: "sable", CatalogVersion: 3, HadCharacter: true}
	result := MigrateAppearance(nil, &legacy, AppearanceEnvironment{})
	if result.Appearance.Mode != AppearanceModeCharacter {
		t.Fatalf("mode = %q, want character", result.Appearance.Mode)
	}
	if result.Appearance.CharacterCatalogVersion() != 3 {
		t.Errorf("the stored version must be kept when the catalog is unreadable, got %d", result.Appearance.CharacterCatalogVersion())
	}
}

func TestMigrationGivesEveryRecordAnAppearance(t *testing.T) {
	result := MigrateAppearance(nil, nil, testEnv())
	if result.Appearance == nil {
		t.Fatal("migration must never return a nil appearance")
	}
	if result.Appearance.Mode != AppearanceModeGenerated {
		t.Errorf("mode = %q, want generated", result.Appearance.Mode)
	}
	if !result.Changed {
		t.Error("materializing the default appearance is a change worth persisting")
	}
}

func sameReasons(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
