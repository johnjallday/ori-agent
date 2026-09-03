package onboardinghttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/onboarding/detector"
)

func TestMatchSpecialistReturnsTheOneDomainOffer(t *testing.T) {
	entry := MatchSpecialist([]detector.DetectedApp{
		{Name: "Safari", LastUsed: time.Now()},
		{Name: "REAPER", LastUsed: time.Now().Add(-time.Hour)},
	})
	if entry == nil {
		t.Fatal("expected a specialist for a detected REAPER install")
	}
	if entry.Slug != "music_production" {
		t.Fatalf("specialist slug = %q", entry.Slug)
	}
	if entry.OfferCopy.Headline == "" || entry.OfferCopy.Question == "" {
		t.Fatal("the offer must carry its own copy so the wizard hardcodes none")
	}
}

// No match, an empty scan, and a nil scan are all the generic flow. None of
// them is an error, and none of them may produce an offer.
func TestMatchSpecialistReturnsNilWhenNothingMatches(t *testing.T) {
	cases := map[string][]detector.DetectedApp{
		"nil":      nil,
		"empty":    {},
		"no match": {{Name: "Safari", LastUsed: time.Now()}, {Name: "Slack", LastUsed: time.Now()}},
	}
	for name, apps := range cases {
		if entry := MatchSpecialist(apps); entry != nil {
			t.Fatalf("%s: expected no specialist, got %q", name, entry.Slug)
		}
	}
}

func TestSpecialistsEndpointServesTheBuiltInMapping(t *testing.T) {
	handler := &SmartOnboardingHandler{}
	recorder := httptest.NewRecorder()
	handler.Specialists(recorder, httptest.NewRequest(http.MethodGet, "/api/onboarding/specialists", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var payload struct {
		Success     bool `json:"success"`
		Specialists []struct {
			Slug      string `json:"slug"`
			OfferCopy struct {
				ManualLabel string `json:"manual_label"`
			} `json:"offer_copy"`
			SuggestedTemplateID string `json:"suggested_template_id"`
		} `json:"specialists"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.Success || len(payload.Specialists) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	entry := payload.Specialists[0]
	if entry.Slug != "music_production" || entry.SuggestedTemplateID != "reaper-song" {
		t.Fatalf("entry = %+v", entry)
	}
	// The manual route into a domain is what a producer on a second machine
	// uses; it must reach the client without waiting for a scan.
	if entry.OfferCopy.ManualLabel == "" {
		t.Fatal("expected a manual path label")
	}
}

func TestSpecialistsEndpointRejectsNonGET(t *testing.T) {
	handler := &SmartOnboardingHandler{}
	recorder := httptest.NewRecorder()
	handler.Specialists(recorder, httptest.NewRequest(http.MethodPost, "/api/onboarding/specialists", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
}
