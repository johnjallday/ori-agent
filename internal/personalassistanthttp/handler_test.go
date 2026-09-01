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

type fakeTodayReader struct {
	projection *personalassistant.TodayProjection
	err        error
	userID     string
	calls      int
}

func (f *fakeTodayReader) Get(_ context.Context, userID string) (*personalassistant.TodayProjection, error) {
	f.calls++
	f.userID = userID
	return f.projection, f.err
}

type fakeAssignmentService struct {
	result      *personalassistant.AssignmentPreviewResult
	err         error
	userID      string
	ifVersion   int64
	input       personalassistant.AssignmentInput
	calls       int
	applyResult *personalassistant.AssignmentApplyResult
	applyErr    error
	apply       personalassistant.AssignmentApplyRequest
	current     *personalassistant.AssignmentCurrentResult
	currentErr  error
}

func (f *fakeAssignmentService) Current(_ context.Context, userID string) (*personalassistant.AssignmentCurrentResult, error) {
	f.calls++
	f.userID = userID
	return f.current, f.currentErr
}

func (f *fakeAssignmentService) Preview(_ context.Context, userID string, ifVersion int64, input personalassistant.AssignmentInput) (*personalassistant.AssignmentPreviewResult, error) {
	f.calls++
	f.userID = userID
	f.ifVersion = ifVersion
	f.input = input
	return f.result, f.err
}

func (f *fakeAssignmentService) Apply(_ context.Context, userID string, request personalassistant.AssignmentApplyRequest) (*personalassistant.AssignmentApplyResult, error) {
	f.calls++
	f.userID = userID
	f.apply = request
	return f.applyResult, f.applyErr
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

type fakeContinuityService struct {
	projection *personalassistant.Projection
	err        error
	update     personalassistant.WorkingAgreementUpdate
	ifVersion  int64
	action     string
}

func (f *fakeContinuityService) UpdateWorkingAgreement(_ context.Context, _ string, request personalassistant.WorkingAgreementUpdate) (*personalassistant.Projection, error) {
	f.update = request
	return f.projection, f.err
}
func (f *fakeContinuityService) Pause(_ context.Context, _ string, ifVersion int64) (*personalassistant.Projection, error) {
	f.ifVersion, f.action = ifVersion, "pause"
	return f.projection, f.err
}
func (f *fakeContinuityService) Resume(_ context.Context, _ string, ifVersion int64) (*personalassistant.Projection, error) {
	f.ifVersion, f.action = ifVersion, "resume"
	return f.projection, f.err
}

type fakeUserProvider struct {
	userID string
	err    error
}

func (f fakeUserProvider) CurrentUserID(context.Context) (string, error) {
	return f.userID, f.err
}

func TestHandlerWorkingAgreement_PinsPatchFieldsAndConflictProjection(t *testing.T) {
	current := &personalassistant.Projection{State: personalassistant.APIStateActive, StateVersion: 9, DisplayName: "Ada"}
	reader := &fakeStateReader{projection: current}
	continuity := &fakeContinuityService{projection: current}
	handler := NewHandler(reader, fakeUserProvider{userID: "local"})
	handler.SetContinuityService(continuity)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/personal-assistant/working-agreement", strings.NewReader(`{"if_version":9,"mandate":"Keep launches visible","timezone":"UTC","schedule_enabled":false}`))
	request.Header.Set("Content-Type", "application/json")
	handler.UpdateWorkingAgreement(recorder, request)
	if recorder.Code != http.StatusOK || continuity.update.IfVersion != 9 || continuity.update.Mandate == nil || *continuity.update.Mandate != "Keep launches visible" || continuity.update.ScheduleEnabled == nil || *continuity.update.ScheduleEnabled {
		t.Fatalf("status=%d update=%+v body=%s", recorder.Code, continuity.update, recorder.Body.String())
	}

	continuity.err = personalassistant.ErrConflict
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPatch, "/api/personal-assistant/working-agreement", strings.NewReader(`{"if_version":8}`))
	handler.UpdateWorkingAgreement(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"state_version":9`) || !strings.Contains(recorder.Body.String(), `"code":"state_conflict"`) {
		t.Fatalf("conflict status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerPauseResumeRequireVersionAndPreserveProjection(t *testing.T) {
	projection := &personalassistant.Projection{State: personalassistant.APIStatePaused, StateVersion: 10}
	continuity := &fakeContinuityService{projection: projection}
	handler := NewHandler(&fakeStateReader{projection: projection}, fakeUserProvider{userID: "local"})
	handler.SetContinuityService(continuity)
	for _, test := range []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
	}{
		{"pause", handler.Pause}, {"resume", handler.Resume},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.call(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"if_version":10}`)))
			if recorder.Code != http.StatusOK || continuity.ifVersion != 10 || continuity.action != test.name {
				t.Fatalf("status=%d action=%s version=%d body=%s", recorder.Code, continuity.action, continuity.ifVersion, recorder.Body.String())
			}
		})
	}
}

