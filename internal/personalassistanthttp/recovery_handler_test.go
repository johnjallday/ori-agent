package personalassistanthttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type recoveryServiceStub struct {
	state     *personalassistant.State
	err       error
	calls     int
	userID    string
	ifVersion int64
}

func (s *recoveryServiceStub) Repair(_ context.Context, userID string, ifVersion int64) (*personalassistant.State, error) {
	s.calls++
	s.userID = userID
	s.ifVersion = ifVersion
	return s.state, s.err
}

type recoveryStateReaderStub struct {
	projection *personalassistant.Projection
	err        error
}

func (s recoveryStateReaderStub) Get(context.Context, string) (*personalassistant.Projection, error) {
	return s.projection, s.err
}

func TestRepairReconnectsServerOwnedCandidate(t *testing.T) {
	recovery := &recoveryServiceStub{state: &personalassistant.State{AssistantID: "assistant-a"}}
	handler := NewHandler(recoveryStateReaderStub{projection: &personalassistant.Projection{
		State: personalassistant.APIStatePaused, StateVersion: 1,
		AssistantID: "assistant-a", DisplayName: "Assistant",
		NextAction: personalassistant.NextActionResume,
	}}, fakeUserProvider{userID: "local"})
	handler.SetRecoveryService(recovery)

	req := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/repair", strings.NewReader(`{"if_version":0}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.Repair(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if recovery.calls != 1 || recovery.userID != "local" || recovery.ifVersion != 0 {
		t.Fatalf("recovery call = %#v", recovery)
	}
	if !strings.Contains(rec.Body.String(), `"state":"paused"`) ||
		!strings.Contains(rec.Body.String(), `"assistant_id":"assistant-a"`) {
		t.Fatalf("response = %s", rec.Body.String())
	}
}

func TestRepairRequestRequiresOnlyExplicitVersionZero(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing version", body: `{}`},
		{name: "null version", body: `{"if_version":null}`},
		{name: "client selected identities", body: `{"if_version":0,"assistant_id":"foreign","workspace_id":"foreign"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recovery := &recoveryServiceStub{}
			handler := NewHandler(recoveryStateReaderStub{}, fakeUserProvider{userID: "local"})
			handler.SetRecoveryService(recovery)

			req := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/repair", strings.NewReader(test.body))
			rec := httptest.NewRecorder()
			handler.Repair(rec, req)

			if rec.Code != http.StatusBadRequest || recovery.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", rec.Code, recovery.calls, rec.Body.String())
			}
		})
	}
}

func TestRepairFailsClosedForMissingOrContradictoryEvidence(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "no evidence", err: personalassistant.ErrNotFound, code: "recovery_not_available"},
		{name: "contradictory evidence", err: personalassistant.ErrRepairNeeded, code: "recovery_blocked"},
		{name: "relationship changed", err: personalassistant.ErrConflict, code: "recovery_conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recovery := &recoveryServiceStub{err: test.err}
			handler := NewHandler(recoveryStateReaderStub{}, fakeUserProvider{userID: "local"})
			handler.SetRecoveryService(recovery)
			req := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/repair",
				strings.NewReader(`{"if_version":0}`))
			rec := httptest.NewRecorder()
			handler.Repair(rec, req)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRepairReportsPostWriteProjectionFailureWithoutSecondMutation(t *testing.T) {
	recovery := &recoveryServiceStub{state: &personalassistant.State{AssistantID: "assistant-a"}}
	handler := NewHandler(recoveryStateReaderStub{err: errors.New("read failed")}, fakeUserProvider{userID: "local"})
	handler.SetRecoveryService(recovery)
	req := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/repair",
		strings.NewReader(`{"if_version":0}`))
	rec := httptest.NewRecorder()
	handler.Repair(rec, req)
	if rec.Code != http.StatusServiceUnavailable || recovery.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", rec.Code, recovery.calls, rec.Body.String())
	}
}
