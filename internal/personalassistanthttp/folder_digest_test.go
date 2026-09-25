package personalassistanthttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type fakeFolderDigest struct {
	chips     []string
	picks     int
	decisions map[string]personalassistant.FolderOfferView
	decided   []personalassistant.FolderDecisionInput
	paused    bool
	cancel    bool
}

func (f *fakeFolderDigest) Current(context.Context, string) (personalassistant.FolderDigestView, error) {
	return personalassistant.FolderDigestView{
		Chips:      []personalassistant.FolderChip{{ID: "downloads", Label: "Downloads"}},
		PickerNote: "Pick a folder from the list for now.",
		Paused:     f.paused,
	}, nil
}

func (f *fakeFolderDigest) ScanChip(_ context.Context, _ string, chip string) (personalassistant.FolderOfferView, error) {
	f.chips = append(f.chips, chip)
	if chip != "downloads" {
		return personalassistant.FolderOfferView{}, personalassistant.ErrFolderChipUnknown
	}
	return personalassistant.FolderOfferView{ID: "offer-1", Verdict: "dump", Folder: "Downloads", Reason: "25 loose files of 6 kinds", Remember: !f.paused}, nil
}

func (f *fakeFolderDigest) ScanPicked(context.Context, string) (*personalassistant.FolderOfferView, error) {
	f.picks++
	if f.cancel {
		return nil, nil
	}
	return &personalassistant.FolderOfferView{ID: "offer-2", Verdict: "project", Folder: "Thesis"}, nil
}

func (f *fakeFolderDigest) Decide(_ context.Context, _ string, offerID string, input personalassistant.FolderDecisionInput) (personalassistant.FolderOfferView, error) {
	if f.decisions == nil {
		f.decisions = map[string]personalassistant.FolderOfferView{}
	}
	// The real service returns the stored result for a replayed request id;
	// the fake mirrors that so the handler's contract is exercised end to end.
	if stored, ok := f.decisions[input.RequestID]; ok {
		return stored, nil
	}
	f.decided = append(f.decided, input)
	view := personalassistant.FolderOfferView{ID: offerID, Status: personalassistant.FolderOfferDeclined, Decision: input.Decision}
	f.decisions[input.RequestID] = view
	return view, nil
}

func (f *fakeFolderDigest) Resolve(_ context.Context, _ string, offerID string, input personalassistant.FolderResolveInput) (personalassistant.FolderOfferView, error) {
	if input.WorkspaceID == "foreign" {
		return personalassistant.FolderOfferView{}, personalassistant.ErrFolderWorkspaceRefused
	}
	return personalassistant.FolderOfferView{
		ID: offerID, Status: personalassistant.FolderOfferResolved,
		Outcome: &personalassistant.FolderOutcome{Kind: "project", WorkspaceID: input.WorkspaceID, Route: "/workspaces/thesis"},
	}, nil
}

func newFolderDigestHandler(fake *fakeFolderDigest) *Handler {
	h := NewHandler(nil, userprofile.LocalUserProvider{})
	h.SetFolderDigest(fake)
	return h
}