func TestHandlerGetState_PinsMethodStatusAndProjection(t *testing.T) {
	reader := &fakeStateReader{projection: &personalassistant.Projection{
		State: personalassistant.APIStateActive, StateVersion: 8,
		AssistantID: "assistant-a", DisplayName: "Ada", NextAction: "ask",
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

func TestHandlerGetToday_UsesCurrentUserAndDoesNotLeakReadErrors(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		reader := &fakeTodayReader{projection: &personalassistant.TodayProjection{State: "active", DisplayName: "Nova"}}
		handler := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "user-a"})
		handler.SetTodayService(reader)
		recorder := httptest.NewRecorder()
		handler.GetToday(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/today", nil))
		if recorder.Code != http.StatusOK || reader.userID != "user-a" || !strings.Contains(recorder.Body.String(), `"display_name":"Nova"`) {
			t.Fatalf("status=%d user=%q body=%s", recorder.Code, reader.userID, recorder.Body.String())
		}
	})

	t.Run("bounded failure", func(t *testing.T) {
		reader := &fakeTodayReader{err: errors.New("sqlite password=do-not-leak")}
		handler := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "user-a"})
		handler.SetTodayService(reader)
		recorder := httptest.NewRecorder()
		handler.GetToday(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/today", nil))
		if recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), "do-not-leak") {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestHandlerHire_FreshHireReturnsNeedsHQWithoutHQOrBriefClaims(t *testing.T) {
	hirer := &fakeHireService{result: &personalassistant.HireResult{
		State: &personalassistant.State{
			Status: personalassistant.StatusAwaitingHQ, AssistantID: "assistant-1",
			DisplayName: "Atlas", Appearance: types.NewAgentAppearance(),
			GlobalAgentProfileName: "Atlas", StateVersion: 2,
			FirstAssignmentStatus: personalassistant.FirstAssignmentNotStarted,
		},
	}}
	handler := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "user-a"})
	handler.SetHireService(hirer)
	body := `{"request_id":"request-1","if_version":0,"display_name":"Atlas","mandate":"Help me plan.","focus_areas":["plan my day"]}`
	recorder := httptest.NewRecorder()
	handler.Hire(recorder, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hire", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		PersonalAssistant map[string]any `json:"personal_assistant"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assistant := response.PersonalAssistant
	if assistant["status"] != "awaiting_hq" || assistant["state"] != "needs_hq" ||
		assistant["next_action"] != "build_hq" {
		t.Fatalf("hire response state = %v", assistant)
	}
	if assistant["assistant_id"] != "assistant-1" || assistant["display_name"] != "Atlas" ||
		assistant["global_agent_profile_name"] != "Atlas" || assistant["appearance"] == nil ||
		assistant["state_version"] != float64(2) {
		t.Fatalf("hire response identity = %v", assistant)
	}
	// A fresh hire has no HQ and no Daily Brief, so it must claim neither.
	for _, key := range []string{"hq_workspace_id", "hq_entry_agent_instance_id", "daily_brief"} {
		if _, present := assistant[key]; present {
			t.Fatalf("fresh hire response advertised %s: %v", key, assistant)
		}
	}
}

