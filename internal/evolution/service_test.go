package evolution

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

type fakeAgentStore struct {
	agents map[string]*agent.Agent
}

func (f *fakeAgentStore) GetAgent(name string) (*agent.Agent, bool) {
	ag, ok := f.agents[name]
	return ag, ok
}

func (f *fakeAgentStore) SetAgent(name string, ag *agent.Agent) error {
	f.agents[name] = ag
	return nil
}

func (f *fakeAgentStore) UpdateAgent(name string, updateFn func(*agent.Agent) error) error {
	ag, ok := f.agents[name]
	if !ok || ag == nil {
		return ErrAgentNotFound
	}
	return updateFn(ag)
}

type fakeAssistantProgressStore struct {
	progress types.AssistantProgress
}

func (f *fakeAssistantProgressStore) GetAssistantProgress() types.AssistantProgress {
	return f.progress
}

func (f *fakeAssistantProgressStore) SetAssistantProgress(progress *types.AssistantProgress) error {
	if progress == nil {
		f.progress = *types.NewAssistantProgress()
		return nil
	}
	f.progress = *progress
	return nil
}

type fakeActivityLogger struct {
	events []types.ActivityEventType
	detail []map[string]any
}

func (f *fakeActivityLogger) LogActivity(_ string, eventType types.ActivityEventType, details map[string]any, _ string) error {
	f.events = append(f.events, eventType)
	f.detail = append(f.detail, details)
	return nil
}

func newTestService(cfg *Config) (*Service, *fakeAgentStore, *fakeAssistantProgressStore) {
	agentStore := &fakeAgentStore{
		agents: map[string]*agent.Agent{
			"alpha": {
				Type:     agent.TypeGeneral,
				Settings: types.Settings{Model: "gpt-4o-mini", Temperature: 1.0},
			},
		},
	}
	assistantProgressStore := &fakeAssistantProgressStore{
		progress: *types.NewAssistantProgress(),
	}
	svc := NewService(agentStore, assistantProgressStore, cfg)
	return svc, agentStore, assistantProgressStore
}

func TestService_AwardMessageXP_BaseAndTokenBonus(t *testing.T) {
	svc, agentStore, assistantProgressStore := newTestService(nil)

	if err := svc.AwardMessageXP("alpha", 250, "Analyze this data"); err != nil {
		t.Fatalf("AwardMessageXP() failed: %v", err)
	}

	got := agentStore.agents["alpha"]
	if got.Evolution == nil {
		t.Fatal("expected evolution to be initialized")
	}
	if got.Evolution.Experience != 12 {
		t.Errorf("expected 12 XP (10 base + 2 token bonus), got %d", got.Evolution.Experience)
	}
	if got.Evolution.Level != 0 {
		t.Errorf("expected level 0, got %d", got.Evolution.Level)
	}
	if got.Evolution.Stage != types.AgentStageSpark {
		t.Errorf("expected stage %q, got %q", types.AgentStageSpark, got.Evolution.Stage)
	}
	if assistantProgressStore.progress.Experience != 12 {
		t.Errorf("expected assistant XP 12, got %d", assistantProgressStore.progress.Experience)
	}
}

func TestService_AwardMessageXP_DuplicateSuppression(t *testing.T) {
	svc, agentStore, _ := newTestService(nil)

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	if err := svc.AwardMessageXP("alpha", 0, "Repeated Message"); err != nil {
		t.Fatalf("first award failed: %v", err)
	}

	now = now.Add(5 * time.Second)
	if err := svc.AwardMessageXP("alpha", 0, " repeated   message "); err != nil {
		t.Fatalf("duplicate award failed: %v", err)
	}

	if agentStore.agents["alpha"].Evolution.Experience != 10 {
		t.Errorf("expected duplicate message to be suppressed, got XP %d", agentStore.agents["alpha"].Evolution.Experience)
	}

	now = now.Add(40 * time.Second)
	if err := svc.AwardMessageXP("alpha", 0, "repeated message"); err != nil {
		t.Fatalf("third award failed: %v", err)
	}

	if agentStore.agents["alpha"].Evolution.Experience != 20 {
		t.Errorf("expected message outside duplicate window to award XP, got %d", agentStore.agents["alpha"].Evolution.Experience)
	}
}

