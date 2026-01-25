package skillshttp

import (
	"encoding/json"
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
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	agentName := r.URL.Query().Get("agent")
	if agentName == "" && h.store != nil {
		if _, current := h.store.ListAgents(); current != "" {
			agentName = current
		}
	}

	skillsList, err := h.manager.ListSkills(agentName)
	if err != nil {
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

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/skills/")
	name = strings.TrimSpace(name)
	if name == "" {
		orihttp.BadRequest(w, "skill name required")
		return
	}

	agentName := r.URL.Query().Get("agent")
	if agentName == "" && h.store != nil {
		if _, current := h.store.ListAgents(); current != "" {
			agentName = current
		}
	}

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
