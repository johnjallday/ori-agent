package agenthttp

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"

	"net/http"
	"time"

	"github.com/johnjallday/ori-agent/internal/httputil"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// validAgentNameRegex defines the allowed characters for agent names
// Only alphanumeric, underscores, hyphens, and spaces are allowed
var validAgentNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\- ]+$`)

const (
	minAgentNameLength = 1
	maxAgentNameLength = 100
)

// validateAgentName validates that an agent name is safe for filesystem operations
func validateAgentName(name string) error {
	if len(name) < minAgentNameLength {
		return fmt.Errorf("agent name cannot be empty")
	}
	if len(name) > maxAgentNameLength {
		return fmt.Errorf("agent name too long (max %d characters)", maxAgentNameLength)
	}
	if !validAgentNameRegex.MatchString(name) {
		return fmt.Errorf("agent name contains invalid characters (only alphanumeric, spaces, underscores, and hyphens allowed)")
	}
	return nil
}

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
			logger.Error("CreateAgent decode error", logger.Fields{"error": errMsg})
			http.Error(w, errMsg, http.StatusBadRequest)
			return
		}
		log.Printf("📝 CreateAgent request: name=%q, type=%q, model=%q, temperature=%v",
			req.Name, req.Type, req.Model, req.Temperature)

		// Validate agent name
		if err := validateAgentName(req.Name); err != nil {
			logger.Error("CreateAgent error: invalid agent name", logger.Fields{"error": err})
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Build config from request
		config := &store.CreateAgentConfig{
			Type:         req.Type,
			Model:        req.Model,
			Temperature:  req.Temperature,
			SystemPrompt: req.SystemPrompt,
		}

		logger.Debug("🔄 Creating agent", logger.Fields{"agent": req.Name})
		if err := h.State.CreateAgent(req.Name, config); err != nil {
			logger.Error("CreateAgent error", logger.Fields{"error": err})
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
					logger.Error("Failed to set metadata", logger.Fields{"err": err})
				}
			}
		}

		logger.Info("Agent created successfully", logger.Fields{"agent": req.Name})

		// Log activity
		if h.ActivityLogger != nil {
			details := map[string]interface{}{
				"type":        req.Type,
				"model":       req.Model,
				"description": req.Description,
			}
			if err := h.ActivityLogger.LogActivity(req.Name, types.ActivityEventCreated, details, ""); err != nil {
				logger.Error("Failed to log activity", logger.Fields{"err": err})
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"message": "Agent '" + req.Name + "' created successfully",
		}); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}

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
			// Core fields
			Name         *string  `json:"name,omitempty"`
			Type         *string  `json:"type,omitempty"`
			Role         *string  `json:"role,omitempty"`
			Model        *string  `json:"model,omitempty"`
			Temperature  *float64 `json:"temperature,omitempty"`
			SystemPrompt *string  `json:"system_prompt,omitempty"`
			// Metadata
			Description *string   `json:"description,omitempty"`
			Tags        *[]string `json:"tags,omitempty"`
			AvatarColor *string   `json:"avatar_color,omitempty"`
			Favorite    *bool     `json:"favorite,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, http.StatusBadRequest, "Invalid request body", err)
			return
		}

		// Initialize metadata if nil
		if agent.Metadata == nil {
			agent.Metadata = &types.AgentMetadata{}
		}

		// Update core fields if provided (partial update)
		if req.Type != nil {
			agent.Type = *req.Type
		}
		if req.Role != nil {
			agent.Role = types.AgentRole(*req.Role)
		}
		if req.Model != nil {
			agent.Settings.Model = *req.Model
		}
		if req.Temperature != nil {
			agent.Settings.Temperature = *req.Temperature
		}
		if req.SystemPrompt != nil {
			agent.Settings.SystemPrompt = *req.SystemPrompt
		}

		// Update metadata fields if provided (partial update)
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

		// Track if this was the current agent (for rename handling)
		_, currentAgent := h.State.ListAgents()
		wasCurrent := currentAgent == agentName
		newName := agentName

		// Update timestamp if statistics exist
		if agent.Statistics != nil {
			agent.Statistics.UpdatedAt = time.Now()
		}

		// Save updated agent (handle rename if needed)
		if req.Name != nil && *req.Name != "" && *req.Name != agentName {
			if _, exists := h.State.GetAgent(*req.Name); exists {
				http.Error(w, "Agent with that name already exists", http.StatusConflict)
				return
			}

			if err := h.State.SetAgent(*req.Name, agent); err != nil {
				logger.Error("Failed to save renamed agent", logger.Fields{"agent": err})
				httputil.RespondError(w, http.StatusInternalServerError, "Failed to update agent", err)
				return
			}
			if err := h.State.DeleteAgent(agentName); err != nil {
				logger.Error("Failed to delete old agent record after rename", logger.Fields{"name": agentName, "err": err})
			}
			newName = *req.Name
			if wasCurrent {
				_ = h.State.SwitchAgent(newName)
			}
		} else {
			if err := h.State.SetAgent(agentName, agent); err != nil {
				logger.Error("Failed to update agent metadata", logger.Fields{"agent": err})
				httputil.RespondError(w, http.StatusInternalServerError, "Failed to update agent", err)
				return
			}
		}

		logger.Info("Agent metadata updated", logger.Fields{"agent": newName})

		// Log activity
		if h.ActivityLogger != nil {
			updatedFields := []string{}
			if req.Name != nil {
				updatedFields = append(updatedFields, "name")
			}
			if req.Type != nil {
				updatedFields = append(updatedFields, "type")
			}
			if req.Role != nil {
				updatedFields = append(updatedFields, "role")
			}
			if req.Model != nil {
				updatedFields = append(updatedFields, "model")
			}
			if req.Temperature != nil {
				updatedFields = append(updatedFields, "temperature")
			}
			if req.SystemPrompt != nil {
				updatedFields = append(updatedFields, "system_prompt")
			}
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
			if err := h.ActivityLogger.LogActivity(newName, types.ActivityEventUpdated, details, ""); err != nil {
				logger.Error("Failed to log activity", logger.Fields{"err": err})
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"name":    newName,
			"message": "Agent updated successfully",
		}); err != nil {
			logger.Error("Failed to encode response", logger.Fields{"response": err})
		}

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
				logger.Error("Failed to log activity", logger.Fields{"err": err})
			}
		}

		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