func TestFolderDigestScan_AcceptsChipsOnly(t *testing.T) {
	fake := &fakeFolderDigest{}
	h := newFolderDigestHandler(fake)
	for _, scenario := range []struct {
		body   string
		status int
		leak   string
	}{
		{`{"chip":"downloads"}`, http.StatusOK, ""},
		{`{"chip":"pictures"}`, http.StatusBadRequest, ""},
		{`{"path":"/Users/me/Secrets"}`, http.StatusBadRequest, "/Users/me"},
		{`{"chip":"downloads","folder_path":"/Users/me/Secrets"}`, http.StatusBadRequest, "/Users/me"},
		{`{"chip":"downloads","picker":true}`, http.StatusBadRequest, ""},
		{`{}`, http.StatusBadRequest, ""},
		{`not json`, http.StatusBadRequest, ""},
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/folder-digest/scan", strings.NewReader(scenario.body))
		response := httptest.NewRecorder()
		h.ScanFolderDigest(response, request)
		if response.Code != scenario.status {
			t.Fatalf("%s: status=%d body=%s", scenario.body, response.Code, response.Body.String())
		}
		if scenario.leak != "" && strings.Contains(response.Body.String(), scenario.leak) {
			t.Fatalf("%s: echoed the path: %s", scenario.body, response.Body.String())
		}
	}
	if len(fake.chips) != 2 || fake.chips[0] != "downloads" || fake.chips[1] != "pictures" {
		t.Fatalf("service saw chips %v; a body carrying a path must never reach it", fake.chips)
	}
	response := httptest.NewRecorder()
	h.ScanFolderDigest(response, httptest.NewRequest(http.MethodGet, "/scan", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET scan status=%d", response.Code)
	}
}

func TestFolderDigestScan_PathRefusalNamesTheRule(t *testing.T) {
	h := newFolderDigestHandler(&fakeFolderDigest{})
	response := httptest.NewRecorder()
	h.ScanFolderDigest(response, httptest.NewRequest(http.MethodPost, "/scan", strings.NewReader(`{"path":"/tmp"}`)))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "does not accept a folder path") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFolderDigestPicker_CancelIsCleanAndBodyIsRefused(t *testing.T) {
	fake := &fakeFolderDigest{cancel: true}
	h := newFolderDigestHandler(fake)
	response := httptest.NewRecorder()
	h.PickFolderDigest(response, httptest.NewRequest(http.MethodPost, "/picker", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cancelled":true`) {
		t.Fatalf("cancel status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	h.PickFolderDigest(response, httptest.NewRequest(http.MethodPost, "/picker", strings.NewReader(`{"path":"/tmp"}`)))
	if response.Code != http.StatusBadRequest || fake.picks != 1 {
		t.Fatalf("picker with body status=%d picks=%d", response.Code, fake.picks)
	}
	fake.cancel = false
	response = httptest.NewRecorder()
	h.ScanFolderDigest(response, httptest.NewRequest(http.MethodPost, "/scan", strings.NewReader(`{"picker":true}`)))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"Thesis"`) || fake.picks != 2 {
		t.Fatalf("scan picker status=%d picks=%d body=%s", response.Code, fake.picks, response.Body.String())
	}
}

func TestFolderDigestDecide_ReplayReturnsStoredResult(t *testing.T) {
	fake := &fakeFolderDigest{}
	h := newFolderDigestHandler(fake)
	send := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/folder-digest/offers/offer-1/decide", strings.NewReader(body))
		request.SetPathValue("offerID", "offer-1")
		response := httptest.NewRecorder()
		h.DecideFolderDigest(response, request)
		return response
	}
	first := send(`{"decision":"no","request_id":"req-1"}`)
	second := send(`{"decision":"no","request_id":"req-1"}`)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("statuses %d %d", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay differed:\n%s\n%s", first.Body.String(), second.Body.String())
	}
	if len(fake.decided) != 1 {
		t.Fatalf("decision applied %d times", len(fake.decided))
	}
	var payload struct {
		Offer personalassistant.FolderOfferView `json:"offer"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &payload); err != nil || payload.Offer.ID != "offer-1" || payload.Offer.Decision != "no" {
		t.Fatalf("payload=%s err=%v", first.Body.String(), err)
	}
	if response := send(`{"decision":"no","request_id":"req-2","path":"/tmp"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("decide with path status=%d", response.Code)
	}
}

// The card's confirmed plan reaches the service as a create; a decide without
// it is the modal path, unchanged.
func TestFolderDigestDecide_CarriesTheCreateFlag(t *testing.T) {
	fake := &fakeFolderDigest{}
	h := newFolderDigestHandler(fake)
	send := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/folder-digest/offers/offer-1/decide", strings.NewReader(body))
		request.SetPathValue("offerID", "offer-1")
		response := httptest.NewRecorder()
		h.DecideFolderDigest(response, request)
		return response
	}
	if response := send(`{"decision":"yes","choice":"project","create":true,"request_id":"req-1"}`); response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response := send(`{"decision":"yes","choice":"project","request_id":"req-2"}`); response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(fake.decided) != 2 || !fake.decided[0].Create || fake.decided[1].Create {
		t.Fatalf("decisions=%+v", fake.decided)
	}
	if response := send(`{"decision":"yes","choice":"project","create":"yes","request_id":"req-3"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("create with the wrong type status=%d", response.Code)
	}
}

func TestFolderDigestResolve_AcceptsWorkspaceIDOnly(t *testing.T) {
	h := newFolderDigestHandler(&fakeFolderDigest{})
	send := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/folder-digest/offers/offer-1/resolve", strings.NewReader(body))
		request.SetPathValue("offerID", "offer-1")
		response := httptest.NewRecorder()
		h.ResolveFolderDigest(response, request)
		return response
	}
	ok := send(`{"workspace_id":"ws-1","request_id":"req-1"}`)
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `"/workspaces/thesis"`) {
		t.Fatalf("resolve status=%d body=%s", ok.Code, ok.Body.String())
	}
	if response := send(`{"workspace_id":"ws-1","request_id":"req-1","path":"/Users/me/Thesis"}`); response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "/Users/me") {
		t.Fatalf("resolve with path status=%d body=%s", response.Code, response.Body.String())
	}
	if response := send(`{"workspace_id":"foreign","request_id":"req-2"}`); response.Code != http.StatusConflict {
		t.Fatalf("foreign workspace status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFolderDigestGet_PausedStillOffersTheChooser(t *testing.T) {
	fake := &fakeFolderDigest{paused: true}
	h := newFolderDigestHandler(fake)
	response := httptest.NewRecorder()
	h.GetFolderDigest(response, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/folder-digest", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		View personalassistant.FolderDigestView `json:"folder_digest"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.View.Paused || len(payload.View.Chips) != 1 || payload.View.PickerAvailable || payload.View.PickerNote == "" {
		t.Fatalf("view=%+v", payload.View)
	}
	response = httptest.NewRecorder()
	h.ScanFolderDigest(response, httptest.NewRequest(http.MethodPost, "/scan", strings.NewReader(`{"chip":"downloads"}`)))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"remember":true`) {
		t.Fatalf("paused scan status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFolderDigestErrors_MapWithoutLeaking(t *testing.T) {
	for _, scenario := range []struct {
		err    error
		status int
		text   string
	}{
		{&personalassistant.FolderRootError{Message: "That is your whole home folder. Choose one folder inside it, such as Downloads or Desktop."}, http.StatusBadRequest, "choose_folder"},
		{personalassistant.ErrFolderScanBusy, http.StatusConflict, "still looking"},
		{personalassistant.ErrFolderPathLost, http.StatusConflict, `"needs_pick":true`},
		{personalassistant.ErrFolderOfferNotFound, http.StatusNotFound, "no longer here"},
		{personalassistant.ErrFolderPickerUnavailable, http.StatusConflict, "unavailable here"},
		{personalassistant.ErrNeedsHQ, http.StatusConflict, "Personal HQ"},
		{personalassistant.ErrRepairNeeded, http.StatusConflict, "relationship changed"},
		{context.DeadlineExceeded, http.StatusServiceUnavailable, "temporarily unavailable"},
	} {
		response := httptest.NewRecorder()
		writeFolderDigestError(response, scenario.err)
		if response.Code != scenario.status || !strings.Contains(response.Body.String(), scenario.text) {
			t.Errorf("%v: status=%d body=%s", scenario.err, response.Code, response.Body.String())
		}
	}
}
