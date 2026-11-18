package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/web"
)

func TestWorkspaceDetailSmokeContractMatchesCurrentRenderer(t *testing.T) {
	renderer := web.NewTemplateRenderer()
	if err := renderer.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	html, err := renderer.RenderTemplate("workspace-detail", web.GetDefaultData())
	if err != nil {
		t.Fatal(err)
	}
	spec := workspaceDetailSmokeSpec("/workspaces/owned-fixture")
	check := func(body string) uiSmokeTestCheck {
		return runUISmokeCheck(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}), spec)
	}
	if result := check(html); result.Status != "passed" {
		t.Fatalf("current renderer: %#v", result)
	}
	// Keeping generic page text/controller references is not enough: removing
	// the actual command mount must still make this real contract fail.
	missing := strings.Replace(html, `id="workspaceCommandView"`, "", 1)
	if result := check(missing); result.Status != "failed" {
		t.Fatalf("missing mount: %#v", result)
	}
}

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
