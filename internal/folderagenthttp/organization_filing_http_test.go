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

func TestOrganizationFiling_NewRoutesAreLocalOnlyAndStrict(t *testing.T) {
	routes := map[string]func(*Handler) http.HandlerFunc{
		"preview": func(h *Handler) http.HandlerFunc { return h.OrganizationFilingPreview },
		"file":    func(h *Handler) http.HandlerFunc { return h.OrganizationFile },
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
