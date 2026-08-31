package personalassistanthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
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

func TestHandlerGetState_UserResolutionFailureDoesNotReadState(t *testing.T) {
	reader := &fakeStateReader{}
	handler := NewHandler(reader, fakeUserProvider{err: errors.New("identity unavailable")})
	recorder := httptest.NewRecorder()
	handler.GetState(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-assistant", nil))
	if recorder.Code != http.StatusInternalServerError || reader.reads != 0 {
		t.Fatalf("status=%d reads=%d", recorder.Code, reader.reads)
	}
}
