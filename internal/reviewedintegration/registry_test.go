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
	specialistEntry, ok := specialist.Get("music_production")
	if !ok || specialistEntry.IntegrationKey != entry.Key {
		t.Fatalf("music specialist does not name the reviewed integration: %#v", specialistEntry)
	}
	if entry.ExpectedBlueprintID != "reaper-song" || entry.ExpectedProgramID != "music-producer-assistant" ||
		entry.ExpectedBlueprintID != specialistEntry.SuggestedTemplateID {
		t.Fatalf("registry/specialist identity drift: %#v / %#v", entry, specialistEntry)
	}
	if entry.ExpectedVersion != "0.6.0" || entry.ExpectedBlueprintVersion != 7 ||
		entry.ExpectedProgramSchema != 2 || entry.ExpectedProtocol != plugin.SurfaceProtocolVersion {
		t.Fatalf("reviewed candidate versions drifted: %#v", entry)
	}
	if entry.SourceCommit != "03af9fda3e6b9d8cc3c0496c5e9ef6df99e870b9" {
		t.Fatalf("reviewed candidate commit drifted: %q", entry.SourceCommit)
	}
	if !entry.ReleaseReady || entry.Source() != entry.SourceRepository+"#sha="+entry.SourceCommit {
		t.Fatalf("published release missing immutable install source: ready=%v source=%q", entry.ReleaseReady, entry.Source())
	}
	// The pin must require exactly what the v0.6.0 manifest declares: a
	// narrower list would accept a plugin this host cannot honor, a wider one
	// would refuse the published release.
	expectedFeatures := []string{
		plugin.HostFeatureAssistantProgramV1,
		plugin.HostFeatureSetupQuestsV2,
		plugin.HostFeatureSpecialistSetupJourneyV1,
		plugin.HostFeatureTemplateGroupRequirementsV1,
	}
	if features := strings.Join(entry.RequiredHostFeatures, ","); features != strings.Join(expectedFeatures, ",") {
		t.Errorf("required host features = %q, want %q", features, strings.Join(expectedFeatures, ","))
	}
}

// FR 19: every specialist's integration key names a reviewed integration. The
// specialist package cannot import this one, so the check lives here.
func TestEverySpecialistIntegrationKeyIsReviewed(t *testing.T) {
	keyed := 0
	for _, entry := range specialist.All() {
		if entry.IntegrationKey == "" {
			continue
		}
		keyed++
		if _, ok := Get(entry.IntegrationKey); !ok {
			t.Errorf("specialist %q names unreviewed integration %q", entry.Slug, entry.IntegrationKey)
		}
	}
	if keyed == 0 {
		t.Fatal("no specialist names a reviewed integration")
	}
	reaper, _ := Get("ori_reaper")
	if reaper.InstallQuestID() != "install_ori_reaper" {
		t.Fatalf("install quest id = %q", reaper.InstallQuestID())
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
