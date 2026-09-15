package reviewedintegration

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/specialist"
)

func TestBuiltInRegistryMatchesSpecialistConstraintsAndPublishedRelease(t *testing.T) {
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
	if entry.SourceCommit != "1f494db5a39d8c13f6149943b28e6a506d19631a" {
		t.Fatalf("reviewed candidate commit drifted: %q", entry.SourceCommit)
	}
	if !entry.ReleaseReady || entry.Source() != entry.SourceRepository+"#sha="+entry.SourceCommit {
		t.Fatalf("published release missing immutable install source: ready=%v source=%q", entry.ReleaseReady, entry.Source())
	}
	features := strings.Join(entry.RequiredHostFeatures, ",")
	for _, required := range []string{plugin.HostFeatureAssistantProgramV1, plugin.HostFeatureSpecialistSetupJourneyV1} {
		if !strings.Contains(features, required) {
			t.Errorf("required host feature %q missing from %q", required, features)
		}
	}
}

func TestBuiltInRegistryCarriesInstallQuestCopy(t *testing.T) {
	entry, ok := Get("ori_reaper")
	if !ok {
		t.Fatal("reviewed REAPER integration is missing")
	}
	const explanation = "Ori's REAPER integration is a local integration for Ori, not an audio plug-in, VST, effect, or instrument. It will not appear in REAPER's FX browser."
	if entry.DisplayName != "REAPER" || entry.InstallTitle != "Install Ori REAPER Plugin" || entry.InstallDescription != explanation {
		t.Fatalf("install copy = %q / %q / %q", entry.DisplayName, entry.InstallTitle, entry.InstallDescription)
	}
}

func TestRegistryNormalizationRejectsUnsafeDisplayCopy(t *testing.T) {
	base, _ := Get("ori_reaper")
	cases := map[string]func(*Entry){
		"empty display name":        func(entry *Entry) { entry.DisplayName = "  " },
		"empty install title":       func(entry *Entry) { entry.InstallTitle = "" },
		"empty install description": func(entry *Entry) { entry.InstallDescription = "" },
		"markup display name":       func(entry *Entry) { entry.DisplayName = "<b>REAPER</b>" },
		"url install title":         func(entry *Entry) { entry.InstallTitle = "Install from https://example.invalid" },
		"markdown link description": func(entry *Entry) { entry.InstallDescription = "See [docs](somewhere)." },
		"control character":         func(entry *Entry) { entry.InstallDescription = "Install\x07now." },
		"format character":          func(entry *Entry) { entry.DisplayName = "REA" + string(rune(0x202E)) + "PER" },
		"multi-line display name":   func(entry *Entry) { entry.DisplayName = "REA\nPER" },
		"multi-line install title":  func(entry *Entry) { entry.InstallTitle = "Install\tplugin" },
		"long display name":         func(entry *Entry) { entry.DisplayName = strings.Repeat("R", MaxDisplayNameBytes+1) },
		"long install title":        func(entry *Entry) { entry.InstallTitle = strings.Repeat("I", MaxInstallTitleBytes+1) },
		"long install description":  func(entry *Entry) { entry.InstallDescription = strings.Repeat("d", MaxInstallDescriptionBytes+1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			entry := base.Clone()
			mutate(&entry)
			if _, err := normalize(entry); err == nil {
				t.Fatal("unsafe display copy was accepted")
			}
		})
	}
	trimmed := base.Clone()
	trimmed.DisplayName = "  REAPER  "
	if normalized, err := normalize(trimmed); err != nil || normalized.DisplayName != "REAPER" {
		t.Fatalf("trimmed display name = %q, err = %v", normalized.DisplayName, err)
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
