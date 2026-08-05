package sessionhttp

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/types"
)

func firstWorkingCharacter(t *testing.T) charactercatalog.Character {
	t.Helper()
	working := charactercatalog.MustLoad().Working()
	if len(working) == 0 {
		t.Fatal("catalog has no working characters")
	}
	return working[0]
}

func TestToneAppliesOnlyWhenEveryGatePasses(t *testing.T) {
	ch := firstWorkingCharacter(t)
	id := string(ch.ID)

	cases := []struct {
		name string
		md   *types.AgentMetadata
		want bool
	}{
		{
			name: "displaying the character with voice on",
			md: &types.AgentMetadata{Character: &types.AgentCharacterIdentity{
				DisplayMode: types.DisplayModeCharacter, CatalogID: id, VoiceEnabled: true,
			}},
			want: true,
		},
		{
			name: "voice off",
			md: &types.AgentMetadata{Character: &types.AgentCharacterIdentity{
				DisplayMode: types.DisplayModeCharacter, CatalogID: id,
			}},
			want: false,
		},
		{
			// The character is retained but not shown. Speaking as something the
			// user cannot see would be incoherent.
			name: "voice on but displaying the uploaded avatar",
			md: &types.AgentMetadata{
				AvatarImage: "a.png",
				Character: &types.AgentCharacterIdentity{
					DisplayMode: types.DisplayModeUploaded, CatalogID: id, VoiceEnabled: true,
				},
			},
			want: false,
		},
		{
			name: "voice on but displaying the generated fallback",
			md: &types.AgentMetadata{Character: &types.AgentCharacterIdentity{
				DisplayMode: types.DisplayModeFallback, CatalogID: id, VoiceEnabled: true,
			}},
			want: false,
		},
		{
			name: "voice on with no character selected",
			md: &types.AgentMetadata{Character: &types.AgentCharacterIdentity{
				DisplayMode: types.DisplayModeCharacter, VoiceEnabled: true,
			}},
			want: false,
		},
		{
			// A legacy agent predates the whole system and must be untouched.
			name: "legacy agent",
			md:   &types.AgentMetadata{AvatarImage: "a.png"},
			want: false,
		},
		{name: "nil metadata", md: nil, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint, source := characterToneFor(tc.md)
			if (hint != "") != tc.want {
				t.Fatalf("tone applied=%v, want %v (hint: %q)", hint != "", tc.want, hint)
			}
			if tc.want && source == "" {
				t.Error("an applied tone must say where it came from")
			}
			if !tc.want && source != "" {
				t.Errorf("no tone should mean no source, got %q", source)
			}
		})
	}
}

// A withdrawn or unknown character drops the tone silently rather than leaving
// an agent speaking as something that no longer exists (FR-74).
func TestAWithdrawnCharacterDropsItsTone(t *testing.T) {
	md := &types.AgentMetadata{Character: &types.AgentCharacterIdentity{
		DisplayMode:  types.DisplayModeCharacter,
		CatalogID:    "withdrawn-character-that-never-existed",
		VoiceEnabled: true,
	}}
	if hint, _ := characterToneFor(md); hint != "" {
		t.Fatalf("a withdrawn character produced a tone: %s", hint)
	}
}

// Ori's identity is not assignable, so even a hand-edited record claiming it
// cannot borrow the guide's voice (FR-19/FR-71).
func TestTheGuideIdentityCannotLendItsVoice(t *testing.T) {
	reserved := string(charactercatalog.MustLoad().ReservedGuideID)
	md := &types.AgentMetadata{Character: &types.AgentCharacterIdentity{
		DisplayMode: types.DisplayModeCharacter, CatalogID: reserved, VoiceEnabled: true,
	}}
	if hint, _ := characterToneFor(md); hint != "" {
		t.Fatalf("the reserved guide identity produced a tone: %s", hint)
	}
}

// The source string is for a human reading an effective prompt, so it should
// name the character and the catalog version rather than an opaque id.
func TestToneSourceNamesTheCharacterAndCatalogVersion(t *testing.T) {
	ch := firstWorkingCharacter(t)
	md := &types.AgentMetadata{Character: &types.AgentCharacterIdentity{
		DisplayMode: types.DisplayModeCharacter, CatalogID: string(ch.ID), VoiceEnabled: true,
	}}

	_, source := characterToneFor(md)
	if !strings.Contains(source, ch.Name) {
		t.Errorf("source should name the character, got %q", source)
	}
	if !strings.Contains(source, charactercatalog.MustLoad().Version) {
		t.Errorf("source should name the catalog version, got %q", source)
	}
}

// Composing the tone must not touch the stored prompt. This is the property
// that makes turning the toggle off restore the previous behaviour exactly
// (FR-62).
func TestComposingToneLeavesTheStoredPromptUntouched(t *testing.T) {
	ch := firstWorkingCharacter(t)
	md := &types.AgentMetadata{
		Description: "keeps notes",
		Character: &types.AgentCharacterIdentity{
			DisplayMode: types.DisplayModeCharacter, CatalogID: string(ch.ID), VoiceEnabled: true,
		},
	}
	before := *md.Character

	hint, _ := characterToneFor(md)
	if hint == "" {
		t.Fatal("expected a tone hint")
	}
	if *md.Character != before {
		t.Errorf("composing tone mutated the identity: %+v -> %+v", before, *md.Character)
	}
	if md.Description != "keeps notes" {
		t.Error("composing tone mutated unrelated metadata")
	}
}

// Called repeatedly (chat, task, scheduled run), the hint must be identical
// every time — the layer is applied once per prompt, not accumulated.
func TestToneIsDeterministic(t *testing.T) {
	ch := firstWorkingCharacter(t)
	md := &types.AgentMetadata{Character: &types.AgentCharacterIdentity{
		DisplayMode: types.DisplayModeCharacter, CatalogID: string(ch.ID), VoiceEnabled: true,
	}}

	first, _ := characterToneFor(md)
	for i := 0; i < 5; i++ {
		if got, _ := characterToneFor(md); got != first {
			t.Fatalf("tone hint changed between calls:\n%q\n%q", first, got)
		}
	}
}
