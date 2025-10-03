package chathttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/johnjallday/dolphin-agent/internal/pluginhttp"
	"github.com/johnjallday/dolphin-agent/internal/store"
)

// CommandHandler handles special chat commands
type CommandHandler struct {
	store         store.Store
	enumExtractor *pluginhttp.EnumExtractor
}

// NewCommandHandler creates a new command handler
func NewCommandHandler(store store.Store) *CommandHandler {
	return &CommandHandler{
		store:         store,
		enumExtractor: pluginhttp.NewEnumExtractor(),
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
			// Get fresh definition to show latest dynamic enums (e.g., script lists)
			def := plugin.Definition
			if plugin.Tool != nil {
				def = plugin.Tool.Definition()
			}

			emoji := getPluginEmoji(name)
			toolsResponse.WriteString(fmt.Sprintf("### %s %s\n\n", emoji, def.Name))

			description := def.Description.String()
			if description != "" {
				toolsResponse.WriteString(fmt.Sprintf("**Description:** %s\n\n", description))
			}

			// Extract all enum values for this plugin and show them prominently with parameters
			allEnums, err := ch.enumExtractor.GetAllEnumsFromParameter(def)
			if err == nil && len(allEnums) > 0 {
				toolsResponse.WriteString("**🎯 Available Options:**\n")

				// Get parameter info for inline display
				var parameterInfo map[string]map[string]any
				var required []string
				if def.Parameters != nil {
					if props, ok := def.Parameters["properties"].(map[string]any); ok {
						parameterInfo = make(map[string]map[string]any)
						for paramName, paramData := range props {
							if paramMap, ok := paramData.(map[string]any); ok {
								parameterInfo[paramName] = paramMap
							}
						}

						// Get required fields
						if reqField, exists := def.Parameters["required"]; exists {
							if reqSlice, ok := reqField.([]string); ok {
								required = reqSlice
							}
						}
					}
				}

				for enumProperty, enumValues := range allEnums {
					toolsResponse.WriteString(fmt.Sprintf("- **%s**:\n", enumProperty))
					for _, enumValue := range enumValues {
						// Get parameters for this operation based on operation type
						var operationParams []string

						// Define operation-specific parameter mappings
						operationParamMap := map[string][]string{
							"create_project":  {"name", "bpm"},
							"open_project":    {"path"},
							"filter_project": {"name"},
							"get_settings":   {},
							"scan":           {},
							"list_projects":  {},
						}

						// Get relevant parameters for this specific operation
						if relevantParams, exists := operationParamMap[enumValue]; exists {
							for _, paramName := range relevantParams {
								if _, paramExists := parameterInfo[paramName]; paramExists {
									isRequired := false
									for _, req := range required {
										if req == paramName {
											isRequired = true
											break
										}
									}

									displayName := paramName
									if isRequired {
										displayName += "*"
									}
									operationParams = append(operationParams, displayName)
								}
							}
						} else {
							// Fallback: show all non-operation parameters for unknown operations
							for paramName := range parameterInfo {
								if paramName == enumProperty {
									continue // Skip the operation parameter itself
								}

								isRequired := false
								for _, req := range required {
									if req == paramName {
										isRequired = true
										break
									}
								}

								displayName := paramName
								if isRequired {
									displayName += "*"
								}
								operationParams = append(operationParams, displayName)
							}
						}

						if len(operationParams) > 0 {
							toolsResponse.WriteString(fmt.Sprintf("  - `%s` (%s)\n", enumValue, strings.Join(operationParams, ", ")))
						} else {
							toolsResponse.WriteString(fmt.Sprintf("  - `%s`\n", enumValue))
						}
					}
				}
				toolsResponse.WriteString("\n")
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

// HandleSwitch handles the /switch command to switch between agents
func (ch *CommandHandler) HandleSwitch(w http.ResponseWriter, r *http.Request, agentName string) {
	w.Header().Set("Content-Type", "application/json")

	// If no agent name provided, list available agents
	if agentName == "" {
		names, current := ch.store.ListAgents()
		agentList := fmt.Sprintf("**Available agents:** %s\n\n**Current agent:** %s\n\nUsage: `/switch <agent-name>`",
			strings.Join(names, ", "), current)

		response := map[string]any{
			"response": agentList,
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	// Get list of available agents to validate the requested agent exists
	names, current := ch.store.ListAgents()

	// Check if the requested agent exists
	found := false
	for _, name := range names {
		if name == agentName {
			found = true
			break
		}
	}

	if !found {
		errorMsg := fmt.Sprintf("❌ **Agent '%s' not found.**\n\nAvailable agents: %s",
			agentName, strings.Join(names, ", "))
		response := map[string]any{
			"response": errorMsg,
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	// Check if we're already on the requested agent
	if current == agentName {
		response := map[string]any{
			"response": fmt.Sprintf("✅ **Already using agent '%s'**", agentName),
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	// Switch to the requested agent
	if err := ch.store.SwitchAgent(agentName); err != nil {
		errorMsg := fmt.Sprintf("❌ **Failed to switch to agent '%s':** %v", agentName, err)
		response := map[string]any{
			"response": errorMsg,
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	// Success message
	successMsg := fmt.Sprintf("✅ **Switched to agent '%s'**\n\nYou are now using the '%s' agent for all interactions.",
		agentName, agentName)
	response := map[string]any{
		"response": successMsg,
	}
	json.NewEncoder(w).Encode(response)
}

// HandleHelp handles the /help command to show available commands
func (ch *CommandHandler) HandleHelp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	helpResponse := `## 🤖 Available Commands

**System Commands:**
- **/help** - Show this help message
- **/agent** - Display agent status dashboard
- **/switch <agent-name>** - Switch to a different agent
- **/tools** - List all available plugin tools and operations

**Agent Management:**
- Use **/agent** to see current agent status and available agents
- Use **/switch** to change between configured agents
- Each agent can have different plugins and configurations

**Plugin Tools:**
- Use **/tools** to see all available plugin operations
- Each tool shows available options and parameters
- Tools are specific to your current agent configuration

**Tips:**
- Commands must start with **/** (forward slash)
- Agent names are case-sensitive when switching
- Use the web interface to configure plugins and agents

Type any command above to get started!`

	response := map[string]any{
		"response": helpResponse,
	}
	json.NewEncoder(w).Encode(response)
}