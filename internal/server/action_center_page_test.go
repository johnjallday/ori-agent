package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/web"
)

type actionCenterPageAgentStore struct{ store.Store }

func (actionCenterPageAgentStore) GetAgent(string) (*agent.Agent, bool) { return nil, false }
func (actionCenterPageAgentStore) ListAgents() []string                 { return nil }

func TestActionCenterPageKeepsOriBrandSeparateFromDocumentTitle(t *testing.T) {
	renderer := web.NewTemplateRenderer()
	if err := renderer.LoadTemplates(); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Storage: &StorageSystemFacade{
			AgentStore:    actionCenterPageAgentStore{},
			OnboardingMgr: onboarding.NewManager(filepath.Join(t.TempDir(), "onboarding.json")),
		},
		UI: &UISystemFacade{TemplateRenderer: renderer},
	}
	recorder := httptest.NewRecorder()
	s.serveActionCenter(recorder, httptest.NewRequest(http.MethodGet, "/action-center", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	html := recorder.Body.String()
	brand := regexp.MustCompile(`(?s)<a class="navbar-brand[^>]*>(.*?)</a>`).FindStringSubmatch(html)
	if len(brand) != 2 {
		t.Fatal("missing native navbar brand link")
	}
	text := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(brand[1], " ")
	if got := strings.Join(strings.Fields(text), " "); got != "Ori Agent" {
		t.Fatalf("navbar brand = %q, want Ori Agent", got)
	}
	if !strings.Contains(html, "<title>Action Center - Ori Agent</title>") {
		t.Fatal("page identity must remain in the document title")
	}
}
