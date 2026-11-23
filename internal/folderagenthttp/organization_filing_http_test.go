package folderagenthttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderagent"
)

func TestRespondFiling_ActionableRefusalsAreConflictsWithACode(t *testing.T) {
	preview := &filejanitor.DocumentsPreview{State: "changed"}
	for name, tc := range map[string]struct {
		result folderagent.FilingResult
		err    error
		status int
		code   string
	}{
		"the plan changed":    {folderagent.FilingResult{DocumentsFilingResult: filejanitor.DocumentsFilingResult{Preview: preview}}, filejanitor.ErrReviewPlanChanged, http.StatusConflict, "filing_changed"},
		"the key was reused":  {folderagent.FilingResult{}, filejanitor.ErrFilingKeyReused, http.StatusConflict, "filing_key_reused"},
		"unsupported here":    {folderagent.FilingResult{}, filejanitor.ErrMoveUnsupported, http.StatusConflict, "filing_unsupported"},
		"wrapped unsupported": {folderagent.FilingResult{}, errors.Join(errors.New("context"), filejanitor.ErrMoveUnsupported), http.StatusConflict, "filing_unsupported"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			respondFiling(rec, tc.result, tc.err)
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if rec.Code != tc.status || body["code"] != tc.code || body["error"] == "" {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "/Users/") {
				t.Fatalf("an answer must not carry a path: %s", rec.Body.String())
			}
		})
	}

	t.Run("the changed plan carries the fresh preview", func(t *testing.T) {
		rec := httptest.NewRecorder()
		respondFiling(rec, folderagent.FilingResult{DocumentsFilingResult: filejanitor.DocumentsFilingResult{Preview: preview}}, filejanitor.ErrReviewPlanChanged)
		if !strings.Contains(rec.Body.String(), `"state":"changed"`) {
			t.Fatalf("the page needs the fresh state: %s", rec.Body.String())
		}
	})
	t.Run("a filing is a success with the decision flag", func(t *testing.T) {
		rec := httptest.NewRecorder()
		respondFiling(rec, folderagent.FilingResult{DocumentsFilingResult: filejanitor.DocumentsFilingResult{FilingID: "filing-1", Filed: 2}, DecisionRecorded: true}, nil)
		var body struct {
			Filing struct {
				FilingID         string `json:"filing_id"`
				Filed            int    `json:"filed"`
				DecisionRecorded bool   `json:"decision_recorded"`
			} `json:"filing"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK || body.Filing.FilingID != "filing-1" || body.Filing.Filed != 2 || !body.Filing.DecisionRecorded {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})
}

func TestRespondUndo_ARefusalIsOnePlainSentenceAndAnAnswerCarriesEveryFile(t *testing.T) {
	rec := httptest.NewRecorder()
	respondUndo(rec, filejanitor.FilingUndoResult{}, errors.Join(errors.New("internal reason with /Users/me/Documents/secret.pdf"), filejanitor.ErrUndoUnavailable))
	var refused map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &refused); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusConflict || refused["code"] != "undo_unavailable" || strings.Contains(rec.Body.String(), "secret.pdf") || strings.Contains(rec.Body.String(), "internal reason") {
		t.Fatalf("the engine's reason and any path must stay out of the answer: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	respondUndo(rec, filejanitor.FilingUndoResult{
		FilingID: "filing-1", Back: 1, NotBack: 1,
		Outcomes: []filejanitor.FilingUndoOutcome{
			{Name: "one.pdf", Result: filejanitor.UndoOutcomeUndone},
			{Name: "two.pdf", Result: filejanitor.UndoOutcomeFailed, Message: "Something else is already using the original name, so Ori did not overwrite it.", Location: filejanitor.LocationFiled, Destination: "Taxes/two.pdf"},
		},
	}, nil)
	var body struct {
		Undo struct {
			Back     int `json:"back"`
			NotBack  int `json:"not_back"`
			Outcomes []struct {
				Name        string `json:"name"`
				Result      string `json:"result"`
				Location    string `json:"location"`
				Destination string `json:"destination"`
			} `json:"outcomes"`
		} `json:"undo"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || body.Undo.Back != 1 || body.Undo.NotBack != 1 || len(body.Undo.Outcomes) != 2 || body.Undo.Outcomes[1].Location != "filed" || body.Undo.Outcomes[1].Destination != "Taxes/two.pdf" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestOrganizationFiling_NewRoutesAreLocalOnlyAndStrict(t *testing.T) {
	routes := map[string]func(*Handler) http.HandlerFunc{
		"preview": func(h *Handler) http.HandlerFunc { return h.OrganizationFilingPreview },
		"file":    func(h *Handler) http.HandlerFunc { return h.OrganizationFile },
		"undo":    func(h *Handler) http.HandlerFunc { return h.OrganizationFilingUndo },
	}
	valid := `{"review":"r","group":"g","revision":1,"sources":["a"],"destination":{"folder":"Taxes"},"digest":"` + strings.Repeat("a", 64) + `","client_key":"key-aaaaaaaaaaaaaaaa"}`
	for name, route := range routes {
		t.Run(name+" refuses another origin", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://localhost/folder-agent/ws/x", strings.NewReader(valid))
			req.SetPathValue("workspaceID", "ws")
			req.Header.Set("Origin", "https://other.example")
			rec := httptest.NewRecorder()
			route(handler())(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
		})
		t.Run(name+" refuses unknown fields and paths", func(t *testing.T) {
			for _, body := range []string{
				`{"review":"r","group":"g","revision":1,"sources":["a"],"destination":{"folder":"Taxes"},"path":"/Users/me/Documents"}`,
				`{"review":"r","group":"g","revision":1,"sources":["a"],"destination":{"folder":"Taxes","absolute":"/tmp"}}`,
			} {
				req := httptest.NewRequest(http.MethodPost, "http://localhost/folder-agent/ws/x", strings.NewReader(body))
				req.SetPathValue("workspaceID", "ws")
				rec := httptest.NewRecorder()
				route(handler())(rec, req)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("%s → %d %s", body, rec.Code, rec.Body.String())
				}
			}
		})
	}
}
