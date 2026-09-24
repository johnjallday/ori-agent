package agentappearance

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/charactercatalog"
	"github.com/johnjallday/ori-agent/internal/types"
)

func assignableCharacter(t *testing.T) (string, int) {
	t.Helper()
	cat, err := charactercatalog.Load()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	working := cat.Working()
	if len(working) == 0 {
		t.Fatal("the catalog declares no working character")
	}
	return string(working[0].ID), working[0].EntryVersion
}

func TestValidateStaged_NilMeansNoChoice(t *testing.T) {
	got, err := ValidateStaged(nil)
	if err != nil || got != nil {
		t.Fatalf("ValidateStaged(nil) = %#v, %v; want nil, nil", got, err)
	}
}

func TestValidateStaged_CharacterGetsTheServerAssignedVersion(t *testing.T) {
	id, version := assignableCharacter(t)
	got, err := ValidateStaged(&types.AgentAppearance{
		Mode:      types.AppearanceModeCharacter,
		Generated: &types.GeneratedAppearance{Color: "ABC"},
		Character: &types.CharacterAppearance{CatalogID: " " + id + " "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != types.AppearanceModeCharacter {
		t.Fatalf("mode = %q, want character", got.Mode)
	}
	if got.CharacterCatalogID() != id || got.CharacterCatalogVersion() != version {
		t.Fatalf("character = %s@%d, want %s@%d", got.CharacterCatalogID(), got.CharacterCatalogVersion(), id, version)
	}
	// The inactive colour survives, normalized, exactly as on the agent
	// create endpoint: choosing a source is not deleting another.
	if got.GeneratedColor() != "#aabbcc" {
		t.Fatalf("generated colour = %q, want #aabbcc", got.GeneratedColor())
	}
}

// A create request is normalized at its readiness review and again at
// creation, so validating the canonical record a second time must be a no-op.
func TestValidateStaged_IsIdempotent(t *testing.T) {
	id, _ := assignableCharacter(t)
	first, err := ValidateStaged(&types.AgentAppearance{
		Mode:      types.AppearanceModeCharacter,
		Character: &types.CharacterAppearance{CatalogID: id},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ValidateStaged(first)
	if err != nil {
		t.Fatalf("re-validating the canonical record was refused: %v", err)
	}
	if *second.Character != *first.Character || second.Mode != first.Mode {
		t.Fatalf("re-validation changed the record: %#v vs %#v", second, first)
	}
}

func TestValidateStaged_EmptyModeIsGenerated(t *testing.T) {
	got, err := ValidateStaged(&types.AgentAppearance{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != types.AppearanceModeGenerated || got.Generated == nil || got.Character != nil {
		t.Fatalf("got %#v, want the generated default", got)
	}
}

func TestValidateStaged_Refusals(t *testing.T) {
	id, version := assignableCharacter(t)
	cat, err := charactercatalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		req  *types.AgentAppearance
		want string
	}{
		"an upload cannot be staged": {
			req:  &types.AgentAppearance{Uploaded: &types.UploadedAppearance{Image: "x.png"}},
			want: "upload",
		},
		"upload mode cannot be staged": {
			req:  &types.AgentAppearance{Mode: types.AppearanceModeUploaded},
			want: "upload",
		},
		"a version other than the server's is refused": {
			req:  &types.AgentAppearance{Character: &types.CharacterAppearance{CatalogID: id, CatalogVersion: version + 1}},
			want: "server-managed",
		},
		"a version without a character is refused": {
			req:  &types.AgentAppearance{Character: &types.CharacterAppearance{CatalogVersion: 1}},
			want: "server-managed",
		},
		"the guide's character is reserved": {
			req:  &types.AgentAppearance{Character: &types.CharacterAppearance{CatalogID: string(cat.ReservedGuideID)}},
			want: "reserved",
		},
		"an unknown character is refused": {
			req:  &types.AgentAppearance{Character: &types.CharacterAppearance{CatalogID: "no-such-character"}},
			want: "unknown character",
		},
		"character mode needs a character": {
			req:  &types.AgentAppearance{Mode: types.AppearanceModeCharacter},
			want: "requires a character",
		},
		"a bad colour is refused": {
			req:  &types.AgentAppearance{Generated: &types.GeneratedAppearance{Color: "blue"}},
			want: "generated.color",
		},
		"an unknown mode is refused": {
			req:  &types.AgentAppearance{Mode: "hologram"},
			want: "unknown appearance mode",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ValidateStaged(tc.req)
			if err == nil {
				t.Fatalf("accepted %#v as %#v", tc.req, got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}
