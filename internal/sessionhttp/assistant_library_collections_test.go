package sessionhttp

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCollectionChoice_RejectsAmbiguousConfirmationAndCallerAuthority(t *testing.T) {
	for _, tc := range []struct {
		body   string
		commit bool
	}{
		{`null`, false}, {`[]`, false}, {`{"expected_revision":1,"Expected_record_revision":1}`, false},
		{`{"expected_revision":1,"expected_revision":2}`, false}, {`{"expected_revision":1.5,"expected_record_revision":1}`, false},
		{`{"expected_revision":1,"expected_record_revision":1,"owner":"foreign"}`, false},
		{`{"review_token":"token","idempotency_key":"key","confirm":null}`, true},
		{`{"review_token":"token","idempotency_key":"key","confirm":"true"}`, true},
		{`{"review_token":"token","idempotency_key":"key","confirm":false}`, true},
		{`{"review_token":"token","idempotency_key":"key","confirm":true,"confirm":false}`, true},
		{`{"review_token":"token","idempotency_key":"key","confirm":true} {}`, true},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		if _, err := decodeCollectionChoice(httptest.NewRecorder(), r, tc.commit); err == nil {
			t.Fatal("ambiguous choice accepted", tc.body)
		}
	}
	for _, tc := range []struct {
		body   string
		commit bool
	}{
		{`{"expected_revision":1,"expected_record_revision":2}`, false},
		{`{"review_token":"token","idempotency_key":"key","confirm":true}`, true},
	} {
		if _, err := decodeCollectionChoice(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(tc.body)), tc.commit); err != nil {
			t.Fatal(err)
		}
	}
}