func TestHandlerHire_AcceptsButIgnoresStaleRhythmFields(t *testing.T) {
	hirer := &fakeHireService{result: &personalassistant.HireResult{
		State: &personalassistant.State{
			Status: personalassistant.StatusAwaitingHQ, AssistantID: "assistant-1",
			DisplayName: "Atlas", GlobalAgentProfileName: "Atlas", StateVersion: 2,
		},
	}}
	handler := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "user-a"})
	handler.SetHireService(hirer)
	// A stale client still sends the rhythm. The request must be accepted, and
	// the coordinator is the one that drops it.
	body := `{"request_id":"request-1","display_name":"Atlas","focus_areas":["plan my day"],` +
		`"timezone":"UTC","schedule_days":["mon"],"schedule_time":"08:00","notify_on_ready":true}`
	recorder := httptest.NewRecorder()
	handler.Hire(recorder, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hire", strings.NewReader(body)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if hirer.calls != 1 || hirer.request.RequestID != "request-1" || hirer.request.DisplayName != "Atlas" {
		t.Fatalf("forwarded request = %#v calls=%d", hirer.request, hirer.calls)
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

func TestHandlerGetFirstAssignment_ReturnsDurableResumeIdentity(t *testing.T) {
	service := &fakeAssignmentService{current: &personalassistant.AssignmentCurrentResult{
		StateVersion: 6, Status: personalassistant.AssignmentApplying,
		ApplyRequestID: "apply-1",
		Preview: &personalassistant.AssignmentPreview{
			PreviewID: "preview-1", AssignmentVersion: 3, PayloadHash: "hash", Count: 1,
		},
	}}
	handler := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "local"})
	handler.SetAssignmentService(service)
	recorder := httptest.NewRecorder()
	handler.GetFirstAssignment(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/first-assignment", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"apply_request_id":"apply-1"`) ||
		!strings.Contains(recorder.Body.String(), `"state_version":6`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerPreviewFirstAssignment_ReturnsExactCanonicalRows(t *testing.T) {
	service := &fakeAssignmentService{result: &personalassistant.AssignmentPreviewResult{
		Preview: &personalassistant.AssignmentPreview{
			PreviewID: "preview-1", AssignmentVersion: 2, PayloadHash: "hash", Count: 1,
			Items: []personalassistant.AssignmentPreviewItem{{
				ID: "item-1", InputType: personalassistant.AssignmentRowIOwe,
				RecordType: personalassistant.AssignmentRecordFollowUp,
				Category:   "i_owe", State: "active", Title: "Send the draft",
			}},
		},
		StateVersion: 5, Status: personalassistant.FirstAssignmentPreviewed,
	}}
	handler := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "user-a"})
	handler.SetAssignmentService(service)
	recorder := httptest.NewRecorder()
	body := `{"if_version":4,"rows":[{"type":"i_owe","title":"Send the draft","counterparty":"Maya"}]}`
	handler.PreviewFirstAssignment(recorder, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/first-assignment/preview", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.calls != 1 || service.userID != "user-a" || service.ifVersion != 4 ||
		len(service.input.Rows) != 1 || service.input.Rows[0].Counterparty != "Maya" {
		t.Fatalf("preview call = %#v, user=%q version=%d", service.input, service.userID, service.ifVersion)
	}
	if !strings.Contains(recorder.Body.String(), `"record_type":"follow_up"`) ||
		!strings.Contains(recorder.Body.String(), `"state_version":5`) {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

func TestHandlerPreviewFirstAssignment_MapsValidationAndStaleConflict(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"validation", personalassistant.ErrValidation, http.StatusBadRequest, "invalid_assignment"},
		{"stale", &personalassistant.AssignmentPreviewConflictError{
			StateVersion: 7,
			Preview:      &personalassistant.AssignmentPreview{PreviewID: "current", AssignmentVersion: 3},
			Err:          personalassistant.ErrConflict,
		}, http.StatusConflict, "assignment_conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeAssignmentService{err: test.err}
			handler := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "local"})
			handler.SetAssignmentService(service)
			recorder := httptest.NewRecorder()
			handler.PreviewFirstAssignment(recorder, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/first-assignment/preview", strings.NewReader(`{"if_version":1,"rows":[]}`)))
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if test.status == http.StatusConflict && (!strings.Contains(recorder.Body.String(), `"state_version":7`) || !strings.Contains(recorder.Body.String(), `"preview_id":"current"`)) {
				t.Fatalf("conflict omitted current versions: %s", recorder.Body.String())
			}
		})
	}
}

