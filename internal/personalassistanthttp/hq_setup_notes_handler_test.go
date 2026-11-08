package personalassistanthttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// Personal HQ with notes included, at the HTTP boundary
// (assistant-hq-onboarding 3.4).

func TestHandlerSetupHQ_ForwardsTheReviewedNotesChoiceAndNothingWithoutIt(t *testing.T) {
	service := &fakeHQSetupService{result: activeHQResult()}
	handler := hqHandler(t, service)
	body := `{"request_id":"hq-1","if_version":3,"notes":{"include":true,"witness":"/Users/me/Ori Workspaces/Second Brain"}}`
	if got := postHQ(t, handler, body); got.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", got.Code, got.Body.String())
	}
	notes := service.request.Notes
	if notes == nil || !notes.Include || notes.Witness != "/Users/me/Ori Workspaces/Second Brain" || notes.SelectionToken != "" {
		t.Fatalf("forwarded notes = %+v", notes)
	}
	if got := postHQ(t, handler, `{"request_id":"hq-2","if_version":3}`); got.Code != http.StatusCreated {
		t.Fatalf("legacy status = %d", got.Code)
	}
	if service.request.Notes != nil {
		t.Fatalf("a request without notes forwarded a notes choice: %+v", service.request.Notes)
	}
}

func TestHandlerSetupHQ_ReportsTheNotesHalfBesideTheActiveHQ(t *testing.T) {
	result := activeHQResult()
	result.Notes = &personalassistant.SecondBrainSetupResult{
		Status: personalassistant.SecondBrainSetupFailed, Reason: "occupied",
		Message: "Second Brain already has something in it there, so Ori left it alone.",
		Path:    "/Users/me/Ori Workspaces/Second Brain",
	}
	handler := hqHandler(t, &fakeHQSetupService{result: result})
	got := postHQ(t, handler, `{"request_id":"hq-1","if_version":3,"notes":{"include":true,"witness":"/x/Second Brain"}}`)
	if got.Code != http.StatusCreated {
		t.Fatalf("a notes failure changed the HQ status code: %d", got.Code)
	}
	var payload struct {
		PersonalAssistant struct {
			State string `json:"state"`
		} `json:"personal_assistant"`
		NotesSetup *personalassistant.SecondBrainSetupResult `json:"notes_setup"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.PersonalAssistant.State != "active" || payload.NotesSetup == nil || payload.NotesSetup.Status != "failed" || payload.NotesSetup.Reason != "occupied" {
		t.Fatalf("payload = %s", got.Body.String())
	}
	legacy := hqHandler(t, &fakeHQSetupService{result: activeHQResult()})
	if body := postHQ(t, legacy, `{"request_id":"hq-1","if_version":3}`).Body.String(); strings.Contains(body, "notes_setup") {
		t.Fatalf("a legacy response carries notes_setup: %s", body)
	}
}

func TestHandlerSetupHQ_NotesRefusalsHappenBeforeHQAndSaySo(t *testing.T) {
	cases := []struct {
		err  error
		code int
		want string
	}{
		{personalassistant.ErrSecondBrainDestinationChanged, http.StatusConflict, "notes_destination_changed"},
		{personalassistant.ErrSecondBrainSelection, http.StatusConflict, "notes_selection_unavailable"},
		{&personalassistant.SecondBrainFolderError{Message: "Choose a simple folder name, without slashes."}, http.StatusBadRequest, "notes_folder_invalid"},
		{personalassistant.ErrSecondBrainUnavailable, http.StatusServiceUnavailable, "notes_unavailable"},
	}
	for _, tc := range cases {
		handler := hqHandler(t, &fakeHQSetupService{err: tc.err})
		got := postHQ(t, handler, `{"request_id":"hq-1","if_version":3,"notes":{"include":true,"witness":"/x/Second Brain"}}`)
		if got.Code != tc.code || !strings.Contains(got.Body.String(), tc.want) {
			t.Errorf("%v: %d %s", tc.err, got.Code, got.Body.String())
		}
		if strings.Contains(got.Body.String(), "/x/") {
			t.Errorf("%v: the refusal echoed a path: %s", tc.err, got.Body.String())
		}
	}
}

func TestHandlerSetupHQ_RefusesARawNotesPathField(t *testing.T) {
	service := &fakeHQSetupService{result: activeHQResult()}
	handler := hqHandler(t, service)
	got := postHQ(t, handler, `{"request_id":"hq-1","if_version":3,"notes":{"include":true,"path":"/etc"}}`)
	if got.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("a raw notes path reached the service: %d calls %d", got.Code, service.calls)
	}
}

func TestSecondBrainSetupRetry_IsOriginGuardedTakesNoFolderAndReportsTheOutcome(t *testing.T) {
	fake := &fakeSecondBrain{}
	handler := newSecondBrainHandler(fake)
	if got := secondBrainRequest(handler, http.MethodPost, "/setup/retry", `{}`, "https://evil.example"); got.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", got.Code)
	}
	if got := secondBrainRequest(handler, http.MethodPost, "/setup/retry", `{"path":"/etc"}`, "http://localhost:8931"); got.Code != http.StatusBadRequest {
		t.Fatalf("a folder field was accepted: %d", got.Code)
	}
	got := secondBrainRequest(handler, http.MethodPost, "/setup/retry", `{}`, "http://localhost:8931")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"notes_setup"`) {
		t.Fatalf("retry = %d %s", got.Code, got.Body.String())
	}
	if n := strings.Count(strings.Join(fake.calls, ","), "retry_setup"); n != 1 {
		t.Fatalf("retry_setup called %d times", n)
	}
}

func TestSecondBrainSetupRetry_NothingToRetryIsAConflict(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeSecondBrainError(recorder, fmt.Errorf("wrapped: %w", personalassistant.ErrSecondBrainNoSetup))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "no notes setup to retry") {
		t.Fatalf("no setup = %d %s", recorder.Code, recorder.Body.String())
	}
}
