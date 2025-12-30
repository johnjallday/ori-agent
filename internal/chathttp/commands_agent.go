package chathttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/pluginapi"
)

// HandleAgentStatus handles the /agent command to show agent status dashboard
func (ch *CommandHandler) HandleAgentStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get current agent information
	ag, current, ok := store.GetCurrentAgent(ch.store)
	if !ok {
		orihttp.InternalError(w, "current agent not found")
		return
	}

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

	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {

		logger.Error("Failed to encode response", logger.Fields{"error": encErr})

	}
}

// HandleToolsList handles the /tools command to list available functions
func (ch *CommandHandler) HandleToolsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get current agent information
	ag, _, ok := store.GetCurrentAgent(ch.store)
	if !ok {
		orihttp.InternalError(w, "current agent not found")
		return
	}

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

			description := def.Description
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

					operationParamMap := make(map[string][]string)
					requiredByOperation := make(map[string]map[string]bool)

					// First, try to get operations from OperationsProvider interface
					if plugin.Tool != nil {
						if opsProvider, ok := plugin.Tool.(pluginapi.OperationsProvider); ok {
							operations := opsProvider.GetOperations()
							for _, op := range operations {
								operationParamMap[op.Name] = op.Parameters
								requiredSet := make(map[string]bool)
								for _, req := range op.RequiredParameters {
									requiredSet[req] = true
								}
								requiredByOperation[op.Name] = requiredSet
							}
						}
					}

					// Fallback: try to extract from oneOf schema (legacy support)
					if len(operationParamMap) == 0 && def.Parameters != nil {
						if oneOfRaw, ok := def.Parameters["oneOf"].([]interface{}); ok {
							for _, option := range oneOfRaw {
								optionSchema, ok := option.(map[string]any)
								if !ok {
									continue
								}

								props, ok := optionSchema["properties"].(map[string]any)
								if !ok {
									continue
								}

								opValue := ""
								if opProp, ok := props["operation"].(map[string]any); ok {
									if enumRaw, ok := opProp["enum"]; ok {
										enumValues := interfaceSliceToStrings(enumRaw)
										if len(enumValues) == 1 {
											opValue = enumValues[0]
										}
									}
								}
								if opValue == "" {
									continue
								}

								requiredSet := make(map[string]bool)
								if reqRaw, ok := optionSchema["required"]; ok {
									for _, req := range interfaceSliceToStrings(reqRaw) {
										requiredSet[req] = true
									}
								}

								var params []string
								for paramName := range props {
									if paramName == "operation" {
										continue
									}
									params = append(params, paramName)
								}
								sort.Strings(params)
								operationParamMap[opValue] = params
								requiredByOperation[opValue] = requiredSet
							}
						}
					}

					for _, enumValue := range enumValues {
						// Get parameters for this operation based on operation type
						var operationParams []string

						// Get relevant parameters for this specific operation
						if relevantParams, exists := operationParamMap[enumValue]; exists {
							for _, paramName := range relevantParams {
								if _, paramExists := parameterInfo[paramName]; paramExists {
									isRequired := false
									if requiredByOperation[enumValue] != nil {
										isRequired = requiredByOperation[enumValue][paramName]
									} else {
										for _, req := range required {
											if req == paramName {
												isRequired = true
												break
											}
										}
									}

									displayName := paramName
									if isRequired {
										displayName += "*"
									}
									operationParams = append(operationParams, displayName)
								}
							}
						}
						// No fallback - if we don't know the operation-specific params,
						// show the operation name without params rather than showing all params

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

	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {

		logger.Error("Failed to encode response", logger.Fields{"error": encErr})

	}
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
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
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
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	// Check if we're already on the requested agent
	if current == agentName {
		response := map[string]any{
			"response": fmt.Sprintf("✅ **Already using agent '%s'**", agentName),
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	// Switch to the requested agent
	if err := ch.store.SwitchAgent(agentName); err != nil {
		errorMsg := fmt.Sprintf("❌ **Failed to switch to agent '%s':** %v", agentName, err)
		response := map[string]any{
			"response": errorMsg,
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	// Success message
	successMsg := fmt.Sprintf("✅ **Switched to agent '%s'**\n\nYou are now using the '%s' agent for all interactions.",
		agentName, agentName)
	response := map[string]any{
		"response": successMsg,
	}
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// HandleAgentsList handles the /agents command to list all available agents
func (ch *CommandHandler) HandleAgentsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get all agents and current agent
	names, current := ch.store.ListAgents()

	if len(names) == 0 {
		response := map[string]any{
			"response": "No agents found.",
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	// Build agents list response
	var agentsResponse strings.Builder
	agentsResponse.WriteString("## 🤖 Available Agents\n\n")

	for _, name := range names {
		if name == current {
			agentsResponse.WriteString(fmt.Sprintf("- **%s** ✓ (current)\n", name))
		} else {
			agentsResponse.WriteString(fmt.Sprintf("- %s\n", name))
		}
	}

	agentsResponse.WriteString(fmt.Sprintf("\n**Total agents:** %d\n", len(names)))
	agentsResponse.WriteString("\n💡 **Tip:** Use `/switch <agent-name>` to switch to a different agent")

	response := map[string]any{
		"response": agentsResponse.String(),
	}
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
