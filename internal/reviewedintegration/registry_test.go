package reviewedintegration

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

func TestBuiltInRegistryMatchesSpecialistConstraintsAndStaysReleaseGated(t *testing.T) {
	entry, ok := Get(" ORI_REAPER ")
	if !ok {
		t.Fatal("reviewed REAPER integration is missing")
	}
	declarationEntry, ok := specialist.Get("music_production")
	if !ok || declarationEntry.SetupJourney == nil {
		t.Fatal("specialist declaration fixture is missing")
	}
	declaration := declarationEntry.SetupJourney
	if entry.Key != declaration.IntegrationKey ||
		entry.ExpectedBlueprintID != declaration.ExpectedBlueprintID ||
		entry.ExpectedProgramID != declaration.ExpectedAssistantProgramID {
		t.Fatalf("registry/declaration identity drift: %#v / %#v", entry, declaration)
	}
	if entry.ExpectedVersion != "0.5.0" || entry.ExpectedBlueprintVersion != 4 ||
		entry.ExpectedProgramSchema != 2 || entry.ExpectedProtocol != plugin.SurfaceProtocolVersion {
		t.Fatalf("reviewed candidate versions drifted: %#v", entry)
	}
	if entry.SourceCommit != "13d18c52a05025b8a54793a5b0844e72f1018fda" {
		t.Fatalf("reviewed candidate commit drifted: %q", entry.SourceCommit)
	}
	if entry.ReleaseReady || entry.Source() != "" {
		t.Fatalf("unpublished candidate exposed install source: ready=%v source=%q", entry.ReleaseReady, entry.Source())
	}
	features := strings.Join(entry.RequiredHostFeatures, ",")
	for _, required := range []string{plugin.HostFeatureAssistantProgramV1, plugin.HostFeatureSpecialistSetupJourneyV1} {
		if !strings.Contains(features, required) {
			t.Errorf("required host feature %q missing from %q", required, features)
		}
	}
}

func TestRegistryReturnsIndependentCopies(t *testing.T) {
	first, _ := Get("ori_reaper")
	first.RequiredHostFeatures[0] = "changed"
	first.SupportedPlatforms[0] = "changed"
	second, _ := Get("ori_reaper")
	if second.RequiredHostFeatures[0] == "changed" || second.SupportedPlatforms[0] == "changed" {
		t.Fatal("caller mutated built-in reviewed integration registry")
	}
}

func TestRegistryNormalizationRejectsMutableOrConfusedSources(t *testing.T) {
	base, _ := Get("ori_reaper")
	cases := map[string]func(*Entry){
		"ready without pin": func(entry *Entry) {
			entry.SourceCommit = ""
			entry.ReleaseReady = true
		},
		"malformed pin":       func(entry *Entry) { entry.SourceCommit = strings.Repeat("z", 40) },
		"mutable non-github":  func(entry *Entry) { entry.SourceRepository = "https://example.invalid/plugin" },
		"wrong source format": func(entry *Entry) { entry.SourceFormat = plugin.FormatCodex },
		"duplicate capability": func(entry *Entry) {
			entry.RequiredHostFeatures = []string{"assistant_program_v1", "assistant_program_v1"}
		},
		"unsafe platform": func(entry *Entry) { entry.SupportedPlatforms = []string{"../../darwin"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			entry := base.Clone()
			mutate(&entry)
			if _, err := normalize(entry); err == nil {
				t.Fatal("invalid reviewed integration entry was accepted")
			}
		})
	}
}
