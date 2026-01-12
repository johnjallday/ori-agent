package agenthttp

import (
	"fmt"
	"net/http"
	"regexp"
	"time"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
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
		// Check if requesting a specific agent: /api/agents/{name} or /api/agents?name=...
		agentName := orihttp.GetPathParamOrQuery(r, "/api/agents/", "name")

		// If specific agent requested, return its details
		if agentName != "" {
			agent, ok := h.State.GetAgent(agentName)
			if !ok || agent == nil {
				orihttp.NotFound(w, "Agent not found")
				return
			}

			// Get enabled plugins list
			enabledPlugins := make([]string, 0, len(agent.Plugins))
			for pluginName := range agent.Plugins {
				enabledPlugins = append(enabledPlugins, pluginName)
			}

			orihttp.WriteJSON(w, map[string]any{
				"name":              agentName,
				"type":              agent.Type,
				"role":              agent.Role,
				"capabilities":      agent.Capabilities,
				"model":             agent.Settings.Model,
				"temperature":       agent.Settings.Temperature,
				"provider":          agent.Settings.Provider,
				"max_output_tokens": agent.Settings.MaxOutputTokens,
				"system_prompt":     agent.Settings.SystemPrompt,
				"enabled_plugins":   enabledPlugins,
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

		orihttp.WriteJSON(w, map[string]any{
			"agents":  agentInfos,
			"current": current,
		})

	case http.MethodPost:
		var req struct {
			Name            string   `json:"name"`
			Type            string   `json:"type,omitempty"`
			Model           string   `json:"model,omitempty"`
			Temperature     float64  `json:"temperature,omitempty"`
			SystemPrompt    string   `json:"system_prompt,omitempty"`
			Description     string   `json:"description,omitempty"`
			Tags            []string `json:"tags,omitempty"`
			AvatarColor     string   `json:"avatar_color,omitempty"`
			LLMProvider     string   `json:"llm_provider,omitempty"`
			MaxOutputTokens int      `json:"max_output_tokens,omitempty"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
			return
		}
		logger.Debug("CreateAgent request", logger.Fields{
			"name": req.Name, "type": req.Type, "model": req.Model, "temperature": req.Temperature,
		})

		// Validate agent name
		if err := validateAgentName(req.Name); err != nil {
			logger.Error("CreateAgent error: invalid agent name", logger.Fields{"error": err})
			orihttp.BadRequest(w, err.Error())
			return
		}

		// Build config from request
		config := &store.CreateAgentConfig{
			Type:            req.Type,
			Model:           req.Model,
			Temperature:     req.Temperature,
			SystemPrompt:    req.SystemPrompt,
			LLMProvider:     req.LLMProvider,
			MaxOutputTokens: req.MaxOutputTokens,
		}

		logger.Debug("Creating agent", logger.Fields{"agent": req.Name})
		if err := h.State.CreateAgent(req.Name, config); err != nil {
			logger.Error("CreateAgent error", logger.Fields{"error": err})
			orihttp.BadRequest(w, err.Error())
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

		orihttp.Success(w, map[string]any{
			"success": true,
			"message": "Agent '" + req.Name + "' created successfully",
		})

	case http.MethodPut:
		name := r.URL.Query().Get("name")
		if name == "" {
			orihttp.BadRequest(w, "name required")
			return
		}
		if err := h.State.SwitchAgent(name); err != nil {
			orihttp.BadRequest(w, err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)

	case http.MethodPatch:
		// PATCH /api/agents/:name - Update agent metadata
		agentName := orihttp.RequirePathParamOrQuery(w, r, "/api/agents/", "name")
		if agentName == "" {
			return
		}

		// Get existing agent
		agent, ok := h.State.GetAgent(agentName)
		if !ok || agent == nil {
			orihttp.NotFound(w, "Agent not found")
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
			LLMProvider  *string  `json:"llm_provider,omitempty"`
			MaxTokens    *int     `json:"max_output_tokens,omitempty"`
			SystemPrompt *string  `json:"system_prompt,omitempty"`
			// Metadata
			Description *string   `json:"description,omitempty"`
			Tags        *[]string `json:"tags,omitempty"`
			AvatarColor *string   `json:"avatar_color,omitempty"`
			Favorite    *bool     `json:"favorite,omitempty"`
		}
		if !orihttp.ParseJSONBody(w, r, &req) {
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
		if req.LLMProvider != nil {
			agent.Settings.Provider = *req.LLMProvider
		}
		if req.MaxTokens != nil {
			agent.Settings.MaxOutputTokens = *req.MaxTokens
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
			// Validate the new agent name
			if err := validateAgentName(*req.Name); err != nil {
				orihttp.BadRequest(w, err.Error())
				return
			}

			if _, exists := h.State.GetAgent(*req.Name); exists {
				orihttp.Conflict(w, "Agent with that name already exists")
				return
			}

			if err := h.State.SetAgent(*req.Name, agent); err != nil {
				logger.Error("Failed to save renamed agent", logger.Fields{"agent": err})
				orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update agent", err)
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
				orihttp.RespondErrorWithErr(w, http.StatusInternalServerError, "Failed to update agent", err)
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
			if req.LLMProvider != nil {
				updatedFields = append(updatedFields, "llm_provider")
			}
			if req.MaxTokens != nil {
				updatedFields = append(updatedFields, "max_output_tokens")
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

		orihttp.Success(w, map[string]any{
			"success": true,
			"name":    newName,
			"message": "Agent updated successfully",
		})

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			orihttp.BadRequest(w, "name required")
			return
		}
		if err := h.State.DeleteAgent(name); err != nil {
			orihttp.BadRequest(w, err.Error())
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
