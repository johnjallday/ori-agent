package evolution

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

var (
	ErrNotLearnerStage = errors.New("agent must reach learner stage before selecting a path")
	ErrInvalidPath     = errors.New("invalid path")
	ErrAgentNotFound   = errors.New("agent not found")
)

const (
	defaultBaseMessageXP   int64         = 10
	defaultTokensPerXP     int           = 100
	defaultFeedXP          int64         = 25
	defaultDuplicateWindow time.Duration = 30 * time.Second
	defaultMaxXPPerHour    int64         = 200
	defaultXPPerLevel      int64         = 100
)

type AgentStore interface {
	GetAgent(name string) (*agent.Agent, bool)
	SetAgent(name string, ag *agent.Agent) error
	UpdateAgent(name string, updateFn func(*agent.Agent) error) error
}

type AssistantProgressStore interface {
	GetAssistantProgress() types.AssistantProgress
	SetAssistantProgress(progress *types.AssistantProgress) error
}

type ActivityLogger interface {
	LogActivity(agentName string, eventType types.ActivityEventType, details map[string]any, user string) error
}

type Config struct {
	BaseMessageXP   int64
	TokensPerXP     int
	FeedXP          int64
	DuplicateWindow time.Duration
	MaxXPPerHour    int64
	XPPerLevel      int64
}

type Service struct {
	mu                     sync.Mutex
	agentStore             AgentStore
	assistantProgressStore AssistantProgressStore
	cfg                    Config
	now                    func() time.Time
	recentByAgent          map[string]recentMessage
	hourlyByAgent          map[string]hourlyBucket
	intentByAgent          map[string]intentStats
	activityLogger         ActivityLogger
}

type recentMessage struct {
	fingerprint string
	timestamp   time.Time
}

type hourlyBucket struct {
	hourStart time.Time
	xpAwarded int64
}

type intentStats struct {
	counts map[string]int64
	total  int64
}

type Suggestion struct {
	Type             string          `json:"type"`
	Agent            string          `json:"agent"`
	Confidence       float64         `json:"confidence"`
	Reason           string          `json:"reason"`
	RequiresApproval bool            `json:"requires_approval"`
	RecommendedPath  types.AgentPath `json:"recommended_path,omitempty"`
}

func NewService(agentStore AgentStore, assistantProgressStore AssistantProgressStore, cfg *Config) *Service {
	resolved := Config{
		BaseMessageXP:   defaultBaseMessageXP,
		TokensPerXP:     defaultTokensPerXP,
		FeedXP:          defaultFeedXP,
		DuplicateWindow: defaultDuplicateWindow,
		MaxXPPerHour:    defaultMaxXPPerHour,
		XPPerLevel:      defaultXPPerLevel,
	}
	if cfg != nil {
		if cfg.BaseMessageXP > 0 {
			resolved.BaseMessageXP = cfg.BaseMessageXP
		}
		if cfg.TokensPerXP > 0 {
			resolved.TokensPerXP = cfg.TokensPerXP
		}
		if cfg.FeedXP > 0 {
			resolved.FeedXP = cfg.FeedXP
		}
		if cfg.DuplicateWindow > 0 {
			resolved.DuplicateWindow = cfg.DuplicateWindow
		}
		if cfg.MaxXPPerHour > 0 {
			resolved.MaxXPPerHour = cfg.MaxXPPerHour
		}
		if cfg.XPPerLevel > 0 {
			resolved.XPPerLevel = cfg.XPPerLevel
		}
	}

	return &Service{
		agentStore:             agentStore,
		assistantProgressStore: assistantProgressStore,
		cfg:                    resolved,
		now:                    time.Now,
		recentByAgent:          make(map[string]recentMessage),
		hourlyByAgent:          make(map[string]hourlyBucket),
		intentByAgent:          make(map[string]intentStats),
	}
}

func (s *Service) SetActivityLogger(activityLogger ActivityLogger) {
	if s == nil {
		return
	}
	s.activityLogger = activityLogger
}

// CleanupAgent removes all in-memory tracking state for a deleted agent,
// preventing unbounded map growth.
func (s *Service) CleanupAgent(agentName string) {
	if s == nil || agentName == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.recentByAgent, agentName)
	delete(s.hourlyByAgent, agentName)
	delete(s.intentByAgent, agentName)
}

