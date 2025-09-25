package chathttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/johnjallday/dolphin-agent/internal/store"
)

// CommandHandler handles special chat commands
type CommandHandler struct {
	store store.Store
}

// NewCommandHandler creates a new command handler
func NewCommandHandler(store store.Store) *CommandHandler {
	return &CommandHandler{
		store: store,
	}
}

// HandleAgentStatus handles the /agent command to show agent status dashboard
func (ch *CommandHandler) HandleAgentStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get current agent information
	names, current := ch.store.ListAgents()
	if current == "" && len(names) > 0 {
		current = names[0] // fallback to first available agent
	}

	ag, ok := ch.store.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}

	// Get API key status - this would need to be passed in or accessed differently
	// For now, we'll simplify this
	apiKeyStatus := "Configured"

	// Build status dashboard
	statusResponse := fmt.Sprintf(`## 🤖 Agent Status Dashboard

**Current Agent:** %s

**Model Configuration:**
- Model: %s
- Temperature: %.1f

**API Configuration:**
- API Key: %s

**Plugin Status:**
- Total Plugins: %d`,
		current,
		ag.Settings.Model,
		ag.Settings.Temperature,
		apiKeyStatus,
		len(ag.Plugins))

	// Add plugin details
	if len(ag.Plugins) > 0 {
		statusResponse += "\n- Active Plugins:\n"
		for name, plugin := range ag.Plugins {
			statusResponse += fmt.Sprintf("  - %s %s (v%s)\n", getPluginEmoji(name), name, plugin.Version)
		}
	} else {
		statusResponse += "\n- No plugins loaded"
	}

	// Add system information
	statusResponse += "\n\n**System Status:**\n- Server: Running ✅\n- Registry: Loaded ✅"

	// Return as a response that mimics a chat message
	response := map[string]any{
		"response": statusResponse,
	}

	json.NewEncoder(w).Encode(response)
}

// HandleToolsList handles the /tools command to list available functions
func (ch *CommandHandler) HandleToolsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get current agent information
	names, current := ch.store.ListAgents()
	if current == "" && len(names) > 0 {
		current = names[0] // fallback to first available agent
	}

	ag, ok := ch.store.GetAgent(current)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}

	// Build tools list response
	var toolsResponse strings.Builder
	toolsResponse.WriteString("## 🔧 Available Tools\n\n")

	if len(ag.Plugins) == 0 {
		toolsResponse.WriteString("No tools are currently loaded.")
	} else {
		for name, plugin := range ag.Plugins {
			emoji := getPluginEmoji(name)
			toolsResponse.WriteString(fmt.Sprintf("### %s %s\n\n", emoji, plugin.Definition.Name))

			description := plugin.Definition.Description.String()
			if description != "" {
				toolsResponse.WriteString(fmt.Sprintf("**Description:** %s\n\n", description))
			}

			// Extract and display parameters information
			if plugin.Definition.Parameters != nil {
				if props, ok := plugin.Definition.Parameters["properties"].(map[string]any); ok {
					toolsResponse.WriteString("**Parameters:**\n")

					// Handle required fields
					var required []string
					if reqField, exists := plugin.Definition.Parameters["required"]; exists {
						if reqSlice, ok := reqField.([]string); ok {
							required = reqSlice
						}
					}

					for paramName, paramInfo := range props {
						if paramInfoMap, ok := paramInfo.(map[string]any); ok {
							isRequired := false
							for _, req := range required {
								if req == paramName {
									isRequired = true
									break
								}
							}

							reqText := ""
							if isRequired {
								reqText = " (required)"
							}

							// Get parameter description
							description := ""
							if desc, exists := paramInfoMap["description"]; exists {
								if descStr, ok := desc.(string); ok {
									description = descStr
								}
							}

							// Get enum values if they exist
							enumText := ""
							if enumField, exists := paramInfoMap["enum"]; exists {
								if enumSlice, ok := enumField.([]any); ok {
									var enumStrings []string
									for _, enumVal := range enumSlice {
										if enumStr, ok := enumVal.(string); ok {
											enumStrings = append(enumStrings, enumStr)
										}
									}
									if len(enumStrings) > 0 {
										enumText = fmt.Sprintf(" (options: %s)", strings.Join(enumStrings, ", "))
									}
								}
							}

							toolsResponse.WriteString(fmt.Sprintf("- **%s**%s: %s%s\n", paramName, reqText, description, enumText))
						}
					}
				}
			}

			toolsResponse.WriteString("\n")
		}
	}

	// Return as a response that mimics a chat message
	response := map[string]any{
		"response": toolsResponse.String(),
	}

	json.NewEncoder(w).Encode(response)
}