package skillshttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
)

type Handler struct {
	manager *skills.Manager
	store   store.Store
}

func New(manager *skills.Manager, st store.Store) *Handler {
	return &Handler{
		manager: manager,
		store:   st,
	}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listSkills(w, r)
	case http.MethodPost:
		h.createSkill(w, r)
	default:
		orihttp.MethodNotAllowed(w)
	}
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/skills/")
	path = strings.TrimSpace(path)
	if path == "" {
		orihttp.BadRequest(w, "skill name required")
		return
	}

	parts := strings.Split(path, "/")
	name := strings.TrimSpace(parts[0])
	if name == "" {
		orihttp.BadRequest(w, "skill name required")
		return
	}

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			h.getSkill(w, r, name)
		case http.MethodPut:
			h.updateSkill(w, r, name)
		case http.MethodDelete:
			h.deleteSkill(w, r, name)
		default:
			orihttp.MethodNotAllowed(w)
		}
		return
	}

	if len(parts) == 2 {
		switch parts[1] {
		case "enable":
			h.setSkillEnabled(w, r, name)
			return
		case "trust":
			h.setSkillTrusted(w, r, name)
			return
		}
	}

	orihttp.NotFound(w, "skill endpoint not found")
}

type skillWriteRequest struct {
	Agent       string  `json:"agent"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Prompt      string  `json:"prompt"`
	OpenAIYAML  *string `json:"openai_yaml,omitempty"`
}

type skillStateRequest struct {
	Agent   string `json:"agent"`
	Enabled *bool  `json:"enabled,omitempty"`
	Trusted *bool  `json:"trusted,omitempty"`
}

func (h *Handler) listSkills(w http.ResponseWriter, r *http.Request) {
	agentName := resolveAgentName(r, h.store)
	skillsList, err := h.manager.ListSkills(agentName)
	if err != nil {
		var conflicts *skills.SkillConflictError
		if errors.As(err, &conflicts) {
			_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
				"error":     err.Error(),
				"conflicts": conflicts.Conflicts,
				"agent":     agentName,
			})
			return
		}
		orihttp.InternalError(w, err.Error())
		return
	}

	response := map[string]any{
		"agent":  agentName,
		"skills": skillsList,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (h *Handler) createSkill(w http.ResponseWriter, r *http.Request) {
	var req skillWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}
	agentName := resolveAgentNameWithFallback(req.Agent, h.store)
	if agentName == "" {
		orihttp.BadRequest(w, "agent name required")
		return
	}

	skill, err := h.manager.CreateSkill(agentName, skills.SkillInput{
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Prompt:      req.Prompt,
		OpenAIYAML:  req.OpenAIYAML,
	})
	if err != nil {
		switch {
		case errors.Is(err, skills.ErrSkillExists):
			orihttp.RespondErrorWithErr(w, http.StatusConflict, "skill already exists", err)
		default:
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "failed to create skill", err)
		}
		return
	}

	orihttp.Created(w, skill)
}

func (h *Handler) getSkill(w http.ResponseWriter, r *http.Request, name string) {
	agentName := resolveAgentName(r, h.store)
	skill, found, err := h.manager.GetSkill(agentName, name)
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	if !found {
		orihttp.NotFound(w, "skill not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(skill)
}

func (h *Handler) updateSkill(w http.ResponseWriter, r *http.Request, name string) {
	var req skillWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}
	agentName := resolveAgentNameWithFallback(req.Agent, h.store)
	if agentName == "" {
		orihttp.BadRequest(w, "agent name required")
		return
	}

	skill, err := h.manager.UpdateSkill(agentName, name, skills.SkillInput{
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Prompt:      req.Prompt,
		OpenAIYAML:  req.OpenAIYAML,
	})
	if err != nil {
		switch {
		case errors.Is(err, skills.ErrSkillNotFound):
			orihttp.NotFound(w, err.Error())
		case errors.Is(err, skills.ErrSkillRenameNotSupported):
			orihttp.BadRequest(w, err.Error())
		default:
			orihttp.RespondErrorWithErr(w, http.StatusBadRequest, "failed to update skill", err)
		}
		return
	}

	orihttp.Success(w, skill)
}

func (h *Handler) deleteSkill(w http.ResponseWriter, r *http.Request, name string) {
	agentName := resolveAgentName(r, h.store)
	if agentName == "" {
		orihttp.BadRequest(w, "agent name required")
		return
	}

	if err := h.manager.DeleteSkill(agentName, name); err != nil {
		if errors.Is(err, skills.ErrSkillNotFound) {
			orihttp.NotFound(w, err.Error())
			return
		}
		orihttp.InternalError(w, err.Error())
		return
	}

	orihttp.RespondNoContent(w)
}

func (h *Handler) setSkillEnabled(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	var req skillStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}
	if req.Enabled == nil {
		orihttp.BadRequest(w, "enabled flag required")
		return
	}
	agentName := resolveAgentNameWithFallback(req.Agent, h.store)
	if agentName == "" {
		orihttp.BadRequest(w, "agent name required")
		return
	}
	if _, found, err := h.manager.GetSkill(agentName, name); err != nil {
		orihttp.InternalError(w, err.Error())
		return
	} else if !found {
		orihttp.NotFound(w, "skill not found")
		return
	}
	if err := h.manager.SetSkillEnabled(agentName, name, *req.Enabled); err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	orihttp.Success(w, map[string]any{
		"agent":   agentName,
		"name":    name,
		"enabled": *req.Enabled,
	})
}

func (h *Handler) setSkillTrusted(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	var req skillStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		orihttp.BadRequest(w, "invalid request body")
		return
	}
	if req.Trusted == nil {
		orihttp.BadRequest(w, "trusted flag required")
		return
	}
	agentName := resolveAgentNameWithFallback(req.Agent, h.store)
	if agentName == "" {
		orihttp.BadRequest(w, "agent name required")
		return
	}
	if _, found, err := h.manager.GetSkill(agentName, name); err != nil {
		orihttp.InternalError(w, err.Error())
		return
	} else if !found {
		orihttp.NotFound(w, "skill not found")
		return
	}
	if err := h.manager.SetSkillTrusted(agentName, name, *req.Trusted); err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}
	orihttp.Success(w, map[string]any{
		"agent":   agentName,
		"name":    name,
		"trusted": *req.Trusted,
	})
}

func resolveAgentName(r *http.Request, st store.Store) string {
	agentName := r.URL.Query().Get("agent")
	return resolveAgentNameWithFallback(agentName, st)
}

func resolveAgentNameWithFallback(agentName string, st store.Store) string {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" && st != nil {
		if _, current := st.ListAgents(); current != "" {
			return current
		}
	}
	return agentName
}
