package personalassistanthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

const interviewSuggestRoute = "/api/personal-assistant/knowledge/interview/suggest"

// interviewSuggestFake is a folder digest holding one project offer for the
// Documents chip and one dump offer for Downloads.
func interviewSuggestFake() *fakeFolderDigest {
	key := strings.Repeat("a", 64)
	return &fakeFolderDigest{stored: map[string]personalassistant.FolderOffer{
		"offer-docs": {
			ID: "offer-docs", Status: personalassistant.FolderOfferPending, Verdict: "mixed",
			FolderKey: key, FolderName: "Documents",
			Subject: personalassistant.FolderCandidateRecord{
				Key: key, Name: "Thesis", Kind: personalassistant.FolderChoiceProject, Marker: "LaTeX manuscript", RelPath: "Thesis",
			},
		},
		"offer-1": {
			ID: "offer-1", Status: personalassistant.FolderOfferPending, Verdict: "dump",
			FolderKey: key, FolderName: "Downloads",
			Subject: personalassistant.FolderCandidateRecord{Key: key, Name: "Downloads", Kind: personalassistant.FolderChoiceTidy},
		},
		// The fake's picked folder and picked file.
		"offer-2": {
			ID: "offer-2", Status: personalassistant.FolderOfferPending, Verdict: "project",
			FolderKey: key, FolderName: "Thesis",
			Subject: personalassistant.FolderCandidateRecord{Key: key, Name: "Thesis", Kind: personalassistant.FolderChoiceProject, Marker: "LaTeX manuscript", IsRoot: true},
			Queue: []personalassistant.FolderCandidateRecord{
				{Key: key, Name: "appendix", Kind: personalassistant.FolderChoiceProject, Marker: "outline", RelPath: "appendix"},
			},
		},
		"offer-3": {
			ID: "offer-3", Status: personalassistant.FolderOfferPending, Verdict: "project",
			FolderKey: key, FolderName: "Album", EntryName: "Song.wav",
			Subject: personalassistant.FolderCandidateRecord{Key: key, Name: "Album", Kind: personalassistant.FolderChoiceProject, IsRoot: true},
		},
	}}
}

func postInterviewSuggest(h *Handler, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.SuggestKnowledgeInterview(w, httptest.NewRequest(http.MethodPost, interviewSuggestRoute, strings.NewReader(body)))
	return w
}

type interviewSuggestResponse struct {
	Suggestion *personalassistant.InterviewSuggestion `json:"suggestion"`
	Message    string                                 `json:"message"`
	Cancelled  bool                                   `json:"cancelled"`
	Error      string                                 `json:"error"`
}

func decodeInterviewSuggest(t *testing.T, w *httptest.ResponseRecorder) interviewSuggestResponse {
	t.Helper()
	var body interviewSuggestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return body
}

