package personalassistanthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type fakeFolderHomeStaffing struct {
	*fakeFolderDigest
	inputs          []personalassistant.FolderHomeStaffingInput
	userID, offerID string
}

func (f *fakeFolderHomeStaffing) ReviewHomeStaffing(_ context.Context, userID, offerID string, input personalassistant.FolderHomeStaffingInput) (personalassistant.FolderOfferView, error) {
	f.inputs = append(f.inputs, input)
	f.userID, f.offerID = userID, offerID
	return personalassistant.FolderOfferView{ID: offerID, Status: personalassistant.FolderOfferPending}, nil
}

func TestFolderHomeStaffingUsesExistingReviewAdjustWithClosedPublicInput(t *testing.T) {
	fake := &fakeFolderHomeStaffing{fakeFolderDigest: &fakeFolderDigest{}}
	h := newFolderDigestHandler(fake.fakeFolderDigest)
	h.SetFolderDigest(fake)
	post := func(body string, adjust bool) int {
		r := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/folder-digest/offers/offer-1/review", strings.NewReader(body))
		r.SetPathValue("offerID", "offer-1")
		w := httptest.NewRecorder()
		if adjust {
			h.AdjustFolderSoftware(w, r)
		} else {
			h.ReviewFolderSoftware(w, r)
		}
		return w.Code
	}
	if code := post(`{"request_id":"recommend","home_staffing":{"action":"recommend_staffing","context_digest":"`+strings.Repeat("c", 64)+`","staffing_revision":0}}`, false); code != http.StatusOK {
		t.Fatal(code)
	}
	if code := post(`{"request_id":"choose","home_staffing":{"action":"choose_staffing","context_digest":"`+strings.Repeat("c", 64)+`","staffing_revision":1,"choice":{"role_id":"manager","mode":"assign","name":"Aster"}}}`, true); code != http.StatusOK {
		t.Fatal(code)
	}
	if len(fake.inputs) != 2 || fake.inputs[0].RequestID != "recommend" || fake.inputs[1].RequestID != "choose" || fake.userID != "local" || fake.offerID != "offer-1" || len(fake.reviews) != 0 || len(fake.setups) != 0 {
		t.Fatal("staffing reached another owner/action")
	}
	for name, payload := range map[string]string{
		"private source":     `{"action":"choose_staffing","choice":{"role_id":"manager","mode":"assign","name":"Aster","source_witness":"injected"}}`,
		"private capability": `{"action":"choose_staffing","choice":{"role_id":"manager","mode":"assign","name":"Aster","capability_witness":"injected"}}`,
		"prompt":             `{"action":"choose_staffing","choice":{"role_id":"manager","mode":"create","name":"Aster","system_prompt":"injected"}}`,
		"owner":              `{"action":"choose_staffing","owner_user_id":"foreign"}`,
		"path":               `{"action":"choose_staffing","path":"/private"}`,
		"execution":          `{"action":"choose_staffing","plan_digest":"injected"}`,
		"nested request":     `{"action":"clear_staffing","request_id":"other"}`,
		"null":               `null`,
	} {
		t.Run(name, func(t *testing.T) {
			if code := post(`{"request_id":"bad","home_staffing":`+payload+`}`, true); code != http.StatusBadRequest {
				t.Fatal(code)
			}
		})
	}
	if code := post(`{"request_id":"mixed","workspace_name":"Other","home_staffing":{"action":"clear_staffing"}}`, true); code != http.StatusBadRequest {
		t.Fatal("mixed owners accepted", code)
	}
	if code := post(`{"request_id":"wrong-route","home_staffing":{"action":"recommend_staffing"}}`, true); code != http.StatusBadRequest {
		t.Fatal("adjust inferred", code)
	}
	if len(fake.inputs) != 2 {
		t.Fatal("rejected public input reached staffing")
	}
}
