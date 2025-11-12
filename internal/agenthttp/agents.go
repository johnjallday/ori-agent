package agenthttp

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/store"
)

type Handler struct {
	State store.Store
}

func New(state store.Store) *Handler { return &Handler{State: state} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Check if requesting a specific agent: /api/agents/{name}
		agentName := r.URL.Query().Get("name")
		if agentName == "" {
			// Try to extract from path: /api/agents/AgentName
			path := r.URL.Path
			if len(path) > len("/api/agents/") {
				agentName = path[len("/api/agents/"):]
			}
		}

		// If specific agent requested, return its details
		if agentName != "" {
			agent, ok := h.State.GetAgent(agentName)
			if !ok || agent == nil {
				http.Error(w, "Agent not found", http.StatusNotFound)
				return
			}

			// Get enabled plugins list
			enabledPlugins := make([]string, 0, len(agent.Plugins))
			for pluginName := range agent.Plugins {
				enabledPlugins = append(enabledPlugins, pluginName)
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":            agentName,
				"type":            agent.Type,
				"role":            agent.Role,
				"capabilities":    agent.Capabilities,
				"model":           agent.Settings.Model,
				"temperature":     agent.Settings.Temperature,
				"system_prompt":   agent.Settings.SystemPrompt,
				"enabled_plugins": enabledPlugins,
			})
			return
		}

		// Otherwise, return list of all agents
		names, current := h.State.ListAgents()

		// Build agent details list with name and type
		type AgentInfo struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		agentInfos := make([]AgentInfo, 0, len(names))
		for _, name := range names {
			agent, ok := h.State.GetAgent(name)
			if ok && agent != nil {
				agentInfos = append(agentInfos, AgentInfo{
					Name: name,
					Type: agent.Type,
				})
			} else {
				// Fallback for agents that couldn't be loaded
				agentInfos = append(agentInfos, AgentInfo{
					Name: name,
					Type: "tool-calling", // default
				})
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agents":  agentInfos,
			"current": current,
		})

	case http.MethodPost:
		var req struct {
			Name         string  `json:"name"`
			Type         string  `json:"type,omitempty"`
			Model        string  `json:"model,omitempty"`
			Temperature  float64 `json:"temperature,omitempty"`
			SystemPrompt string  `json:"system_prompt,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			errMsg := "Failed to decode request: " + err.Error()
			log.Printf("❌ CreateAgent decode error: %s", errMsg)
			http.Error(w, errMsg, http.StatusBadRequest)
			return
		}
		log.Printf("📝 CreateAgent request: name=%q, type=%q, model=%q, temperature=%v",
			req.Name, req.Type, req.Model, req.Temperature)
		if req.Name == "" {
			log.Printf("❌ CreateAgent error: name is empty")
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}

		// Build config from request
		config := &store.CreateAgentConfig{
			Type:         req.Type,
			Model:        req.Model,
			Temperature:  req.Temperature,
			SystemPrompt: req.SystemPrompt,
		}

		log.Printf("🔄 Creating agent: %s", req.Name)
		if err := h.State.CreateAgent(req.Name, config); err != nil {
			log.Printf("❌ CreateAgent error: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("✅ Agent created successfully: %s", req.Name)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"message": "Agent '" + req.Name + "' created successfully",
		})

	case http.MethodPut:
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if err := h.State.SwitchAgent(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if err := h.State.DeleteAgent(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
