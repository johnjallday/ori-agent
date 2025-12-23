package agentstudio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// AddAgentRequest represents the request to add an agent to a workspace
type AddAgentRequest struct {
	AgentName string `json:"agent_name"`
}

// AddAgent handles POST /api/studios/:id/agents
func (h *HTTPHandler) AddAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.
				// Extract studio ID from URL path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response",
				// Parse request body
				logger.Fields{"error": err})
		}
		return
	}
	studioID := parts[0]

	var req AddAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err := orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if req.AgentName == "" {
		if err := orihttp.RespondBadRequest(w, "Agent name is required"); err != nil {
			logger.
				// Get studio
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.
				// Add agent using workspace method (creates stable AgentInstance)
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := studio.AddAgent(req.AgentName); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to add agent: %v", err)); err != nil {
			logger.
				// Save updated studio
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to update studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	logger.Debug("Added agent to studio", logger.Fields{"agent": req.AgentName, "studioID": studioID})

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Agent added successfully",
		"agent":   req.AgentName,
		"studio":  studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}

// RemoveAgent handles DELETE /api/studios/:id/agents/:agent_name
func (h *HTTPHandler) RemoveAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.
				// Extract studio ID and agent identifier from URL path
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/studios/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		if err := orihttp.RespondBadRequest(w, "Invalid URL format"); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error":
			// Format: "name" or "name:instanceNumber"
			err})
		}
		return
	}
	studioID := parts[0]
	agentIdentifier := parts[2]

	// Get studio
	studio, err := h.store.Get(studioID)
	if err != nil {
		if err := orihttp.RespondNotFound(w, fmt.Sprintf("Studio not found: %v", err)); err != nil {
			logger.
				// Parse agent identifier to extract name and instance number
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	var agentName string
	var instanceNumber int
	if strings.Contains(agentIdentifier, ":") {
		identParts := strings.SplitN(agentIdentifier, ":", 2)
		agentName = identParts[0]
		instanceNumber, err = strconv.Atoi(identParts[1])
		if err != nil {
			if err := orihttp.RespondBadRequest(w, "Invalid instance number format"); err != nil {
				logger.Error("Failed to write response", logger.Fields{"error": err})
			}
			return
		}
	} else {
		agentName = agentIdentifier
		instanceNumber = 0
	}

	// Find the specific agent instance to remove
	var targetInstanceID string

	// If we have AgentInstances, always use them (even if no instance number provided)
	if len(studio.AgentInstances) > 0 {
		if instanceNumber > 0 {
			// Find by name and instance number
			for _, inst := range studio.AgentInstances {
				if inst.Name == agentName && inst.InstanceNumber == instanceNumber {
					targetInstanceID = inst.ID
					break
				}
			}
		} else {
			// No instance number provided - find first matching agent by name
			for _, inst := range studio.AgentInstances {
				if inst.Name == agentName {
					targetInstanceID = inst.ID
					instanceNumber = inst.InstanceNumber // For logging
					break
				}
			}
		}

		if targetInstanceID == "" {
			if err := orihttp.RespondNotFound(w, fmt.Sprintf("Agent instance %s not found", agentName)); err != nil {
				logger.Error(
					// Remove using new method (maintains stable node IDs, only unassigns tasks for THIS instance)
					"Failed to write response", logger.Fields{"error": err})
			}
			return
		}

		if err := studio.RemoveAgentInstance(targetInstanceID); err != nil {
			if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to remove agent instance: %v", err)); err != nil {
				logger.Error("Failed to write response",
					// LEGACY: No AgentInstances exist - use old method (removes first occurrence by name)
					logger.Fields{"error": err})
			}
			return
		}
	} else {

		if err := studio.RemoveAgent(agentName); err != nil {
			if err := orihttp.RespondNotFound(w, fmt.Sprintf("Failed to remove agent: %v", err)); err != nil {
				logger.Error(
					// Save updated studio
					"Failed to write response", logger.Fields{"error": err})
			}
			return
		}
	}

	if err := h.store.Save(studio); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to save studio: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if instanceNumber > 0 {
		logger.Debug("Removed agent instance # from studio", logger.Fields{"instanceNumber": instanceNumber, "studioID": studioID, "workspace_id": agentName})
	} else {
		logger.Debug("Removed agent from studio", logger.Fields{"agent": agentName, "studioID": studioID})
	}

	// Return success
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Agent removed successfully",
		"agent":   agentName,
		"studio":  studioID,
	}); err != nil {
		logger.Error("Failed to encode response", logger.Fields{"response": err})
	}
}
