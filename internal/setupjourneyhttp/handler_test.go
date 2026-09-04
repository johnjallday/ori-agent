package setupjourneyhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/setupjourney"
)

type fixedUserProvider struct {
	id  string
	err error
}

func (provider fixedUserProvider) CurrentUserID(context.Context) (string, error) {
	return provider.id, provider.err
}

type serviceStub struct {
	projection *setupjourney.JourneyProjection
	readErr    error
	openErr    error
	dismissErr error
	childErr   error
	actionErr  error

	lastOperation string
	lastUserID    string
	lastRunID     string
	lastActionID  setupjourney.ActionID
	lastMutation  setupjourney.PresentationMutation
	lastAction    setupjourney.ActionMutation
}

func (stub *serviceStub) Read(_ context.Context, userID, runID string) (*setupjourney.JourneyProjection, error) {
	stub.lastOperation = "read"
	stub.lastUserID = userID
	stub.lastRunID = runID
	return stub.projection, stub.readErr
}

func (stub *serviceStub) Open(_ context.Context, userID, runID string, request setupjourney.PresentationMutation) (*setupjourney.JourneyProjection, error) {
	stub.lastOperation = "open"
	stub.lastUserID = userID
	stub.lastRunID = runID
	stub.lastMutation = request
	return stub.projection, stub.openErr
}

func (stub *serviceStub) Dismiss(_ context.Context, userID, runID string, request setupjourney.PresentationMutation) (*setupjourney.JourneyProjection, error) {
	stub.lastOperation = "dismiss"
	stub.lastUserID = userID
	stub.lastRunID = runID
	stub.lastMutation = request
	return stub.projection, stub.dismissErr
}

func (stub *serviceStub) CreateOrResumeChild(_ context.Context, userID string, request setupjourney.PresentationMutation) (*setupjourney.JourneyProjection, error) {
	stub.lastOperation = "child"
	stub.lastUserID = userID
	stub.lastMutation = request
	return stub.projection, stub.childErr
}

func (stub *serviceStub) Mutate(_ context.Context, userID, runID string, actionID setupjourney.ActionID, request setupjourney.ActionMutation) (*setupjourney.ActionResult, error) {
	stub.lastOperation = "action"
	stub.lastUserID = userID
	stub.lastRunID = runID
	stub.lastActionID = actionID
	stub.lastAction = request
	return &setupjourney.ActionResult{Journey: stub.projection}, stub.actionErr
}

func testProjection() *setupjourney.JourneyProjection {
	return &setupjourney.JourneyProjection{
		RunID: "run-current", RunKind: setupjourney.RunKindRoot,
		Journey:       setupjourney.DeclarationProjection{ID: "journey", SchemaVersion: 1, Version: 1, Title: "Setup", Description: "Review setup."},
		StateRevision: 7, Lifecycle: setupjourney.LifecycleInProgress,
		CurrentStepID: "integration",
		Steps:         []setupjourney.StepProjection{{ID: "integration", Status: setupjourney.StepActive}},
	}
}

func testMux(handler *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/personal-assistant/setup-journey", handler.GetRoot)
	mux.HandleFunc("GET /api/personal-assistant/setup-journey/runs/{runID}", handler.GetRun)
	mux.HandleFunc("POST /api/personal-assistant/setup-journey/open", handler.OpenRoot)
	mux.HandleFunc("POST /api/personal-assistant/setup-journey/runs/{runID}/open", handler.OpenRun)
	mux.HandleFunc("POST /api/personal-assistant/setup-journey/dismiss", handler.DismissRoot)
	mux.HandleFunc("POST /api/personal-assistant/setup-journey/runs/{runID}/dismiss", handler.DismissRun)
	mux.HandleFunc("POST /api/personal-assistant/setup-journey/children", handler.CreateChild)
	mux.HandleFunc("POST /api/personal-assistant/setup-journey/runs/{runID}/actions/{actionID}", handler.Mutate)
	return mux
}

func TestHandlerUsesCurrentUserAndExactRunPath(t *testing.T) {
	stub := &serviceStub{projection: testProjection()}
	mux := testMux(NewHandler(stub, fixedUserProvider{id: "current-user"}))

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
		"/api/personal-assistant/setup-journey/runs/run-child", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%s", response.Code, response.Body.String())
	}
	if stub.lastOperation != "read" || stub.lastUserID != "current-user" || stub.lastRunID != "run-child" {
		t.Fatalf("handler did not derive current user/exact run: %#v", stub)
	}
	if strings.Contains(response.Body.String(), "current-user") {
		t.Fatalf("response leaked relationship owner: %s", response.Body.String())
	}

	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost,
		"/api/personal-assistant/setup-journey/runs/run-child/open",
		strings.NewReader(`{"if_revision":7,"idempotency_key":"open-1"}`))
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || stub.lastOperation != "open" ||
		stub.lastMutation.IfRevision != 7 || stub.lastMutation.IdempotencyKey != "open-1" {
		t.Fatalf("open request not delegated exactly once: status=%d stub=%#v body=%s", response.Code, stub, response.Body.String())
	}
}

