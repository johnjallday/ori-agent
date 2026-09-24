package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReviewedKnowledgeMutationRejectsBrowserSuppliedOwnerSourceAndCandidateSeed(t *testing.T) {
	_, handler := newDailyBriefTestServer(t)
	for _, test := range []struct {
		path string
		body string
	}{
		{"/api/personal-assistant/knowledge/explicit", `{"state_version":1,"request_id":"test-1","category":"projects","text":"safe","source_kind":"file_janitor"}`},
		{"/api/personal-assistant/knowledge/explicit", `{"state_version":1,"request_id":"test-2","category":"projects","text":"safe","user_id":"foreign"}`},
		{"/api/personal-assistant/knowledge/explicit", `{"state_version":1,"request_id":"test-3","category":"projects","text":"safe","hq_workspace_id":"foreign"}`},
		{"/api/personal-assistant/knowledge/9df8b40b-fbc1-45db-8ea6-54a6f651e530/approve", `{"version":1,"request_id":"test-4","evidence":[{"source_id":"forged"}]}`},
		{"/api/personal-assistant/knowledge/9df8b40b-fbc1-45db-8ea6-54a6f651e530/edit-candidate", `{"version":1,"request_id":"test-5","text":"safe","destination":"global_profile"}`},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body)))
		if response.Code != http.StatusBadRequest || bytes.Contains(response.Body.Bytes(), []byte("foreign")) || bytes.Contains(response.Body.Bytes(), []byte("forged")) {
			t.Errorf("browser gained source/owner control on %s: %d %s", test.path, response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/knowledge/candidate", bytes.NewBufferString(`{"text":"seed"}`)))
	if response.Code != http.StatusNotFound {
		t.Fatalf("production registered arbitrary candidate-seed route: %d %s", response.Code, response.Body.String())
	}
}

func TestReviewedKnowledgeRecoveryRejectsBrowserSuppliedTextAndUnboundItems(t *testing.T) {
	_, handler := newDailyBriefTestServer(t)
	for _, action := range []string{"resume-forget", "resume-operation"} {
		path := "/api/personal-assistant/knowledge/760fda9c-a80f-4fe3-8875-1f34aa9b776e/" + action
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"text":"replace fact"}`)))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s accepted browser-supplied recovery text: %d %s", action, response.Code, response.Body.String())
		}
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code == http.StatusOK {
			t.Errorf("%s resumed an unbound item", action)
		}
	}
}
