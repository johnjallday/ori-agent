package pluginhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
)

type emptyAgentStore struct{}

func (s *emptyAgentStore) ListAgents() []string { return []string{} }
func (s *emptyAgentStore) CreateAgent(name string, config *store.CreateAgentConfig) error {
	return nil
}
func (s *emptyAgentStore) DeleteAgent(name string) error { return nil }
func (s *emptyAgentStore) GetAgent(name string) (*agent.Agent, bool) {
	return nil, false
}
func (s *emptyAgentStore) SetAgent(name string, ag *agent.Agent) error { return nil }
func (s *emptyAgentStore) UpdateAgent(name string, updateFn func(*agent.Agent) error) error {
	return nil
}
func (s *emptyAgentStore) ClearAgents() error { return nil }
func (s *emptyAgentStore) Save() error        { return nil }

func TestListAllPages_NoCurrentAgent_ReturnsEmptyPages(t *testing.T) {
	handler := &WebPageHandler{State: &emptyAgentStore{}}

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/all-pages", nil)
	w := httptest.NewRecorder()
	handler.ListAllPages(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var resp struct {
		Pages []map[string]any `json:"pages"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(resp.Pages) != 0 {
		t.Fatalf("Expected no pages, got %d", len(resp.Pages))
	}
}