func TestHandlerRejectsClientSelectedAuthorityFieldsAndQueryParameters(t *testing.T) {
	stub := &serviceStub{projection: testProjection()}
	mux := testMux(NewHandler(stub, fixedUserProvider{id: "current-user"}))

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
		"/api/personal-assistant/setup-journey?user_id=other", nil))
	if response.Code != http.StatusBadRequest || stub.lastOperation != "" {
		t.Fatalf("client-selected GET user was not rejected: status=%d stub=%#v", response.Code, stub)
	}

	for _, forbidden := range []string{
		`"user_id":"other"`, `"station_id":"station"`, `"source_url":"https://example.invalid"`,
		`"adapter":"custom"`, `"scope":"other"`, `"declaration":{}`, `"workspace_id":"foreign"`,
	} {
		stub.lastOperation = ""
		response = httptest.NewRecorder()
		body := `{"if_revision":7,"idempotency_key":"action-1","input":{` + forbidden + `}}`
		request := httptest.NewRequest(http.MethodPost,
			"/api/personal-assistant/setup-journey/runs/run-child/actions/install",
			strings.NewReader(body))
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || stub.lastOperation != "" {
			t.Fatalf("forbidden field %s: status=%d stub=%#v body=%s", forbidden, response.Code, stub, response.Body.String())
		}
	}

	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost,
		"/api/personal-assistant/setup-journey/open",
		strings.NewReader(`{"if_revision":7,"idempotency_key":"open-1","journey_id":"client-choice"}`))
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown top-level authority field status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestHandlerDelegatesOneClosedActionWithBoundedInput(t *testing.T) {
	stub := &serviceStub{projection: testProjection()}
	mux := testMux(NewHandler(stub, fixedUserProvider{id: "current-user"}))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost,
		"/api/personal-assistant/setup-journey/runs/run-child/actions/review_existing_project",
		strings.NewReader(`{"if_revision":7,"idempotency_key":"review-1","review_token":"token-1","input":{"candidate_id":"candidate-1"}}`))
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("action status = %d, body=%s", response.Code, response.Body.String())
	}
	if stub.lastOperation != "action" || stub.lastUserID != "current-user" || stub.lastRunID != "run-child" ||
		stub.lastActionID != setupjourney.ActionReviewExistingProject || stub.lastAction.IfRevision != 7 ||
		stub.lastAction.IdempotencyKey != "review-1" || string(stub.lastAction.Input) != `{"candidate_id":"candidate-1"}` {
		t.Fatalf("action envelope changed: %#v", stub)
	}

	stub.lastOperation = ""
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost,
		"/api/personal-assistant/setup-journey/runs/run-child/actions/client_adapter",
		strings.NewReader(`{"if_revision":7,"idempotency_key":"review-1"}`))
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || stub.lastOperation != "" {
		t.Fatalf("unknown action reached service: status=%d stub=%#v", response.Code, stub)
	}
}

func TestHandlerConflictReturnsFreshBoundedState(t *testing.T) {
	stub := &serviceStub{
		projection: testProjection(),
		openErr:    setupjourney.FailureFor(setupjourney.ReasonRevisionConflict, 7),
	}
	mux := testMux(NewHandler(stub, fixedUserProvider{id: "current-user"}))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost,
		"/api/personal-assistant/setup-journey/open",
		strings.NewReader(`{"if_revision":6,"idempotency_key":"open-stale"}`))
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Error   setupjourney.Failure            `json:"error"`
		Current *setupjourney.JourneyProjection `json:"current"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if body.Error.ReasonCode != setupjourney.ReasonRevisionConflict || body.Error.StateRevision != 7 ||
		body.Current == nil || body.Current.StateRevision != 7 {
		t.Fatalf("conflict body is not bounded fresh state: %#v", body)
	}
}

func TestHandlerNeverReturnsArbitraryServiceOrUserProviderErrors(t *testing.T) {
	secret := "/private/Music/song.rpp token=secret"
	cases := []struct {
		name     string
		provider fixedUserProvider
		service  *serviceStub
	}{
		{name: "provider", provider: fixedUserProvider{err: errors.New(secret)}, service: &serviceStub{projection: testProjection()}},
		{name: "service", provider: fixedUserProvider{id: "local"}, service: &serviceStub{projection: testProjection(), readErr: errors.New(secret)}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			testMux(NewHandler(testCase.service, testCase.provider)).ServeHTTP(response,
				httptest.NewRequest(http.MethodGet, "/api/personal-assistant/setup-journey", nil))
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "secret") ||
				strings.Contains(response.Body.String(), "song.rpp") {
				t.Fatalf("error leaked sensitive owner text: %s", response.Body.String())
			}
		})
	}
}