// AwardMessageXP grants XP based on a successful message exchange.
func (s *Service) AwardMessageXP(agentName string, tokenCount int, userMessage string) error {
	if tokenCount < 0 {
		tokenCount = 0
	}
	xp := s.cfg.BaseMessageXP + int64(tokenCount/s.cfg.TokensPerXP)
	return s.awardXP(agentName, xp, userMessage, true, false)
}

// AwardFeedXP grants XP for a validated feed action.
func (s *Service) AwardFeedXP(agentName string, source string) error {
	return s.awardXP(agentName, s.cfg.FeedXP, source, false, true)
}

// EvaluateStageTransitions returns the expected stage for a given level.
func (s *Service) EvaluateStageTransitions(level int) types.AgentStage {
	return stageForLevel(level)
}

// SelectPath sets a specialization path for an agent once it reaches Learner stage.
func (s *Service) SelectPath(agentName string, requestedPath types.AgentPath) error {
	if s == nil {
		return fmt.Errorf("evolution service is nil")
	}
	if agentName == "" {
		return fmt.Errorf("agent name is required")
	}
	if s.agentStore == nil {
		return fmt.Errorf("agent store is not configured")
	}

	path, ok := normalizePath(requestedPath)
	if !ok {
		return fmt.Errorf("%w: %q", ErrInvalidPath, requestedPath)
	}

	var previousPath types.AgentPath

	err := s.agentStore.UpdateAgent(agentName, func(ag *agent.Agent) error {
		ag.InitializeEvolution()
		ag.Evolution.EnsureDefaults()
		ag.Evolution.Stage = stageForLevel(ag.Evolution.Level)
		if ag.Evolution.Level < 10 {
			return ErrNotLearnerStage
		}

		previousPath = ag.Evolution.Path
		ag.Evolution.Path = path
		ag.Evolution.UpdatedAt = s.now()
		return nil
	})

	if err != nil {
		return err
	}

	s.logActivity(agentName, types.ActivityEventEvolutionPath, map[string]any{
		"path":     string(path),
		"old_path": string(previousPath),
	})
	return nil
}

// GetSuggestions returns path and hatch/handoff suggestions for a specific agent.
func (s *Service) GetSuggestions(agentName string) ([]Suggestion, error) {
	if s == nil {
		return nil, fmt.Errorf("evolution service is nil")
	}
	if agentName == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if s.agentStore == nil {
		return nil, fmt.Errorf("agent store is not configured")
	}

	ag, found := s.agentStore.GetAgent(agentName)
	if !found || ag == nil {
		return nil, fmt.Errorf("agent %q not found", agentName)
	}
	ag.InitializeEvolution()
	ag.InitializeStatistics()
	ag.Evolution.EnsureDefaults()
	ag.Evolution.Stage = stageForLevel(ag.Evolution.Level)

	s.mu.Lock()
	intent := s.intentByAgent[agentName]
	s.mu.Unlock()

	topIntent, topCount, total := topIntent(intent)
	recommendedPath := pathForIntent(topIntent)
	confidence := confidenceFor(topCount, total)

	suggestions := make([]Suggestion, 0)

	if ag.Evolution.Level >= 10 && ag.Evolution.Path == "" && recommendedPath != "" && topCount >= 3 {
		suggestions = append(suggestions, Suggestion{
			Type:             "path_selection",
			Agent:            agentName,
			Confidence:       confidence,
			Reason:           fmt.Sprintf("Repeated %s-oriented requests suggest a %s path.", topIntent, recommendedPath),
			RequiresApproval: true,
			RecommendedPath:  recommendedPath,
		})
	}

	if ag.Evolution.Path != "" && recommendedPath != "" && recommendedPath != ag.Evolution.Path && topCount >= 3 && confidence >= 0.60 {
		suggestions = append(suggestions, Suggestion{
			Type:             "hatch_specialist",
			Agent:            agentName,
			Confidence:       confidence,
			Reason:           fmt.Sprintf("Current workload has shifted toward %s tasks; a specialist handoff may help.", topIntent),
			RequiresApproval: true,
			RecommendedPath:  recommendedPath,
		})
	}

	if len(suggestions) == 0 && ag.Statistics.MessageCount >= 30 && ag.Evolution.Level >= 2 {
		suggestions = append(suggestions, Suggestion{
			Type:             "hatch_specialist",
			Agent:            agentName,
			Confidence:       0.55,
			Reason:           "Sustained workload suggests a specialist handoff could improve throughput.",
			RequiresApproval: true,
		})
	}

	return suggestions, nil
}

