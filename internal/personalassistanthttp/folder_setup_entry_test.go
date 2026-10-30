package personalassistanthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type fakeEntryChoiceDigest struct {
	fakeFolderDigest
	choices []personalassistant.FolderSetupEntryInput
}

func (f *fakeEntryChoiceDigest) ChooseSetupEntry(_ context.Context, _, id string, input personalassistant.FolderSetupEntryInput) (personalassistant.FolderOfferView, error) {
	f.choices = append(f.choices, input)
	offer := personalassistant.FolderOfferView{ID: id, Plan: &personalassistant.FolderSetupPlan{Digest: "fresh", EntryName: input.EntryName}}
	if input.PlanDigest == "old" {
		return offer, personalassistant.ErrFolderPlanChanged
	}
	return offer, nil
}

func TestFolderSetupEntry_HTTPAcceptsOnlyAChoiceAndItsPlan(t *testing.T) {
	fake := &fakeEntryChoiceDigest{}
	h := newFolderDigestHandler(&fake.fakeFolderDigest)
	h.SetFolderDigest(fake)
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/folder-digest/offers/offer-1/entry", strings.NewReader(body))
		r.SetPathValue("offerID", "offer-1")
		w := httptest.NewRecorder()
		h.ChooseFolderSetupEntry(w, r)
		return w
	}
	for _, body := range []string{
		`{"plan_digest":"current","entry_name":"Song.rpp","path":"/private"}`,
		`{"plan_digest":"current","entry_name":"Song.rpp","open":true}`,
		`{"plan_digest":"current","entry_name":"Song.rpp","request_id":"start"}`,
		`{"plan_digest":"current","entry_name":"Song.rpp","entry_name":"Other.rpp"}`,
		`{"plan_digest":"current","entry_name":"Song.rpp","entry_candidates":["Other.rpp"]}`,
		`{"plan_digest":"current","EntryName":"Song.rpp"}`,
		`{"plan_digest":"current","entry_name":null}`,
		`{"plan_digest":"current","entry_name":["Song.rpp"]}`,
		`{"plan_digest":"current","entry_name":"Song.rpp"} {}`,
		`{}`,
		`null`,
		`not json`,
	} {
		if result := post(body); result.Code != http.StatusBadRequest {
			t.Fatal(result.Code, result.Body.String())
		}
	}
	if len(fake.choices) != 0 {
		t.Fatal("bad input reached the service")
	}
	if result := post(`{"plan_digest":"current","entry_name":"Song.rpp"}`); result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"entry_name":"Song.rpp"`) {
		t.Fatal(result.Code, result.Body.String())
	}
	if len(fake.choices) != 1 || len(fake.setups) != 0 {
		t.Fatal("choice started setup or was not saved")
	}
	if result := post(`{"plan_digest":"old","entry_name":"Song.rpp"}`); result.Code != http.StatusConflict || !strings.Contains(result.Body.String(), `"plan_changed":true`) {
		t.Fatal(result.Code, result.Body.String())
	}
	w := httptest.NewRecorder()
	h.ChooseFolderSetupEntry(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
}
