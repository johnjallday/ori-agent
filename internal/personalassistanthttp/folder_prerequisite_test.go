package personalassistanthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type prerequisiteDigest struct {
	fakeFolderDigest
	calls int
	input personalassistant.FolderPrerequisiteInput
}

func (f *prerequisiteDigest) Prerequisite(_ context.Context, userID, offerID string, input personalassistant.FolderPrerequisiteInput) (personalassistant.FolderPrerequisiteReview, error) {
	if userID == "" || offerID != "offer-1" {
		return personalassistant.FolderPrerequisiteReview{}, personalassistant.ErrFolderOfferNotFound
	}
	f.calls++
	f.input = input
	return personalassistant.FolderPrerequisiteReview{Kind: input.Kind, Completed: input.Confirm}, nil
}
func TestFolderPrerequisiteHTTPRejectsSourcesPathsAndUnknownExecutionInput(t *testing.T) {
	for _, body := range []string{`{"kind":"provider","request_id":"review"}`, `{"kind":"integration","source":"untrusted"}`, `{"kind":"provider","path":"/private"}`, `{"kind":"integration","input":{"workspace":"new"}}`} {
		fake := &prerequisiteDigest{}
		handler := newFolderDigestHandler(&fake.fakeFolderDigest)
		handler.SetFolderDigest(fake)
		request := httptest.NewRequest(http.MethodPost, "/prerequisites", strings.NewReader(body))
		request.SetPathValue("offerID", "offer-1")
		response := httptest.NewRecorder()
		handler.ReviewFolderPrerequisite(response, request)
		valid := !strings.Contains(body, "source") && !strings.Contains(body, "path") && !strings.Contains(body, "input")
		if valid && (response.Code != 200 || fake.calls != 1 || fake.input.Confirm) {
			t.Fatal(response.Code, response.Body.String())
		}
		if !valid && (response.Code != 400 || fake.calls != 0) {
			t.Fatal("unknown input reached package service", response.Code, response.Body.String())
		}
	}
}
