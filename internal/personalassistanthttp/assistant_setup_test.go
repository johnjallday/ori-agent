package personalassistanthttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantsetup"
)

type fakeAssistantSetupService struct {
	projection                                                    *assistantsetup.Projection
	err                                                           error
	created                                                       bool
	getCalls, acceptCalls, deferCalls, deferRunCalls, resumeCalls int
	owner, revision, target, runID                                string
	ifVersion                                                     int64
}

func (f *fakeAssistantSetupService) Get(_ context.Context, owner, target string) (*assistantsetup.Projection, error) {
	f.getCalls++
	f.owner, f.target = owner, target
	return f.projection, f.err
}
func (f *fakeAssistantSetupService) Accept(_ context.Context, owner, revision, target string) (*assistantsetup.Projection, bool, error) {
	f.acceptCalls++
	f.owner, f.revision, f.target = owner, revision, target
	return f.projection, f.created, f.err
}
func (f *fakeAssistantSetupService) DeferRecommendation(_ context.Context, owner, revision string) (*assistantsetup.Projection, error) {
	f.deferCalls++
	f.owner, f.revision = owner, revision
	return f.projection, f.err
}
func (f *fakeAssistantSetupService) DeferRun(_ context.Context, owner, runID string, version int64) (*assistantsetup.Projection, error) {
	f.deferRunCalls++
	f.owner, f.runID, f.ifVersion = owner, runID, version
	return f.projection, f.err
}
func (f *fakeAssistantSetupService) ResumeRun(_ context.Context, owner, runID string, version int64) (*assistantsetup.Projection, error) {
	f.resumeCalls++
	f.owner, f.runID, f.ifVersion = owner, runID, version
	return f.projection, f.err
}

func assistantSetupHTTPHandler(service AssistantSetupService) *Handler {
	handler := NewHandler(nil, fakeUserProvider{userID: "owner-1"})
	handler.SetAssistantSetupService(service)
	return handler
}

const testProposalRevision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestGetAssistantSetupIsOwnerScopedAndReadOnly(t *testing.T) {
	projection := &assistantsetup.Projection{SchemaVersion: 1, CapabilityID: assistantsetup.CapabilityID, ViewState: "proposed"}
	service := &fakeAssistantSetupService{projection: projection}
	handler := assistantSetupHTTPHandler(service)
	recorder := httptest.NewRecorder()
	handler.GetAssistantSetup(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/setup/file-janitor?target_workspace_id=workspace-1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.getCalls != 1 || service.owner != "owner-1" || service.target != "workspace-1" || service.acceptCalls != 0 {
		t.Fatalf("service calls=%+v", service)
	}
}

func TestGetAssistantSetupRejectsUnknownQueryWithoutReading(t *testing.T) {
	service := &fakeAssistantSetupService{}
	handler := assistantSetupHTTPHandler(service)
	recorder := httptest.NewRecorder()
	handler.GetAssistantSetup(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/setup/file-janitor?owner_user_id=other", nil))
	if recorder.Code != http.StatusBadRequest || service.getCalls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.getCalls, recorder.Body.String())
	}
}

func TestAcceptAssistantSetupUsesClosedBodyAndCreatedReplayStatuses(t *testing.T) {
	for _, test := range []struct {
		name    string
		created bool
		status  int
	}{
		{"created", true, http.StatusCreated}, {"replay", false, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeAssistantSetupService{created: test.created, projection: &assistantsetup.Projection{ViewState: "needs_permission"}}
			handler := assistantSetupHTTPHandler(service)
			recorder := httptest.NewRecorder()
			body := `{"proposal_revision":"` + testProposalRevision + `","target_workspace_id":"workspace-1"}`
			handler.AcceptAssistantSetup(recorder, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/setup/file-janitor/accept", strings.NewReader(body)))
			if recorder.Code != test.status || service.acceptCalls != 1 || service.owner != "owner-1" || service.revision != testProposalRevision {
				t.Fatalf("status=%d service=%+v body=%s", recorder.Code, service, recorder.Body.String())
			}
		})
	}

	service := &fakeAssistantSetupService{}
	handler := assistantSetupHTTPHandler(service)
	recorder := httptest.NewRecorder()
	handler.AcceptAssistantSetup(recorder, httptest.NewRequest(http.MethodPost, "/accept", strings.NewReader(`{"proposal_revision":"`+testProposalRevision+`","owner_user_id":"other"}`)))
	if recorder.Code != http.StatusBadRequest || service.acceptCalls != 0 {
		t.Fatalf("unknown field status=%d calls=%d", recorder.Code, service.acceptCalls)
	}
}

func TestAssistantSetupErrorsAreSanitized(t *testing.T) {
	service := &fakeAssistantSetupService{err: assistantsetup.ErrReconcileRequired}
	handler := assistantSetupHTTPHandler(service)
	recorder := httptest.NewRecorder()
	handler.AcceptAssistantSetup(recorder, httptest.NewRequest(http.MethodPost, "/accept", strings.NewReader(`{"proposal_revision":"`+testProposalRevision+`"}`)))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"reconcile_required"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "internal-path") {
		t.Fatal("raw error leaked")
	}

	service.err = errors.New("/private/internal-path: provider secret")
	recorder = httptest.NewRecorder()
	handler.GetAssistantSetup(recorder, httptest.NewRequest(http.MethodGet, "/setup", nil))
	if recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), "internal-path") || strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("unsafe body=%s", recorder.Body.String())
	}
}

func TestAssistantSetupRunMutationsRequireOwnedPathAndVersion(t *testing.T) {
	service := &fakeAssistantSetupService{projection: &assistantsetup.Projection{ViewState: "saved_for_later"}}
	handler := assistantSetupHTTPHandler(service)
	req := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/setup/file-janitor/runs/run-1/defer", strings.NewReader(`{"if_version":2}`))
	req.SetPathValue("runID", "run-1")
	recorder := httptest.NewRecorder()
	handler.DeferAssistantSetupRun(recorder, req)
	if recorder.Code != http.StatusOK || service.deferRunCalls != 1 || service.owner != "owner-1" || service.runID != "run-1" || service.ifVersion != 2 {
		t.Fatalf("status=%d service=%+v", recorder.Code, service)
	}

	req = httptest.NewRequest(http.MethodPost, "/resume", strings.NewReader(`{"if_version":0}`))
	req.SetPathValue("runID", "run-1")
	recorder = httptest.NewRecorder()
	handler.ResumeAssistantSetupRun(recorder, req)
	if recorder.Code != http.StatusBadRequest || service.resumeCalls != 0 {
		t.Fatalf("status=%d resume_calls=%d", recorder.Code, service.resumeCalls)
	}
}
