package sessionhttp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
)

func eligibility(state, format string) projectlibrary.ActivationEligibility {
	return projectlibrary.ActivationEligibility{EntryID: "song", State: state, ObservedFormat: format}
}

func TestLibraryIntegrationOffer_OnlyWhenTheIntegrationIsTheHonestRemedy(t *testing.T) {
	const supported = "darwin/arm64"
	offer := libraryIntegrationOfferFor(eligibility("project_provider_unavailable", "reaper"), supported)
	if offer == nil || offer.Key != "ori_reaper" || offer.QuestID != "install_ori_reaper" || offer.DisplayName != "REAPER" {
		t.Fatalf("reaper offer = %+v", offer)
	}

	// Every other blocker stays its own blocker: installing REAPER is not a cure.
	for _, state := range []string{
		"revoked_source", "unavailable", "home_provider_unavailable", "provider_ambiguous",
		"unsupported_format", "folder_owned", "link_needs_review", "connected",
		"review_available", "file_choice_required", "",
	} {
		if got := libraryIntegrationOfferFor(eligibility(state, "reaper"), supported); got != nil {
			t.Fatalf("%q was offered an install: %+v", state, got)
		}
	}

	// A format no reviewed integration supports, or none at all, is never offered one.
	for _, format := range []string{"logic", "ableton", "", "REAPER", "unknown"} {
		if got := libraryIntegrationOfferFor(eligibility("project_provider_unavailable", format), supported); got != nil {
			t.Fatalf("format %q was offered an install: %+v", format, got)
		}
	}

	// An unsupported platform is not offered an install it cannot complete.
	for _, platform := range []string{"linux/amd64", "windows/amd64", "darwin/amd64", ""} {
		if got := libraryIntegrationOfferFor(eligibility("project_provider_unavailable", "reaper"), platform); got != nil {
			t.Fatalf("platform %q was offered an install: %+v", platform, got)
		}
	}
}

func TestLibraryActivationResponse_KeepsEligibilityFieldsAndAddsTheOfferOnlyWhenPresent(t *testing.T) {
	with, err := json.Marshal(libraryActivationResponse{
		ActivationEligibility: eligibility("project_provider_unavailable", "reaper"),
		IntegrationOffer:      libraryIntegrationOfferFor(eligibility("project_provider_unavailable", "reaper"), "darwin/arm64"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(with, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["entry_id"] != "song" || decoded["state"] != "project_provider_unavailable" || decoded["observed_format"] != "reaper" {
		t.Fatalf("eligibility fields were lost by embedding: %s", with)
	}
	offer, _ := decoded["integration_offer"].(map[string]any)
	if offer["quest_id"] != "install_ori_reaper" || offer["key"] != "ori_reaper" {
		t.Fatalf("offer = %s", with)
	}
	if strings.Contains(string(with), "/Users/") || strings.Contains(string(with), ".rpp") {
		t.Fatalf("offer leaked a path or file name: %s", with)
	}

	without, err := json.Marshal(libraryActivationResponse{ActivationEligibility: eligibility("revoked_source", "")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(without), "integration_offer") || strings.Contains(string(without), "observed_format") {
		t.Fatalf("absent offer/format must be omitted: %s", without)
	}
}