func (s *Service) awardXP(agentName string, requestedXP int64, userMessage string, enableDuplicateCheck bool, incrementFeedCount bool) error {
	if s == nil {
		return fmt.Errorf("evolution service is nil")
	}
	if agentName == "" {
		return fmt.Errorf("agent name is required")
	}
	if s.agentStore == nil {
		return fmt.Errorf("agent store is not configured")
	}
	if requestedXP <= 0 {
		return nil
	}

	now := s.now()

	// Hold the lock only for in-memory anti-gaming checks.
	s.mu.Lock()
	if enableDuplicateCheck && s.isDuplicateMessageLocked(agentName, userMessage, now) {
		s.mu.Unlock()
		return nil
	}
	if enableDuplicateCheck {
		s.recordIntentLocked(agentName, userMessage)
	}
	awardXP := s.applyHourlyCapLocked(agentName, requestedXP, now)
	s.mu.Unlock()

	if awardXP <= 0 {
		return nil
	}

	var previousLevel int
	var previousStage types.AgentStage
	var newLevel int
	var newStage types.AgentStage
	var feedCount int64

	// Atomic update for agent evolution.
	err := s.agentStore.UpdateAgent(agentName, func(ag *agent.Agent) error {
		ag.InitializeEvolution()
		ag.Evolution.EnsureDefaults()

		previousLevel = ag.Evolution.Level
		previousStage = ag.Evolution.Stage

		ag.Evolution.Experience += awardXP
		ag.Evolution.Level = levelForExperience(ag.Evolution.Experience, s.cfg.XPPerLevel)
		ag.Evolution.Stage = stageForLevel(ag.Evolution.Level)

		newLevel = ag.Evolution.Level
		newStage = ag.Evolution.Stage

		if incrementFeedCount {
			ag.Evolution.FeedCount++
		}
		feedCount = ag.Evolution.FeedCount
		if ag.Evolution.Level != previousLevel || ag.Evolution.Stage != previousStage {
			ag.Evolution.LastEvolvedAt = now
		}
		ag.Evolution.UpdatedAt = now
		return nil
	})
	if err != nil {
		return fmt.Errorf("persisting agent evolution: %w", err)
	}

	if s.assistantProgressStore != nil {
		assistantProgress := s.assistantProgressStore.GetAssistantProgress()
		assistantProgress.EnsureDefaults()
		assistantProgress.Experience += awardXP
		assistantProgress.Level = levelForExperience(assistantProgress.Experience, s.cfg.XPPerLevel)
		assistantProgress.Rank = rankForLevel(assistantProgress.Level)
		assistantProgress.UpdatedAt = now
		if err := s.assistantProgressStore.SetAssistantProgress(&assistantProgress); err != nil {
			return fmt.Errorf("persisting assistant progress: %w", err)
		}
	}

	if incrementFeedCount {
		source := strings.TrimSpace(userMessage)
		if source == "" {
			source = "manual"
		}
		s.logActivity(agentName, types.ActivityEventEvolutionFeed, map[string]any{
			"source":     source,
			"awarded_xp": awardXP,
			"feed_count": feedCount,
		})
	}
	if previousStage != newStage {
		s.logActivity(agentName, types.ActivityEventEvolutionStage, map[string]any{
			"old_stage": string(previousStage),
			"new_stage": string(newStage),
			"old_level": previousLevel,
			"new_level": newLevel,
		})
	}

	return nil
}

func (s *Service) logActivity(agentName string, eventType types.ActivityEventType, details map[string]any) {
	if s == nil || s.activityLogger == nil {
		return
	}
	_ = s.activityLogger.LogActivity(agentName, eventType, details, "")
}

func (s *Service) isDuplicateMessageLocked(agentName, userMessage string, now time.Time) bool {
	normalized := normalizeMessage(userMessage)
	if normalized == "" {
		return false
	}

	last, exists := s.recentByAgent[agentName]
	s.recentByAgent[agentName] = recentMessage{
		fingerprint: normalized,
		timestamp:   now,
	}

	if !exists {
		return false
	}
	return last.fingerprint == normalized && now.Sub(last.timestamp) <= s.cfg.DuplicateWindow
}

