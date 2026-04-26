package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunUISmokeCheckPassed(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/target" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("expected marker"))
	})

	check := runUISmokeCheck(handler, uiSmokeCheckSpec{
		name:             "Target",
		path:             "/target",
		expectedStatuses: []int{http.StatusOK},
		requiredSnippets: []string{"expected marker"},
	})

	if check.Status != "passed" {
		t.Fatalf("expected passed check, got %#v", check)
	}
	if check.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", check.StatusCode)
	}
}

func TestRunUISmokeCheckMissingContent(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("different body"))
	})

	check := runUISmokeCheck(handler, uiSmokeCheckSpec{
		name:             "Target",
		path:             "/target",
		expectedStatuses: []int{http.StatusOK},
		requiredSnippets: []string{"expected marker"},
	})

	if check.Status != "failed" {
		t.Fatalf("expected failed check, got %#v", check)
	}
	if !strings.Contains(check.Detail, "expected marker") {
		t.Fatalf("expected missing marker detail, got %q", check.Detail)
	}
}

func TestHandleUISmokeTestMethodNotAllowed(t *testing.T) {
	server := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/diagnostics/ui-smoke-test", nil)
	rr := httptest.NewRecorder()

	server.handleUISmokeTest(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}
