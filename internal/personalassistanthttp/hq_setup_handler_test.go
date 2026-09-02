package personalassistanthttp

import (
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
)

type fakeHQSetupService struct {
	result  *personalassistant.HQSetupResult
	err     error
	request personalassistant.HQSetupRequest
	userID  string
	calls   int
}

func (f *fakeHQSetupService) Setup(_ context.Context, userID string, request personalassistant.HQSetupRequest) (*personalassistant.HQSetupResult, error) {
	f.calls++
	f.userID = userID
	f.request = request
	return f.result, f.err
}

func activeHQResult() *personalassistant.HQSetupResult {
	return &personalassistant.HQSetupResult{
		State: &personalassistant.State{
			Status: personalassistant.StatusActive, AssistantID: "assistant-1",
			DisplayName: "Atlas", GlobalAgentProfileName: "Atlas",
			HQWorkspaceID: "hq-1", HQEntryAgentInstanceID: "instance-1", StateVersion: 5,
		},
		BriefConfig: &dailybrief.Config{
			WorkspaceID: "hq-1", Timezone: "America/New_York", ScheduleTime: "07:30",
		},
	}
}

func postHQ(t *testing.T, handler *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.SetupHQ(recorder, httptest.NewRequest(
		http.MethodPost, "/api/personal-assistant/hq", strings.NewReader(body)))
	return recorder
}

func hqHandler(t *testing.T, service *fakeHQSetupService) *Handler {
	t.Helper()
	handler := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "user-a"})
	handler.SetHQSetupService(service)
	return handler
}

func TestHandlerSetupHQ_ForwardsOnlyBoundedFormFields(t *testing.T) {
	service := &fakeHQSetupService{result: activeHQResult()}
	handler := hqHandler(t, service)

	body := `{"request_id":"hq-request-1","if_version":4,"name":"Command Post",` +
		`"timezone":"America/New_York","schedule_days":["mon","wed"],"schedule_time":"07:30",` +
		`"scope":"selected","selected_workspace_ids":["ws-a"],` +
		`"include_future_workspaces":true,"notify_on_ready":true}`
	recorder := postHQ(t, handler, body)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}

	if service.calls != 1 || service.userID != "user-a" {
		t.Fatalf("calls=%d user=%q", service.calls, service.userID)
	}
	got := service.request
	if got.RequestID != "hq-request-1" || got.IfVersion != 4 || got.HQName != "Command Post" ||
		got.Timezone != "America/New_York" || got.ScheduleTime != "07:30" ||
		got.Scope != "selected" || !got.IncludeFuture || !got.NotifyOnReady {
		t.Fatalf("forwarded request = %#v", got)
	}
	if len(got.ScheduleDays) != 2 || len(got.SelectedIDs) != 1 || got.SelectedIDs[0] != "ws-a" {
		t.Fatalf("forwarded collections = %#v", got)
	}
}

func TestHandlerSetupHQ_RejectsForgedIdentityFieldsOutright(t *testing.T) {
	// The request struct has nowhere to put assistant, profile, workspace, or
	// instance identity — and the decoder disallows unknown fields, so a client
	// that tries to supply one is refused rather than silently ignored.
	forged := []string{
		`"assistant_id":"forged"`,
		`"hq_workspace_id":"forged-ws"`,
		`"hq_entry_agent_instance_id":"forged-instance"`,
		`"global_agent_profile_name":"Somebody Else"`,
		`"user_id":"another-user"`,
		`"status":"active"`,
	}
	for _, field := range forged {
		service := &fakeHQSetupService{result: activeHQResult()}
		body := `{"request_id":"hq-request-1","if_version":4,` + field + `}`
		recorder := postHQ(t, hqHandler(t, service), body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d; want 400", field, recorder.Code)
		}
		if service.calls != 0 {
			t.Fatalf("%s reached the coordinator", field)
		}
	}
}

