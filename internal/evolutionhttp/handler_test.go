package evolutionhttp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/evolution"
	"github.com/johnjallday/ori-agent/internal/types"
)

type fakeAgentStore struct {
	agents map[string]*agent.Agent
}

func (f *fakeAgentStore) ListAgents() []string {
	names := make([]string, 0, len(f.agents))
	for name := range f.agents {
		names = append(names, name)
	}
	return names
}

func (f *fakeAgentStore) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := f.agents[name]
	return ag, ok
}

func (f *fakeAgentStore) SetAgent(name string, ag *agent.Agent) error {
	f.agents[name] = ag
	return nil
}

type fakeAssistantProgressStore struct {
	progress types.AssistantProgress
}

func (f *fakeAssistantProgressStore) GetAssistantProgress() types.AssistantProgress {
	return f.progress
}

type fakeEvolutionService struct {
	store *fakeAgentStore
	err   error
	calls int
}

func (f *fakeEvolutionService) AwardFeedXP(agentName string, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.calls++
	ag, ok := f.store.GetAgent(agentName)
	if !ok || ag == nil {
		return nil
	}
	ag.InitializeEvolution()
	ag.Evolution.FeedCount++
	ag.Evolution.Experience += 25
	return f.store.SetAgent(agentName, ag)
}

func (f *fakeEvolutionService) SelectPath(agentName string, requestedPath types.AgentPath) error {
	ag, ok := f.store.GetAgent(agentName)
	if !ok || ag == nil {
		return fmt.Errorf("%w: %q", evolution.ErrAgentNotFound, agentName)
	}
	ag.InitializeEvolution()
	if ag.Evolution.Level < 10 {
		return evolution.ErrNotLearnerStage
	}
	switch requestedPath {
	case types.AgentPathCoder, types.AgentPathResearcher, types.AgentPathWriter:
	default:
		return fmt.Errorf("%w: %q", evolution.ErrInvalidPath, requestedPath)
	}
	ag.Evolution.Path = requestedPath
	return f.store.SetAgent(agentName, ag)
}

func (f *fakeEvolutionService) GetSuggestions(agentName string) ([]evolution.Suggestion, error) {
	ag, ok := f.store.GetAgent(agentName)
	if !ok || ag == nil {
		return nil, fmt.Errorf("not found")
	}
	ag.InitializeEvolution()
	if ag.Evolution.Level >= 10 && ag.Evolution.Path == "" {
		return []evolution.Suggestion{
			{
				Type:             "path_selection",
				Agent:            agentName,
				Confidence:       0.8,
				Reason:           "test suggestion",
				RequiresApproval: true,
				RecommendedPath:  types.AgentPathCoder,
			},
		}, nil
	}
	return []evolution.Suggestion{}, nil
}

