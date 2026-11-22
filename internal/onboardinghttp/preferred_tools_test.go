package onboardinghttp

import (
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreferredToolsAreScopedAndStaleProfileCannotEraseThem(t *testing.T) {
	m := onboarding.NewManager(filepath.Join(t.TempDir(), "state.json"))
	h := &SmartOnboardingHandler{onboardingMgr: m}
	h.SetOnPersonalized(func() { t.Fatal("unexpected personalization completion") })
	req := func(fn http.HandlerFunc, method, body string) int {
		w := httptest.NewRecorder()
		fn(w, httptest.NewRequest(method, "/", strings.NewReader(body)))
		return w.Code
	}
	if got := req(h.AddPreferredTools, "GET", ""); got != 405 {
		t.Fatal(got)
	}
	if got := req(h.AddPreferredTools, "POST", `{"names":[""]}`); got != 400 {
		t.Fatal(got)
	}
	if got := req(h.AddPreferredTools, "POST", `{"names":["Example","example"]}`); got != 200 {
		t.Fatal(got)
	}
	if got := req(h.SavePersonalization, "POST", `{"base_preferred_tools":[],"description":"stale"}`); got != 409 {
		t.Fatal(got)
	}
	p := m.GetUserProfile()
	if len(p.PreferredTools) != 1 || p.Description != "" || !p.InferredAt.IsZero() || !p.PersonalizedAt.IsZero() || m.GetAssistantProgress().Experience != 0 {
		t.Fatalf("unexpected profile change %+v", p)
	}
}
