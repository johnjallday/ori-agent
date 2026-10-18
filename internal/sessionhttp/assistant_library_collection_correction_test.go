package sessionhttp

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCollectionCorrectionRequest_RequiresExactTypedIntentWithoutApproval(t *testing.T) {
	for _, body := range []string{
		`{"review_token":"token","operation":0,"name":"Corrected"}`,
		`{"review_token":"token","operation":1,"exclude_ids":["entry-a"]}`,
		`{"review_token":"token","operation":0,"add_ids":["entry-b"]}`,
		`{"review_token":"token","operation":0,"convert":"select_activation"}`,
		`{"review_token":"token","operation":1,"convert":"catalog_only"}`,
		`{"review_token":"token","operation":0,"resolve_question":2,"resolution":"unchanged"}`,
		`{"review_token":"token","operation":0,"release_unchanged":true}`,
	} {
		if _, err := decodeCollectionCorrection(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(body))); err != nil {
			t.Fatal(err)
		}
	}
	for _, body := range []string{
		`{"review_token":"token","operation":null,"name":"Corrected"}`,
		`{"review_token":"token","operation":0,"name":null}`,
		`{"review_token":"token","operation":0,"name":"A","name":"B"}`,
		`{"review_token":"token","Operation":0,"name":"A"}`,
		`{"review_token":"token","operation":0,"exclude_ids":null}`,
		`{"review_token":"token","operation":0,"exclude_ids":[true]}`,
		`{"review_token":"token","operation":0,"add_ids":null}`,
		`{"review_token":"token","operation":0,"add_ids":[true]}`,
		`{"review_token":"token","operation":0,"add_ids":[],"add_ids":[]}`,
		`{"review_token":"token","operation":0,"confirm":true}`,
		`{"review_token":"token","operation":0,"owner":"foreign"}`,
		`{"review_token":"token","operation":0} {}`,
		`{"review_token":"token","operation":0,"release_unchanged":false}`,
		`{"review_token":"token","operation":0,"release_unchanged":null}`,
		`{"review_token":"token","operation":0,"resolve_question":"0","resolution":"dismiss"}`,
		`{"review_token":"token","operation":0,"convert":1}`,
		`{"review_token":"token","operation":0,"convert":"catalog_only","convert":"catalog_only"}`,
	} {
		if _, err := decodeCollectionCorrection(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(body))); err == nil {
			t.Fatal("ambiguous correction accepted", body)
		}
	}
}