func TestHandler_GetAssistantProgress(t *testing.T) {
	store := &fakeAgentStore{agents: map[string]*agent.Agent{}}
	assistantProgress := &fakeAssistantProgressStore{progress: types.AssistantProgress{
		Level:      3,
		Experience: 240,
		Rank:       "captain",
	}}
	service := &fakeEvolutionService{store: store}
	h := NewHandler(store, assistantProgress, service)

	req := httptest.NewRequest(http.MethodGet, "/api/evolution/assistant", nil)
	rr := httptest.NewRecorder()
	h.GetAssistantProgress(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	var progress types.AssistantProgress
	if err := json.Unmarshal(body["assistant"], &progress); err != nil {
		t.Fatalf("failed to decode user payload: %v", err)
	}
	if progress.Level != 3 {
		t.Errorf("expected assistant level 3, got %d", progress.Level)
	}
}

func TestHandler_GetAgentEvolution(t *testing.T) {
	store := &fakeAgentStore{
		agents: map[string]*agent.Agent{
			"alpha": {
				Type:     agent.TypeGeneral,
				Settings: types.Settings{Model: "gpt-4o-mini", Temperature: 1},
			},
		},
	}
	assistantProgress := &fakeAssistantProgressStore{progress: *types.NewAssistantProgress()}
	service := &fakeEvolutionService{store: store}
	h := NewHandler(store, assistantProgress, service)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/alpha/evolution", nil)
	rr := httptest.NewRecorder()
	h.GetAgentEvolution(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	var evolution types.AgentEvolution
	if err := json.Unmarshal(body["evolution"], &evolution); err != nil {
		t.Fatalf("failed to decode evolution payload: %v", err)
	}
	if evolution.Stage != types.AgentStageSpark {
		t.Errorf("expected default stage spark, got %q", evolution.Stage)
	}
}

func TestHandler_FeedAgent(t *testing.T) {
	store := &fakeAgentStore{
		agents: map[string]*agent.Agent{
			"alpha": {
				Type:     agent.TypeGeneral,
				Settings: types.Settings{Model: "gpt-4o-mini", Temperature: 1},

				Evolution: types.NewAgentEvolution(),
			},
		},
	}
	assistantProgress := &fakeAssistantProgressStore{progress: *types.NewAssistantProgress()}
	service := &fakeEvolutionService{store: store}
	h := NewHandler(store, assistantProgress, service)

	reqBody := []byte(`{"content":"project context","source":"manual"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/alpha/feed", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.FeedAgent(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if service.calls != 1 {
		t.Fatalf("expected AwardFeedXP to be called once, got %d", service.calls)
	}
	if store.agents["alpha"].Evolution.FeedCount != 1 {
		t.Errorf("expected feed count 1, got %d", store.agents["alpha"].Evolution.FeedCount)
	}
}

func TestHandler_FeedAgent_ValidatesContent(t *testing.T) {
	store := &fakeAgentStore{agents: map[string]*agent.Agent{"alpha": {}}}
	assistantProgress := &fakeAssistantProgressStore{progress: *types.NewAssistantProgress()}
	service := &fakeEvolutionService{store: store}
	h := NewHandler(store, assistantProgress, service)

	reqBody := []byte(`{"content":"   "}`)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/alpha/feed", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.FeedAgent(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_FeedAgent_MissingAgent(t *testing.T) {
	store := &fakeAgentStore{agents: map[string]*agent.Agent{}}
	assistantProgress := &fakeAssistantProgressStore{progress: *types.NewAssistantProgress()}
	service := &fakeEvolutionService{store: store}
	h := NewHandler(store, assistantProgress, service)

	reqBody := []byte(`{"content":"project context"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/missing/feed", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.FeedAgent(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}
}

func TestHandler_GetSuggestions(t *testing.T) {
	stats := types.NewAgentStatistics()
	stats.MessageCount = 35
	store := &fakeAgentStore{agents: map[string]*agent.Agent{
		"alpha": {
			Type:     agent.TypeGeneral,
			Settings: types.Settings{Model: "gpt-4o-mini", Temperature: 1},

			Evolution:  &types.AgentEvolution{Level: 12},
			Statistics: stats,
		},
	}}
	assistantProgress := &fakeAssistantProgressStore{progress: *types.NewAssistantProgress()}
	service := &fakeEvolutionService{store: store}
	h := NewHandler(store, assistantProgress, service)

	req := httptest.NewRequest(http.MethodGet, "/api/evolution/suggestions", nil)
	rr := httptest.NewRecorder()
	h.GetSuggestions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode suggestions response: %v", err)
	}
	var suggestions []map[string]any
	if err := json.Unmarshal(payload["suggestions"], &suggestions); err != nil {
		t.Fatalf("failed to decode suggestions payload: %v", err)
	}
	if len(suggestions) == 0 {
		t.Fatal("expected at least one suggestion")
	}
	if suggestions[0]["confidence"] == nil || suggestions[0]["reason"] == nil {
		t.Fatal("expected suggestion to include confidence and reason")
	}
}

func TestHandler_SetAgentPath(t *testing.T) {
	store := &fakeAgentStore{agents: map[string]*agent.Agent{
		"alpha": {
			Type:     agent.TypeGeneral,
			Settings: types.Settings{Model: "gpt-4o-mini", Temperature: 1},

			Evolution: &types.AgentEvolution{Level: 10, Stage: types.AgentStageLearner},
		},
	}}
	assistantProgress := &fakeAssistantProgressStore{progress: *types.NewAssistantProgress()}
	service := &fakeEvolutionService{store: store}
	h := NewHandler(store, assistantProgress, service)

	reqBody := []byte(`{"path":"coder"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/alpha/evolution/path", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.SetAgentPath(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if store.agents["alpha"].Evolution.Path != types.AgentPathCoder {
		t.Fatalf("expected path coder, got %q", store.agents["alpha"].Evolution.Path)
	}
}

func TestHandler_SetAgentPath_GatesLearnerStage(t *testing.T) {
	store := &fakeAgentStore{agents: map[string]*agent.Agent{
		"alpha": {
			Type:     agent.TypeGeneral,
			Settings: types.Settings{Model: "gpt-4o-mini", Temperature: 1},

			Evolution: &types.AgentEvolution{Level: 2, Stage: types.AgentStageInfant},
		},
	}}
	assistantProgress := &fakeAssistantProgressStore{progress: *types.NewAssistantProgress()}
	service := &fakeEvolutionService{store: store}
	h := NewHandler(store, assistantProgress, service)

	reqBody := []byte(`{"path":"coder"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/agents/alpha/evolution/path", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.SetAgentPath(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rr.Code)
	}
}