func TestService_AwardMessageXP_PerHourCap(t *testing.T) {
	svc, agentStore, assistantProgressStore := newTestService(&Config{
		BaseMessageXP: 10,
		MaxXPPerHour:  15,
	})

	now := time.Date(2026, 2, 7, 13, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	if err := svc.AwardMessageXP("alpha", 0, "msg one"); err != nil {
		t.Fatalf("first award failed: %v", err)
	}
	if err := svc.AwardMessageXP("alpha", 0, "msg two"); err != nil {
		t.Fatalf("second award failed: %v", err)
	}
	if err := svc.AwardMessageXP("alpha", 0, "msg three"); err != nil {
		t.Fatalf("third award failed: %v", err)
	}

	if agentStore.agents["alpha"].Evolution.Experience != 15 {
		t.Errorf("expected hourly XP cap to clamp at 15, got %d", agentStore.agents["alpha"].Evolution.Experience)
	}
	if assistantProgressStore.progress.Experience != 15 {
		t.Errorf("expected assistant XP to respect hourly cap 15, got %d", assistantProgressStore.progress.Experience)
	}

	now = now.Add(61 * time.Minute)
	if err := svc.AwardMessageXP("alpha", 0, "new hour message"); err != nil {
		t.Fatalf("new hour award failed: %v", err)
	}
	if agentStore.agents["alpha"].Evolution.Experience != 25 {
		t.Errorf("expected cap to reset next hour, got XP %d", agentStore.agents["alpha"].Evolution.Experience)
	}
}

func TestService_AwardMessageXP_StageTransitions(t *testing.T) {
	svc, agentStore, _ := newTestService(&Config{
		BaseMessageXP: 10,
		XPPerLevel:    10,
		MaxXPPerHour:  200,
	})

	if err := svc.AwardMessageXP("alpha", 0, "first"); err != nil {
		t.Fatalf("first award failed: %v", err)
	}
	if err := svc.AwardMessageXP("alpha", 0, "second"); err != nil {
		t.Fatalf("second award failed: %v", err)
	}

	evolution := agentStore.agents["alpha"].Evolution
	if evolution.Level != 2 {
		t.Errorf("expected level 2 after 20 XP with 10 XP/level, got %d", evolution.Level)
	}
	if evolution.Stage != types.AgentStageInfant {
		t.Errorf("expected stage %q at level 2, got %q", types.AgentStageInfant, evolution.Stage)
	}
	if evolution.LastEvolvedAt.IsZero() {
		t.Error("expected LastEvolvedAt to be set after stage/level transition")
	}
}

func TestService_AwardFeedXP_IncrementsFeedCount(t *testing.T) {
	svc, agentStore, assistantProgressStore := newTestService(nil)
	logSink := &fakeActivityLogger{}
	svc.SetActivityLogger(logSink)

	if err := svc.AwardFeedXP("alpha", "manual"); err != nil {
		t.Fatalf("AwardFeedXP() failed: %v", err)
	}

	evolution := agentStore.agents["alpha"].Evolution
	if evolution == nil {
		t.Fatal("expected evolution to be initialized")
	}
	if evolution.FeedCount != 1 {
		t.Errorf("expected feed_count 1, got %d", evolution.FeedCount)
	}
	if evolution.Experience != 25 {
		t.Errorf("expected feed XP 25, got %d", evolution.Experience)
	}
	if assistantProgressStore.progress.Experience != 25 {
		t.Errorf("expected assistant XP 25, got %d", assistantProgressStore.progress.Experience)
	}
	if len(logSink.events) != 1 || logSink.events[0] != types.ActivityEventEvolutionFeed {
		t.Fatalf("expected one evolution feed activity log, got %v", logSink.events)
	}
}

func TestService_SelectPath_GatesBeforeLearner(t *testing.T) {
	svc, _, _ := newTestService(nil)

	err := svc.SelectPath("alpha", types.AgentPathCoder)
	if err == nil {
		t.Fatal("expected path selection to fail before learner stage")
	}
}

func TestService_SelectPath_SucceedsAtLearner(t *testing.T) {
	svc, agentStore, _ := newTestService(nil)
	logSink := &fakeActivityLogger{}
	svc.SetActivityLogger(logSink)

	ag := agentStore.agents["alpha"]
	ag.InitializeEvolution()
	ag.Evolution.Level = 10
	ag.Evolution.Stage = types.AgentStageLearner
	_ = agentStore.SetAgent("alpha", ag)

	if err := svc.SelectPath("alpha", types.AgentPathResearcher); err != nil {
		t.Fatalf("SelectPath() failed: %v", err)
	}

	updated := agentStore.agents["alpha"]
	if updated.Evolution.Path != types.AgentPathResearcher {
		t.Errorf("expected path %q, got %q", types.AgentPathResearcher, updated.Evolution.Path)
	}
	if len(logSink.events) != 1 || logSink.events[0] != types.ActivityEventEvolutionPath {
		t.Fatalf("expected one evolution path activity log, got %v", logSink.events)
	}
}

func TestService_SelectPath_RejectsInvalidPath(t *testing.T) {
	svc, agentStore, _ := newTestService(nil)
	ag := agentStore.agents["alpha"]
	ag.InitializeEvolution()
	ag.Evolution.Level = 10
	_ = agentStore.SetAgent("alpha", ag)

	if err := svc.SelectPath("alpha", types.AgentPath("invalid")); err == nil {
		t.Fatal("expected invalid path error")
	}
}

func TestService_GetSuggestions_PathSelectionFromIntentPatterns(t *testing.T) {
	svc, agentStore, _ := newTestService(nil)

	ag := agentStore.agents["alpha"]
	ag.InitializeEvolution()
	ag.InitializeStatistics()
	ag.Evolution.Level = 12
	ag.Evolution.Experience = 1200
	ag.Statistics.MessageCount = 12
	_ = agentStore.SetAgent("alpha", ag)

	_ = svc.AwardMessageXP("alpha", 50, "Please debug this code bug in the function")
	_ = svc.AwardMessageXP("alpha", 50, "Refactor the api handler and add tests")
	_ = svc.AwardMessageXP("alpha", 50, "Fix compile error and code issue")

	suggestions, err := svc.GetSuggestions("alpha")
	if err != nil {
		t.Fatalf("GetSuggestions() failed: %v", err)
	}
	if len(suggestions) == 0 {
		t.Fatal("expected at least one suggestion")
	}
	if suggestions[0].Type != "path_selection" {
		t.Fatalf("expected first suggestion type path_selection, got %q", suggestions[0].Type)
	}
	if suggestions[0].RecommendedPath != types.AgentPathCoder {
		t.Fatalf("expected recommended path coder, got %q", suggestions[0].RecommendedPath)
	}
	if !suggestions[0].RequiresApproval {
		t.Fatal("expected suggestion to require approval")
	}
}

func TestService_GetSuggestions_HandoffWhenIntentShifts(t *testing.T) {
	svc, agentStore, _ := newTestService(nil)

	ag := agentStore.agents["alpha"]
	ag.InitializeEvolution()
	ag.InitializeStatistics()
	ag.Evolution.Level = 20
	ag.Evolution.Experience = 2000
	ag.Evolution.Path = types.AgentPathCoder
	ag.Statistics.MessageCount = 40
	_ = agentStore.SetAgent("alpha", ag)

	_ = svc.AwardMessageXP("alpha", 0, "Research and compare these approaches with sources")
	_ = svc.AwardMessageXP("alpha", 0, "Investigate docs and benchmark options")
	_ = svc.AwardMessageXP("alpha", 0, "Find and analyze authoritative sources")

	suggestions, err := svc.GetSuggestions("alpha")
	if err != nil {
		t.Fatalf("GetSuggestions() failed: %v", err)
	}

	foundHandoff := false
	for _, suggestion := range suggestions {
		if suggestion.Type == "hatch_specialist" && suggestion.RecommendedPath == types.AgentPathResearcher {
			foundHandoff = true
		}
	}
	if !foundHandoff {
		t.Fatal("expected hatch_specialist suggestion for researcher path")
	}
}

func TestService_AwardMessageXP_LogsStageTransition(t *testing.T) {
	svc, _, _ := newTestService(&Config{
		BaseMessageXP: 10,
		XPPerLevel:    10,
		MaxXPPerHour:  200,
	})
	logSink := &fakeActivityLogger{}
	svc.SetActivityLogger(logSink)

	if err := svc.AwardMessageXP("alpha", 0, "first"); err != nil {
		t.Fatalf("first award failed: %v", err)
	}
	if err := svc.AwardMessageXP("alpha", 0, "second"); err != nil {
		t.Fatalf("second award failed: %v", err)
	}

	found := false
	for _, eventType := range logSink.events {
		if eventType == types.ActivityEventEvolutionStage {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected stage transition activity log, got %v", logSink.events)
	}
}