func TestHandlerApplyFirstAssignment_ReturnsCompleteAndBoundedPartialResults(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		service := &fakeAssignmentService{applyResult: &personalassistant.AssignmentApplyResult{
			PreviewID: "preview-1", AssignmentVersion: 4, StateVersion: 7,
			Status: personalassistant.AssignmentCompleted, AppliedCount: 1, TotalCount: 1,
			CreatedCanonicalRefs: []personalassistant.CanonicalRef{{Kind: "ticket", WorkspaceID: "hq", ID: "ticket-1"}},
		}}
		handler := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "local"})
		handler.SetAssignmentService(service)
		completedCalls := 0
		handler.SetOnFirstAssignmentCompleted(func() { completedCalls++ })
		recorder := httptest.NewRecorder()
		body := `{"preview_id":"preview-1","preview_version":1,"payload_hash":"hash","if_version":2,"apply_request_id":"apply-1"}`
		handler.ApplyFirstAssignment(recorder, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/first-assignment/apply", strings.NewReader(body)))
		if recorder.Code != http.StatusOK || service.apply.ApplyRequestID != "apply-1" || !strings.Contains(recorder.Body.String(), `"id":"ticket-1"`) {
			t.Fatalf("status=%d request=%#v body=%s", recorder.Code, service.apply, recorder.Body.String())
		}
		if completedCalls != 1 {
			t.Fatalf("completion callback calls = %d, want 1", completedCalls)
		}
	})

	t.Run("partial does not leak cause", func(t *testing.T) {
		partialResult := &personalassistant.AssignmentApplyResult{
			PreviewID: "preview-1", AssignmentVersion: 3, StateVersion: 6,
			Status: personalassistant.AssignmentApplying, AppliedCount: 1, TotalCount: 2, Retryable: true,
			CreatedCanonicalRefs: []personalassistant.CanonicalRef{{Kind: "ticket", ID: "ticket-1"}},
		}
		service := &fakeAssignmentService{applyErr: &personalassistant.PartialAssignmentError{
			Result: partialResult, Err: errors.New("database password super-secret"),
		}}
		handler := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "local"})
		handler.SetAssignmentService(service)
		completedCalls := 0
		handler.SetOnFirstAssignmentCompleted(func() { completedCalls++ })
		recorder := httptest.NewRecorder()
		handler.ApplyFirstAssignment(recorder, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/first-assignment/apply", strings.NewReader(`{"preview_id":"preview-1","preview_version":1,"payload_hash":"hash","if_version":2,"apply_request_id":"apply-1"}`)))
		if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"code":"assignment_partial"`) ||
			!strings.Contains(recorder.Body.String(), `"retryable":true`) || strings.Contains(recorder.Body.String(), "super-secret") {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if completedCalls != 0 {
			t.Fatalf("partial apply invoked completion callback %d times", completedCalls)
		}
	})
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
