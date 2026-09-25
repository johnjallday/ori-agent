package onboardinghttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
	// Historical accepted relationships still resolve their domain copy.
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
