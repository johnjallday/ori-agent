package agenthttp

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

type Handler struct {
	State          store.Store
	ActivityLogger *ActivityLogger
}

func New(state store.Store) *Handler {
	return &Handler{
		State:          state,
		ActivityLogger: nil, // Will be set by server initialization
	}
}

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
			Name         string   `json:"name"`
			Type         string   `json:"type,omitempty"`
			Model        string   `json:"model,omitempty"`
			Temperature  float64  `json:"temperature,omitempty"`
			SystemPrompt string   `json:"system_prompt,omitempty"`
			Description  string   `json:"description,omitempty"`
			Tags         []string `json:"tags,omitempty"`
			AvatarColor  string   `json:"avatar_color,omitempty"`
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

		// Set metadata if provided
		if req.Description != "" || len(req.Tags) > 0 || req.AvatarColor != "" {
			agent, ok := h.State.GetAgent(req.Name)
			if ok && agent != nil {
				if agent.Metadata == nil {
					agent.Metadata = &types.AgentMetadata{}
				}
				agent.Metadata.Description = req.Description
				agent.Metadata.Tags = req.Tags
				agent.Metadata.AvatarColor = req.AvatarColor
				if err := h.State.SetAgent(req.Name, agent); err != nil {
					log.Printf("⚠️  Failed to set metadata: %v", err)
				}
			}
		}

		log.Printf("✅ Agent created successfully: %s", req.Name)

		// Log activity
		if h.ActivityLogger != nil {
			details := map[string]interface{}{
				"type":        req.Type,
				"model":       req.Model,
				"description": req.Description,
			}
			if err := h.ActivityLogger.LogActivity(req.Name, types.ActivityEventCreated, details, ""); err != nil {
				log.Printf("⚠️  Failed to log activity: %v", err)
			}
		}

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

	case http.MethodPatch:
		// PATCH /api/agents/:name - Update agent metadata
		path := r.URL.Path
		var agentName string
		if len(path) > len("/api/agents/") {
			agentName = path[len("/api/agents/"):]
		}
		if agentName == "" {
			agentName = r.URL.Query().Get("name")
		}
		if agentName == "" {
			http.Error(w, "agent name required", http.StatusBadRequest)
			return
		}

		// Get existing agent
		agent, ok := h.State.GetAgent(agentName)
		if !ok || agent == nil {
			http.Error(w, "Agent not found", http.StatusNotFound)
			return
		}

		// Parse update request
		var req struct {
			Description *string   `json:"description,omitempty"`
			Tags        *[]string `json:"tags,omitempty"`
			AvatarColor *string   `json:"avatar_color,omitempty"`
			Favorite    *bool     `json:"favorite,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Initialize metadata if nil
		if agent.Metadata == nil {
			agent.Metadata = &types.AgentMetadata{}
		}

		// Update fields if provided (partial update)
		if req.Description != nil {
			agent.Metadata.Description = *req.Description
		}
		if req.Tags != nil {
			agent.Metadata.Tags = *req.Tags
		}
		if req.AvatarColor != nil {
			agent.Metadata.AvatarColor = *req.AvatarColor
		}
		if req.Favorite != nil {
			agent.Metadata.Favorite = *req.Favorite
		}

		// Update timestamp if statistics exist
		if agent.Statistics != nil {
			agent.Statistics.UpdatedAt = time.Now()
		}

		// Save updated agent
		if err := h.State.SetAgent(agentName, agent); err != nil {
			log.Printf("❌ Failed to update agent metadata: %v", err)
			http.Error(w, "Failed to update agent: "+err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("✅ Agent metadata updated: %s", agentName)

		// Log activity
		if h.ActivityLogger != nil {
			updatedFields := []string{}
			if req.Description != nil {
				updatedFields = append(updatedFields, "description")
			}
			if req.Tags != nil {
				updatedFields = append(updatedFields, "tags")
			}
			if req.AvatarColor != nil {
				updatedFields = append(updatedFields, "avatar_color")
			}
			if req.Favorite != nil {
				updatedFields = append(updatedFields, "favorite")
			}

			details := map[string]interface{}{
				"fields": updatedFields,
			}
			if err := h.ActivityLogger.LogActivity(agentName, types.ActivityEventUpdated, details, ""); err != nil {
				log.Printf("⚠️  Failed to log activity: %v", err)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"message": "Agent metadata updated successfully",
		})

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

		// Log activity
		if h.ActivityLogger != nil {
			details := map[string]interface{}{}
			if err := h.ActivityLogger.LogActivity(name, types.ActivityEventDeleted, details, ""); err != nil {
				log.Printf("⚠️  Failed to log activity: %v", err)
			}
		}

		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
