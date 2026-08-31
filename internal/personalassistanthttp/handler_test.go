package personalassistanthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/types"
)

type fakeStateReader struct {
	projection *personalassistant.Projection
	err        error
	reads      int
}

func (f *fakeStateReader) Get(context.Context, string) (*personalassistant.Projection, error) {
	f.reads++
	return f.projection, f.err
}

type fakeHireService struct {
	result  *personalassistant.HireResult
	err     error
	request personalassistant.HireRequest
	userID  string
	calls   int
}

func (f *fakeHireService) Hire(_ context.Context, userID string, request personalassistant.HireRequest) (*personalassistant.HireResult, error) {
	f.calls++
	f.userID = userID
	f.request = request
	return f.result, f.err
}

type fakeUserProvider struct {
	userID string
	err    error
}

func (f fakeUserProvider) CurrentUserID(context.Context) (string, error) {
	return f.userID, f.err
}

func TestHandlerGetState_PinsMethodStatusAndProjection(t *testing.T) {
	reader := &fakeStateReader{projection: &personalassistant.Projection{
		State: personalassistant.APIStateActive, StateVersion: 8,
		AssistantID: "assistant-a", DisplayName: "Ada", NextAction: "ask",
		Availability: personalassistant.Availability{
			Rollout: personalassistant.SourceAvailability{Available: true, Status: personalassistant.AvailabilityAvailable},
		},
	}}
	handler := NewHandler(reader, fakeUserProvider{userID: "user-a"})

	recorder := httptest.NewRecorder()
	handler.GetState(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-assistant", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		PersonalAssistant personalassistant.Projection `json:"personal_assistant"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.PersonalAssistant.State != personalassistant.APIStateActive ||
		body.PersonalAssistant.StateVersion != 8 ||
		body.PersonalAssistant.AssistantID != "assistant-a" {
		t.Fatalf("projection = %#v", body.PersonalAssistant)
	}
	if reader.reads != 1 {
		t.Fatalf("service reads = %d, want 1", reader.reads)
	}

	methodRecorder := httptest.NewRecorder()
	handler.GetState(methodRecorder, httptest.NewRequest(http.MethodPost, "/api/personal-assistant", nil))
	if methodRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", methodRecorder.Code)
	}
	if reader.reads != 1 {
		t.Fatal("disallowed method reached the read service")
	}
}

func TestHandlerGetState_DependencyFailuresAreNotHealthyEmptyStates(t *testing.T) {
	reader := &fakeStateReader{err: errors.New("store offline")}
	handler := NewHandler(reader, fakeUserProvider{userID: "local"})
	recorder := httptest.NewRecorder()
	handler.GetState(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-assistant", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, exists := body["personal_assistant"]; exists {
		t.Fatal("dependency failure returned a fabricated personal-assistant state")
	}
}

