package evolutionhttp

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/evolution"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/types"
)

type AgentStore interface {
	ListAgents() (names []string, current string)
	GetAgent(name string) (*agent.Agent, bool)
	SetAgent(name string, ag *agent.Agent) error
}

type AssistantProgressStore interface {
	GetAssistantProgress() types.AssistantProgress
}

type EvolutionService interface {
	AwardFeedXP(agentName string, source string) error
	SelectPath(agentName string, requestedPath types.AgentPath) error
	GetSuggestions(agentName string) ([]evolution.Suggestion, error)
}

type Handler struct {
	agentStore             AgentStore
	assistantProgressStore AssistantProgressStore
	evolutionService       EvolutionService
}

func NewHandler(agentStore AgentStore, assistantProgressStore AssistantProgressStore, evolutionService EvolutionService) *Handler {
	return &Handler{
		agentStore:             agentStore,
		assistantProgressStore: assistantProgressStore,
		evolutionService:       evolutionService,
	}
}

func (h *Handler) GetAssistantProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.assistantProgressStore == nil {
		orihttp.RespondError(w, http.StatusServiceUnavailable, "assistant progression unavailable")
		return
	}

	progress := h.assistantProgressStore.GetAssistantProgress()
	progress.EnsureDefaults()
	orihttp.WriteJSON(w, map[string]any{
		"assistant": progress,
	})
}

func (h *Handler) GetAgentEvolution(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.agentStore == nil {
		orihttp.RespondError(w, http.StatusServiceUnavailable, "agent store unavailable")
		return
	}

	agentName, ok := extractAgentNameFromPath(r.URL.Path, "/evolution")
	if !ok {
		orihttp.BadRequest(w, "invalid agent evolution path")
		return
	}

	ag, found := h.agentStore.GetAgent(agentName)
	if !found || ag == nil {
		orihttp.NotFound(w, "agent not found")
		return
	}

	needsSave := ag.Evolution == nil
	ag.InitializeEvolution()
	if needsSave {
		if err := h.agentStore.SetAgent(agentName, ag); err != nil {
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "failed to persist evolution defaults", err)
			return
		}
	}

	orihttp.WriteJSON(w, map[string]any{
		"agent":     agentName,
		"evolution": ag.Evolution,
	})
}

func (h *Handler) FeedAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.agentStore == nil || h.evolutionService == nil {
		orihttp.RespondError(w, http.StatusServiceUnavailable, "evolution feed unavailable")
		return
	}

	agentName, ok := extractAgentNameFromPath(r.URL.Path, "/feed")
	if !ok {
		orihttp.BadRequest(w, "invalid feed path")
		return
	}

	var req struct {
		Content  string                 `json:"content"`
		Source   string                 `json:"source,omitempty"`
		Metadata map[string]interface{} `json:"metadata,omitempty"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		orihttp.BadRequest(w, "content is required")
		return
	}

	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "manual"
	}

	if _, found := h.agentStore.GetAgent(agentName); !found {
		orihttp.NotFound(w, "agent not found")
		return
	}

	if err := h.evolutionService.AwardFeedXP(agentName, source); err != nil {
		orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "failed to award feed XP", err)
		return
	}

	ag, found := h.agentStore.GetAgent(agentName)
	if !found || ag == nil {
		orihttp.NotFound(w, "agent not found")
		return
	}
	ag.InitializeEvolution()

	orihttp.WriteJSON(w, map[string]any{
		"success":   true,
		"agent":     agentName,
		"evolution": ag.Evolution,
		"fed_at":    time.Now().UTC(),
	})
}

func (h *Handler) SetAgentPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.agentStore == nil || h.evolutionService == nil {
		orihttp.RespondError(w, http.StatusServiceUnavailable, "path selection unavailable")
		return
	}

	agentName, ok := extractAgentNameFromPath(r.URL.Path, "/evolution/path")
	if !ok {
		orihttp.BadRequest(w, "invalid evolution path route")
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	path := types.AgentPath(strings.TrimSpace(strings.ToLower(req.Path)))
	if path == "" {
		orihttp.BadRequest(w, "path is required")
		return
	}

	if err := h.evolutionService.SelectPath(agentName, path); err != nil {
		switch {
		case errors.Is(err, evolution.ErrNotLearnerStage):
			orihttp.BadRequest(w, err.Error())
		case errors.Is(err, evolution.ErrInvalidPath):
			orihttp.BadRequest(w, err.Error())
		case errors.Is(err, evolution.ErrAgentNotFound):
			orihttp.NotFound(w, "agent not found")
		default:
			orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "failed to select path", err)
		}
		return
	}

	ag, found := h.agentStore.GetAgent(agentName)
	if !found || ag == nil {
		orihttp.NotFound(w, "agent not found")
		return
	}
	ag.InitializeEvolution()

	orihttp.WriteJSON(w, map[string]any{
		"success":   true,
		"agent":     agentName,
		"evolution": ag.Evolution,
	})
}

func (h *Handler) GetSuggestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	suggestions := make([]map[string]any, 0)
	if h.agentStore != nil {
		names, _ := h.agentStore.ListAgents()
		for _, name := range names {
			if h.evolutionService == nil {
				continue
			}
			serviceSuggestions, err := h.evolutionService.GetSuggestions(name)
			if err != nil {
				continue
			}
			for _, suggestion := range serviceSuggestions {
				suggestions = append(suggestions, map[string]any{
					"type":              suggestion.Type,
					"agent":             suggestion.Agent,
					"confidence":        suggestion.Confidence,
					"reason":            suggestion.Reason,
					"requires_approval": suggestion.RequiresApproval,
					"recommended_path":  suggestion.RecommendedPath,
				})
			}
		}
	}

	orihttp.WriteJSON(w, map[string]any{
		"suggestions":  suggestions,
		"generated_at": time.Now().UTC(),
	})
}

func extractAgentNameFromPath(path, suffix string) (string, bool) {
	if !strings.HasPrefix(path, "/api/agents/") || !strings.HasSuffix(path, suffix) {
		return "", false
	}

	raw := strings.TrimPrefix(path, "/api/agents/")
	raw = strings.TrimSuffix(raw, suffix)
	raw = strings.Trim(raw, "/")
	if raw == "" || strings.Contains(raw, "/") {
		return "", false
	}
	return raw, true
}
