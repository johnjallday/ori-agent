package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestLibraryArrangementRequest_RejectsAmbiguousSharingAndIdentity(t *testing.T) {
	for _, raw := range []string{`{"expected_context":null}`, `{"expected_context":""}`, `{"expected_context":12}`, `{"expected_context":"not-a-pin"}`, `null`, `[]`, `{"goal":null}`, `{"Goal":"x"}`, `{"expected_revision":1.5}`, `{"share_catalog_metadata":null}`, `{"share_catalog_metadata":true,"share_catalog_metadata":false}`, `{"owner":"foreign"}`, `{"manager_name":"forged"}`, `{"allow_upgrade":"true"}`, `{"goal":"x"} {}`} {
		var request libraryArrangementRequest
		if err := json.Unmarshal([]byte(raw), &request); err == nil {
			t.Fatal("ambiguous request accepted", raw)
		}
	}
	var request libraryArrangementRequest
	if err := json.Unmarshal([]byte(`{"goal":"Organize","expected_revision":1,"share_catalog_metadata":true,"allow_upgrade":false}`), &request); err != nil || !request.ShareCatalogMetadata || request.AllowUpgrade {
		t.Fatal("choice lost", err)
	}
}

func TestLibraryArrangementRequest_RevisionRequiresExactPairedPins(t *testing.T) {
	for _, raw := range []string{`{"revise_arrangement_id":"saved"}`, `{"expected_record_revision":1}`, `{"revise_arrangement_id":null,"expected_record_revision":1}`, `{"revise_arrangement_id":"saved","expected_record_revision":null}`, `{"revise_arrangement_id":"saved","expected_record_revision":1.5}`, `{"revise_arrangement_id":"saved","expected_record_revision":0}`, `{"revise_arrangement_id":"saved","expected_record_revision":1,"expected_record_revision":2}`} {
		var request libraryArrangementRequest
		if json.Unmarshal([]byte(raw), &request) == nil {
			t.Fatal("ambiguous revision accepted", raw)
		}
	}
	var request libraryArrangementRequest
	if err := json.Unmarshal([]byte(`{"revise_arrangement_id":"saved","expected_record_revision":2}`), &request); err != nil || request.ReviseArrangementID != "saved" || request.ExpectedRecordRevision != 2 {
		t.Fatal(request, err)
	}
}

func TestLibraryArrangementHTTP_RequiresOwnerAndPreservesSafeFailure(t *testing.T) {
	h, _, home, _ := assistantPortfolioHTTPFixture(t)
	h.currentUserID = func(context.Context) (string, error) { return home.OwnerUserID, nil }
	calls := 0
	h.SetArrangementPlanning(func(_ context.Context, in projectlibrary.ArrangementPlanningRequest) (projectlibrary.ArrangementPlanningResult, error) {
		calls++
		if in.Owner != home.OwnerUserID || in.HomeID != home.ID {
			t.Fatal("identity supplied by caller")
		}
		return projectlibrary.ArrangementPlanningResult{}, errors.New("private-api-key at /private/provider")
	})
	call := func(body, query string) *httptest.ResponseRecorder {
		r := assistantProgramRequest(http.MethodPost, "/library/arrangements/plan"+query, home.ID, body)
		w := httptest.NewRecorder()
		h.PlanAssistantLibraryArrangement(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("response cacheable")
		}
		return w
	}
	valid := `{"goal":"Organize","expected_revision":1,"share_catalog_metadata":true,"allow_upgrade":true}`
	for _, body := range []string{`{}`, `{"goal":"Organize","expected_revision":1,"share_catalog_metadata":false}`, `{"goal":"Organize","expected_revision":1,"share_catalog_metadata":true,"manager_name":"forged"}`} {
		if w := call(body, ""); w.Code != http.StatusBadRequest {
			t.Fatal("invalid request accepted", w.Code, w.Body.String())
		}
	}
	if w := call(valid, "?manager=forged"); w.Code != http.StatusBadRequest {
		t.Fatal("query accepted")
	}
	if calls != 0 {
		t.Fatal("planning invoked without prerequisites")
	}
	w := call(valid, "")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "planning_failed") || strings.Contains(w.Body.String(), "private-api-key") || strings.Contains(w.Body.String(), "/private/provider") || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatal("unsafe failure", w.Code, w.Body.String())
	}
	h.currentUserID = func(context.Context) (string, error) { return "foreign", nil }
	if w := call(valid, ""); w.Code != http.StatusNotFound || calls != 1 {
		t.Fatal("foreign owner invoked planning", w.Code, calls)
	}
}

func TestLibraryArrangementFailure_RecoveryCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{projectlibrary.ErrArrangementUpgradeRequired, "upgrade_required"}, {projectlibrary.ErrNoManagerModel, "model_unavailable"}, {workspacearrangement.ErrBusy, "busy"}, {workspacearrangement.ErrStale, "stale_context"}, {projectlibrary.ErrConflict, "stale_context"}, {context.Canceled, "cancelled"}, {context.DeadlineExceeded, "timed_out"}, {workspacearrangement.ErrLimit, "limit_reached"}, {workspacearrangement.ErrInvalid, "invalid_result"},
	} {
		w := httptest.NewRecorder()
		writeArrangementPlanningFailure(w, tc.err)
		if !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
			t.Fatal("wrong code", tc.code, w.Body.String())
		}
	}
}