func TestHandlerHire_ReturnsCanonicalResultAndForwardsBoundedInput(t *testing.T) {
	hirer := &fakeHireService{result: &personalassistant.HireResult{
		State: &personalassistant.State{
			Status: personalassistant.StatusActive, AssistantID: "assistant-1", DisplayName: "Assistant",
			Appearance: types.NewAgentAppearance(), HQWorkspaceID: "hq-1",
			HQEntryAgentInstanceID: "instance-1", GlobalAgentProfileName: "Assistant",
			StateVersion: 4, FirstAssignmentStatus: personalassistant.FirstAssignmentNotStarted,
		},
		BriefConfig: &dailybrief.Config{WorkspaceID: "hq-1", Timezone: "UTC", ScheduleTime: "08:00"},
	}}
	handler := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "user-a"})
	handler.SetHireService(hirer)
	body := `{"request_id":"request-1","if_version":0,"display_name":"Assistant","mandate":"Help me plan.","focus_areas":["plan my day"],"timezone":"UTC","schedule_days":["mon"],"schedule_time":"08:00","notify_on_ready":false}`
	recorder := httptest.NewRecorder()
	handler.Hire(recorder, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hire", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if hirer.calls != 1 || hirer.userID != "user-a" || hirer.request.RequestID != "request-1" ||
		hirer.request.DisplayName != "Assistant" || hirer.request.ScheduleTime != "08:00" {
		t.Fatalf("forwarded request = %#v, user=%q calls=%d", hirer.request, hirer.userID, hirer.calls)
	}
	var response struct {
		PersonalAssistant hireResponse `json:"personal_assistant"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.PersonalAssistant.AssistantID != "assistant-1" ||
		response.PersonalAssistant.HQWorkspaceID != "hq-1" ||
		response.PersonalAssistant.HQEntryAgentInstanceID != "instance-1" ||
		response.PersonalAssistant.StateVersion != 4 {
		t.Fatalf("response = %#v", response.PersonalAssistant)
	}
}

func TestHandlerHire_MapsTypedErrorsWithoutLeakingInternalCauses(t *testing.T) {
	partialState := &personalassistant.State{
		Status: personalassistant.StatusRepairNeeded, AssistantID: "assistant-1",
		HQWorkspaceID: "hq-1", HQEntryAgentInstanceID: "instance-1", StateVersion: 3,
	}
	tests := []struct {
		name, code string
		err        error
		status     int
		retryable  bool
	}{
		{"validation", "invalid_hire_request", personalassistant.ErrValidation, http.StatusBadRequest, false},
		{"ineligible", "personal_assistant_ineligible", personalassistant.ErrIneligible, http.StatusForbidden, false},
		{"state conflict", "hire_conflict", personalassistant.ErrConflict, http.StatusConflict, false},
		{"name conflict", "hire_conflict", personalhq.ErrAssistantNameConflict, http.StatusConflict, false},
		{"partial", "hire_partial", &personalassistant.PartialHireError{
			Step: personalassistant.RepairDailyBriefConfig, State: partialState,
			Err: errors.New("database password secret-internal"),
		}, http.StatusServiceUnavailable, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hirer := &fakeHireService{err: test.err}
			handler := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "local"})
			handler.SetHireService(hirer)
			recorder := httptest.NewRecorder()
			handler.Hire(recorder, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hire", strings.NewReader(`{}`)))
			if recorder.Code != test.status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var response hireErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if response.Code != test.code || response.Retryable != test.retryable {
				t.Fatalf("response = %#v", response)
			}
			if strings.Contains(recorder.Body.String(), "secret-internal") {
				t.Fatal("response leaked internal partial failure")
			}
			if test.code == "hire_partial" && (response.DurableResult == nil || response.DurableResult.HQWorkspaceID != "hq-1") {
				t.Fatalf("partial durable result = %#v", response.DurableResult)
			}
		})
	}
}

func TestHandlerHire_RejectsMalformedOversizedAndUnknownJSONBeforeService(t *testing.T) {
	tests := []string{
		`{"unknown":true}`,
		`{} {}`,
		`{"mandate":"` + strings.Repeat("x", maxHireBodyBytes) + `"}`,
	}
	for _, body := range tests {
		hirer := &fakeHireService{}
		handler := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "local"})
		handler.SetHireService(hirer)
		recorder := httptest.NewRecorder()
		handler.Hire(recorder, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hire", bytes.NewBufferString(body)))
		if recorder.Code != http.StatusBadRequest || hirer.calls != 0 {
			t.Fatalf("status=%d calls=%d body=%s", recorder.Code, hirer.calls, recorder.Body.String())
		}
	}
}

func TestHandlerGetState_UserResolutionFailureDoesNotReadState(t *testing.T) {
	reader := &fakeStateReader{}
	handler := NewHandler(reader, fakeUserProvider{err: errors.New("identity unavailable")})
	recorder := httptest.NewRecorder()
	handler.GetState(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-assistant", nil))
	if recorder.Code != http.StatusInternalServerError || reader.reads != 0 {
		t.Fatalf("status=%d reads=%d", recorder.Code, reader.reads)
	}
}