func (s *Service) applyHourlyCapLocked(agentName string, requestedXP int64, now time.Time) int64 {
	if requestedXP <= 0 {
		return 0
	}

	hourStart := now.Truncate(time.Hour)
	bucket := s.hourlyByAgent[agentName]
	if bucket.hourStart != hourStart {
		bucket = hourlyBucket{hourStart: hourStart}
	}

	remaining := s.cfg.MaxXPPerHour - bucket.xpAwarded
	if remaining <= 0 {
		s.hourlyByAgent[agentName] = bucket
		return 0
	}

	awarded := requestedXP
	if awarded > remaining {
		awarded = remaining
	}

	bucket.xpAwarded += awarded
	s.hourlyByAgent[agentName] = bucket
	return awarded
}

func levelForExperience(experience, xpPerLevel int64) int {
	if xpPerLevel <= 0 || experience <= 0 {
		return 0
	}
	return int(experience / xpPerLevel)
}

func stageForLevel(level int) types.AgentStage {
	switch {
	case level >= 50:
		return types.AgentStageSentient
	case level >= 25:
		return types.AgentStageExpert
	case level >= 10:
		return types.AgentStageLearner
	case level >= 2:
		return types.AgentStageInfant
	default:
		return types.AgentStageSpark
	}
}

func rankForLevel(level int) string {
	switch {
	case level >= 50:
		return "legend"
	case level >= 25:
		return "strategist"
	case level >= 10:
		return "operator"
	case level >= 2:
		return "apprentice"
	default:
		return "novice"
	}
}

func normalizeMessage(message string) string {
	clean := strings.TrimSpace(strings.ToLower(message))
	if clean == "" {
		return ""
	}
	return strings.Join(strings.Fields(clean), " ")
}

func normalizePath(path types.AgentPath) (types.AgentPath, bool) {
	switch strings.TrimSpace(strings.ToLower(string(path))) {
	case string(types.AgentPathCoder):
		return types.AgentPathCoder, true
	case string(types.AgentPathResearcher):
		return types.AgentPathResearcher, true
	case string(types.AgentPathWriter):
		return types.AgentPathWriter, true
	default:
		return "", false
	}
}

func (s *Service) recordIntentLocked(agentName, userMessage string) {
	intent := classifyIntent(userMessage)
	if intent == "" {
		return
	}
	stats := s.intentByAgent[agentName]
	if stats.counts == nil {
		stats.counts = make(map[string]int64)
	}
	stats.counts[intent]++
	stats.total++
	s.intentByAgent[agentName] = stats
}

func classifyIntent(message string) string {
	text := normalizeMessage(message)
	if text == "" {
		return ""
	}

	codingKeywords := []string{"code", "bug", "refactor", "compile", "function", "test", "stacktrace", "api", "golang", "javascript"}
	researchKeywords := []string{"research", "compare", "find", "investigate", "source", "docs", "benchmark", "analyze"}
	writerKeywords := []string{"write", "draft", "email", "blog", "copy", "rewrite", "tone", "edit", "summarize"}

	scores := map[string]int64{
		"coding":   keywordScore(text, codingKeywords),
		"research": keywordScore(text, researchKeywords),
		"writer":   keywordScore(text, writerKeywords),
	}

	bestIntent := ""
	var bestScore int64
	for intent, score := range scores {
		if score > bestScore {
			bestScore = score
			bestIntent = intent
		}
	}
	if bestScore == 0 {
		return ""
	}
	return bestIntent
}

func keywordScore(text string, keywords []string) int64 {
	words := make(map[string]struct{})
	for _, w := range strings.Fields(text) {
		words[w] = struct{}{}
	}
	var score int64
	for _, keyword := range keywords {
		if _, ok := words[keyword]; ok {
			score++
		}
	}
	return score
}

func pathForIntent(intent string) types.AgentPath {
	switch intent {
	case "coding":
		return types.AgentPathCoder
	case "research":
		return types.AgentPathResearcher
	case "writer":
		return types.AgentPathWriter
	default:
		return ""
	}
}

func topIntent(stats intentStats) (string, int64, int64) {
	top := ""
	var topCount int64
	for intent, count := range stats.counts {
		if count > topCount {
			top = intent
			topCount = count
		}
	}
	return top, topCount, stats.total
}

func confidenceFor(topCount, total int64) float64 {
	if total <= 0 || topCount <= 0 {
		return 0
	}
	return float64(topCount) / float64(total)
}