func TestHandlerSetupHQ_ReturnsTheActiveRelationshipAndCanonicalIDs(t *testing.T) {
	service := &fakeHQSetupService{result: activeHQResult()}
	recorder := postHQ(t, hqHandler(t, service), `{"request_id":"hq-request-1","if_version":4}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d", recorder.Code)
	}
	var response struct {
		PersonalAssistant map[string]any `json:"personal_assistant"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assistant := response.PersonalAssistant
	if assistant["state"] != "active" || assistant["next_action"] != "ask" {
		t.Fatalf("state = %v", assistant)
	}
	if assistant["hq_workspace_id"] != "hq-1" || assistant["hq_entry_agent_instance_id"] != "instance-1" {
		t.Fatalf("canonical ids = %v", assistant)
	}
	brief, ok := assistant["daily_brief"].(map[string]any)
	if !ok || brief["schedule_time"] != "07:30" {
		t.Fatalf("daily brief = %v", assistant["daily_brief"])
	}
}

func TestHandlerSetupHQ_ReplayIsOKRatherThanCreated(t *testing.T) {
	result := activeHQResult()
	result.Resumed = true
	recorder := postHQ(t, hqHandler(t, &fakeHQSetupService{result: result}),
		`{"request_id":"hq-request-1"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("replay status = %d; want 200", recorder.Code)
	}
}

func TestHandlerSetupHQ_PinsStatusCodesWithoutLeakingCauses(t *testing.T) {
	partialState := &personalassistant.State{
		Status: personalassistant.StatusProvisioningHQ, AssistantID: "assistant-1",
		DisplayName: "Atlas", GlobalAgentProfileName: "Atlas",
		HQWorkspaceID: "hq-1", StateVersion: 3,
	}
	tests := []struct {
		name, code string
		err        error
		status     int
		retryable  bool
	}{
		{"validation", "invalid_hq_setup_request", personalassistant.ErrValidation, http.StatusBadRequest, false},
		{"state conflict", "hq_setup_conflict", personalassistant.ErrConflict, http.StatusConflict, false},
		{"name conflict", "hq_setup_conflict", personalhq.ErrAssistantNameConflict, http.StatusConflict, false},
		{"repair needed", "hq_setup_repair_needed", personalassistant.ErrRepairNeeded, http.StatusConflict, false},
		{"partial", "hq_setup_partial", &personalassistant.PartialHQSetupError{
			Step:  personalassistant.RepairDailyBriefConfig,
			State: partialState,
			Err:   errors.New("dial tcp 10.0.0.5:5432: connection refused; password=hunter2"),
		}, http.StatusServiceUnavailable, true},
		{"unknown", "hq_setup_unavailable", errors.New("sql: no rows in result set"), http.StatusServiceUnavailable, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := postHQ(t, hqHandler(t, &fakeHQSetupService{err: test.err}),
				`{"request_id":"hq-request-1","if_version":4}`)
			if recorder.Code != test.status {
				t.Fatalf("status = %d; want %d", recorder.Code, test.status)
			}
			var response hqSetupErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if response.Code != test.code || response.Retryable != test.retryable {
				t.Fatalf("response = %#v", response)
			}
			// No provider, database, host, or credential text may reach a client.
			body := recorder.Body.String()
			for _, leak := range []string{"dial tcp", "10.0.0.5", "hunter2", "sql:", "password"} {
				if strings.Contains(body, leak) {
					t.Fatalf("response leaked %q: %s", leak, body)
				}
			}
		})
	}
}

func TestHandlerSetupHQ_PartialReportsTheDurableRecordsAndSafeStep(t *testing.T) {
	partial := &personalassistant.PartialHQSetupError{
		Step: personalassistant.RepairDesignation,
		State: &personalassistant.State{
			Status: personalassistant.StatusProvisioningHQ, AssistantID: "assistant-1",
			DisplayName: "Atlas", GlobalAgentProfileName: "Atlas",
			HQWorkspaceID: "hq-1", HQEntryAgentInstanceID: "instance-1", StateVersion: 3,
		},
		Err: errors.New("internal"),
	}
	recorder := postHQ(t, hqHandler(t, &fakeHQSetupService{err: partial}),
		`{"request_id":"hq-request-1","if_version":4}`)

	var response hqSetupErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.RepairStep != personalassistant.RepairDesignation {
		t.Fatalf("repair step = %q", response.RepairStep)
	}
	if response.DurableResult == nil || response.DurableResult.HQWorkspaceID != "hq-1" {
		t.Fatalf("durable result = %#v", response.DurableResult)
	}
	// It reads as resumable, not as an invitation to create another HQ.
	if !response.Retryable || response.DurableResult.State != personalassistant.APIStateProvisioningHQ {
		t.Fatalf("partial did not read as resumable: %#v", response)
	}
}

func TestHandlerSetupHQ_RejectsWrongMethodAndUnconfiguredService(t *testing.T) {
	handler := hqHandler(t, &fakeHQSetupService{result: activeHQResult()})
	recorder := httptest.NewRecorder()
	handler.SetupHQ(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/hq", nil))
	if recorder.Code == http.StatusOK || recorder.Code == http.StatusCreated {
		t.Fatalf("GET was accepted: %d", recorder.Code)
	}

	bare := NewHandler(&fakeStateReader{}, fakeUserProvider{userID: "user-a"})
	unavailable := postHQ(t, bare, `{"request_id":"hq-request-1"}`)
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured status = %d", unavailable.Code)
	}
}

func TestHandlerSetupHQ_RejectsMalformedBodyBeforeAnyConsequence(t *testing.T) {
	for _, body := range []string{"", "not json", "[]", `{"request_id":"a"}{"request_id":"b"}`} {
		service := &fakeHQSetupService{result: activeHQResult()}
		recorder := postHQ(t, hqHandler(t, service), body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d; want 400", body, recorder.Code)
		}
		if service.calls != 0 {
			t.Fatalf("body %q reached the coordinator", body)
		}
	}
}