func TestSuggestKnowledgeInterview_TakesOneModeAndNeverAPath(t *testing.T) {
	fake := interviewSuggestFake()
	h := newFolderDigestHandler(fake)
	for _, body := range []string{
		`{"path":"/Users/me/Documents"}`,
		`{"chip":"documents","path":"/Users/me/Documents"}`,
		`{"chip":"documents","folder":"Thesis"}`,
		`{"chip":"documents","picker":true}`,
		`{"picker":true,"file":true}`,
		`{"chip":"documents","text":"anything"}`,
		`{"chip":"  "}`,
		`{}`,
		``,
		`[]`,
	} {
		w := postInterviewSuggest(h, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%q => %d %s, want 400", body, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "/Users/me") {
			t.Fatalf("%q echoed the path: %s", body, w.Body.String())
		}
	}
	if len(fake.chips) != 0 || fake.picks != 0 || fake.filePicks != 0 || len(fake.storedReads) != 0 {
		t.Fatalf("a refused request reached the service: chips=%v picks=%d files=%d reads=%v", fake.chips, fake.picks, fake.filePicks, fake.storedReads)
	}

	w := httptest.NewRecorder()
	h.SuggestKnowledgeInterview(w, httptest.NewRequest(http.MethodGet, interviewSuggestRoute, nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET => %d, want 405", w.Code)
	}
	unwired := httptest.NewRecorder()
	NewHandler(nil, nil).SuggestKnowledgeInterview(unwired, httptest.NewRequest(http.MethodPost, interviewSuggestRoute, strings.NewReader(`{"chip":"documents"}`)))
	if unwired.Code != http.StatusServiceUnavailable {
		t.Fatalf("no folder digest => %d, want 503", unwired.Code)
	}
}

func TestSuggestKnowledgeInterview_ProjectChipProposesTheAnswer(t *testing.T) {
	fake := interviewSuggestFake()
	h := newFolderDigestHandler(fake)
	w := postInterviewSuggest(h, `{"chip":" documents "}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := decodeInterviewSuggest(t, w)
	if body.Suggestion == nil || body.Suggestion.Text != "Thesis, a LaTeX manuscript" || body.Suggestion.Folder != "Thesis" || body.Message != "" {
		t.Fatalf("body = %s", w.Body.String())
	}
	if len(fake.chips) != 1 || fake.chips[0] != "documents" || len(fake.storedReads) != 1 || fake.storedReads[0] != "offer-docs" {
		t.Fatalf("chips=%v reads=%v", fake.chips, fake.storedReads)
	}
	// Only the wording leaves the server: no offer, key, or path field.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 || strings.Contains(w.Body.String(), strings.Repeat("a", 64)) || strings.Contains(w.Body.String(), "rel_path") {
		t.Fatalf("response carries more than the suggestion: %s", w.Body.String())
	}
}

func TestSuggestKnowledgeInterview_DumpChipSaysItCouldNotTell(t *testing.T) {
	fake := interviewSuggestFake()
	h := newFolderDigestHandler(fake)
	w := postInterviewSuggest(h, `{"chip":"downloads"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"suggestion":null`) {
		t.Fatalf("suggestion must be an explicit null: %s", w.Body.String())
	}
	body := decodeInterviewSuggest(t, w)
	if body.Suggestion != nil || body.Message != "I couldn't tell what this folder is for. Type your answer instead." {
		t.Fatalf("body = %s", w.Body.String())
	}
}

// A scan that worked but whose offer is already gone (an empty folder's closed
// offer can be pruned in the same write) is "nothing to propose", not an error.
func TestSuggestKnowledgeInterview_PrunedOfferIsNotAFailedLook(t *testing.T) {
	fake := interviewSuggestFake()
	delete(fake.stored, "offer-1")
	w := postInterviewSuggest(newFolderDigestHandler(fake), `{"chip":"downloads"}`)
	body := decodeInterviewSuggest(t, w)
	if w.Code != http.StatusOK || body.Suggestion != nil || body.Message != "I couldn't tell what this folder is for. Type your answer instead." {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(fake.storedReads) != 1 {
		t.Fatalf("reads = %v", fake.storedReads)
	}
}

func TestSuggestKnowledgeInterview_PickerAndFileModes(t *testing.T) {
	t.Run("a picked folder proposes the answer and its alternates", func(t *testing.T) {
		fake := interviewSuggestFake()
		w := postInterviewSuggest(newFolderDigestHandler(fake), `{"picker":true}`)
		body := decodeInterviewSuggest(t, w)
		if w.Code != http.StatusOK || body.Suggestion == nil || body.Suggestion.Text != "Thesis, a LaTeX manuscript" || body.Cancelled {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if len(body.Suggestion.Alternates) != 1 || body.Suggestion.Alternates[0].Text != "appendix, an outline" || body.Suggestion.Alternates[0].Folder != "appendix" {
			t.Fatalf("alternates = %+v", body.Suggestion.Alternates)
		}
		if fake.picks != 1 || fake.filePicks != 0 || len(fake.chips) != 0 || len(fake.storedReads) != 1 || fake.storedReads[0] != "offer-2" {
			t.Fatalf("picks=%d files=%d chips=%v reads=%v", fake.picks, fake.filePicks, fake.chips, fake.storedReads)
		}
	})
	t.Run("a picked file proposes its folder and never names the file", func(t *testing.T) {
		fake := interviewSuggestFake()
		w := postInterviewSuggest(newFolderDigestHandler(fake), `{"file":true}`)
		body := decodeInterviewSuggest(t, w)
		if w.Code != http.StatusOK || body.Suggestion == nil || body.Suggestion.Text != "Album" || body.Suggestion.Folder != "Album" {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "Song.wav") || fake.filePicks != 1 || fake.picks != 0 {
			t.Fatalf("body=%s files=%d picks=%d", w.Body.String(), fake.filePicks, fake.picks)
		}
	})
	for _, mode := range []string{`{"picker":true}`, `{"file":true}`} {
		t.Run("a cancelled dialog changes nothing "+mode, func(t *testing.T) {
			fake := interviewSuggestFake()
			fake.cancel = true
			w := postInterviewSuggest(newFolderDigestHandler(fake), mode)
			if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"cancelled":true}` {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(fake.storedReads) != 0 {
				t.Fatalf("a cancelled dialog read an offer: %v", fake.storedReads)
			}
		})
		t.Run("an unavailable dialog is refused "+mode, func(t *testing.T) {
			fake := interviewSuggestFake()
			fake.scanErr = personalassistant.ErrFolderPickerUnavailable
			w := postInterviewSuggest(newFolderDigestHandler(fake), mode)
			body := decodeInterviewSuggest(t, w)
			if w.Code != http.StatusConflict || body.Message != "The folder dialog is unavailable here. Pick a folder from the list" || body.Suggestion != nil {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestSuggestKnowledgeInterview_ScanErrorsReadLikeHome(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{"unknown chip", personalassistant.ErrFolderChipUnknown, http.StatusBadRequest, "That is not one of the folders Ori can look at"},
		{"missing chip folder", personalassistant.ErrFolderChipMissing, http.StatusBadRequest, "That folder is not on this computer"},
		// The one refusal the wizard words itself.
		{"scan in progress", personalassistant.ErrFolderScanBusy, http.StatusConflict, "I'm still looking at the last folder. Try again in a moment."},
		{"picker switched off", personalassistant.ErrFolderPickerUnavailable, http.StatusConflict, "The folder dialog is unavailable here. Pick a folder from the list"},
		{"no Personal HQ", personalassistant.ErrNeedsHQ, http.StatusConflict, "Build Personal HQ before showing a folder"},
		{"unreadable folder", &personalassistant.FolderRootError{Message: "Ori is not allowed to look inside that folder. Choose a different folder."}, http.StatusBadRequest, "Ori is not allowed to look inside that folder. Choose a different folder."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := interviewSuggestFake()
			fake.scanErr = tc.err
			w := postInterviewSuggest(newFolderDigestHandler(fake), `{"chip":"documents"}`)
			body := decodeInterviewSuggest(t, w)
			// A folder the scan refuses answers {"error": …}; every other
			// refusal is the API error shape {"code": …, "message": …}.
			said := body.Error
			if said == "" {
				said = body.Message
			}
			if w.Code != tc.status || said != tc.message || body.Suggestion != nil {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(fake.storedReads) != 0 {
				t.Fatalf("a failed scan must not read an offer: %v", fake.storedReads)
			}
		})
	}
}
