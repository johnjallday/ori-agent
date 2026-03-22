package chathttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/oriagent/ori-pluginapi"
)

func commandSessionModeLabel(resolution executionAgentResolution) string {
	if resolution.isAssistantMode() {
		return "Assistant"
	}
	return "Pinned specialist"
}

func commandExecutionAgentLabel(resolution executionAgentResolution) string {
	if !resolution.isResolved() {
		return "Not configured"
	}
	if resolution.isAssistantMode() {
		return fmt.Sprintf("Assistant (`%s`)", resolution.Name)
	}
	return resolution.Name
}

// HandleAgentStatus handles the /agent command to show Assistant/session status.
func (ch *CommandHandler) HandleAgentStatus(w http.ResponseWriter, r *http.Request, resolution executionAgentResolution) {
	w.Header().Set("Content-Type", "application/json")

	apiKeyStatus := "Configured"
	sessionMode := commandSessionModeLabel(resolution)
	executionAgent := commandExecutionAgentLabel(resolution)

	ag, ok := ch.store.GetAgent(strings.TrimSpace(resolution.Name))
	if !ok || ag == nil {
		statusResponse := fmt.Sprintf(`## 🤖 Assistant Status

**Session Mode:** %s
**Execution Agent:** %s

No resolved specialist runtime is available for this session yet.`, sessionMode, executionAgent)
		response := map[string]any{
			"response": statusResponse,
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	statusResponse := fmt.Sprintf(`## 🤖 Assistant Status

**Session Mode:** %s
**Execution Agent:** %s

**Model Configuration:**
- Model: %s
- Temperature: %.1f

**API Configuration:**
- API Key: %s

**Plugin Status:**
- Total Plugins: %d`,
		sessionMode,
		executionAgent,
		ag.Settings.Model,
		ag.Settings.Temperature,
		apiKeyStatus,
		len(ag.Plugins))

	if len(ag.Plugins) > 0 {
		statusResponse += "\n- Active Plugins:\n"
		for name, plugin := range ag.Plugins {
			statusResponse += fmt.Sprintf("  - %s %s (v%s)\n", getPluginEmoji(name), name, plugin.Version)
		}
	} else {
		statusResponse += "\n- No plugins loaded"
	}

	statusResponse += "\n\n**System Status:**\n- Server: Running ✅\n- Registry: Loaded ✅"

	response := map[string]any{
		"response": statusResponse,
	}

	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// HandleToolsList handles the /tools command to list available functions.
func (ch *CommandHandler) HandleToolsList(w http.ResponseWriter, r *http.Request, resolution executionAgentResolution) {
	w.Header().Set("Content-Type", "application/json")

	ag, ok := ch.store.GetAgent(strings.TrimSpace(resolution.Name))
	if !ok || ag == nil {
		orihttp.WriteJSON(w, map[string]any{
			"response": "## 🔧 Available Tools\n\nNo tools are currently loaded for this Assistant session.",
		})
		return
	}

	var toolsResponse strings.Builder
	toolsResponse.WriteString("## 🔧 Available Tools\n\n")
	if resolution.isAssistantMode() {
		toolsResponse.WriteString(fmt.Sprintf("These tools are available to Assistant via `%s`.\n\n", resolution.Name))
	} else {
		toolsResponse.WriteString(fmt.Sprintf("These tools are available to pinned specialist `%s`.\n\n", resolution.Name))
	}

	if len(ag.Plugins) == 0 {
		toolsResponse.WriteString("No tools are currently loaded.")
	} else {
		for name, plugin := range ag.Plugins {
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

			allEnums, err := ch.enumExtractor.GetAllEnumsFromParameter(def)
			if err == nil && len(allEnums) > 0 {
				toolsResponse.WriteString("**🎯 Available Options:**\n")

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
						var operationParams []string

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

	response := map[string]any{
		"response": toolsResponse.String(),
	}

	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// HandleSkillsList handles the /skills command to list available skills.
func (ch *CommandHandler) HandleSkillsList(w http.ResponseWriter, r *http.Request, resolution executionAgentResolution) {
	w.Header().Set("Content-Type", "application/json")

	if ch.skillsManager == nil {
		orihttp.WriteJSON(w, map[string]any{
			"response": "No skills are currently loaded.",
		})
		return
	}

	if !resolution.isResolved() {
		orihttp.WriteJSON(w, map[string]any{
			"response": "No skills are currently loaded for this Assistant session.",
		})
		return
	}

	skillsList, err := ch.skillsManager.ListSkills(resolution.Name)
	if err != nil {
		orihttp.InternalError(w, err.Error())
		return
	}

	var skillsResponse strings.Builder
	skillsResponse.WriteString("## 🧩 Available Skills\n\n")
	if resolution.isAssistantMode() {
		skillsResponse.WriteString(fmt.Sprintf("These skills are available to Assistant via `%s`.\n\n", resolution.Name))
	} else {
		skillsResponse.WriteString(fmt.Sprintf("These skills are available to pinned specialist `%s`.\n\n", resolution.Name))
	}

	if len(skillsList) == 0 {
		skillsResponse.WriteString("No skills are currently loaded.")
	} else {
		for _, skill := range skillsList {
			sourceLabel := cases.Title(language.English).String(skill.Source)
			description := skill.Description
			if description == "" {
				description = "(No description)"
			}
			skillsResponse.WriteString(fmt.Sprintf("- **%s** (%s) - %s\n", skill.Name, sourceLabel, description))
		}
		skillsResponse.WriteString("\nUse `/skill <name> <args>` or `/<name>` to run a skill.")
	}

	response := map[string]any{
		"response": skillsResponse.String(),
	}

	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// HandleSwitch handles the deprecated /switch command.
func (ch *CommandHandler) HandleSwitch(w http.ResponseWriter, r *http.Request, agentName string) {
	w.Header().Set("Content-Type", "application/json")

	response := map[string]any{
		"response": "Global agent switching is deprecated. Use Assistant sessions by default and pin a specialist to a session only when needed.",
	}
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// HandleAgentsList handles the /agents command to list all available agents.
func (ch *CommandHandler) HandleAgentsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	names := ch.store.ListAgents()

	if len(names) == 0 {
		response := map[string]any{
			"response": "No agents found.",
		}
		if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
			logger.Error("Failed to encode response", logger.Fields{"error": encErr})
		}
		return
	}

	var agentsResponse strings.Builder
	agentsResponse.WriteString("## 🤖 Available Agents\n\n")

	for _, name := range names {
		if strings.EqualFold(strings.TrimSpace(name), assistantExecutionAgentName) {
			agentsResponse.WriteString(fmt.Sprintf("- **%s** (Assistant)\n", name))
			continue
		}
		agentsResponse.WriteString(fmt.Sprintf("- %s\n", name))
	}

	agentsResponse.WriteString(fmt.Sprintf("\n**Total agents:** %d\n", len(names)))
	agentsResponse.WriteString("\n💡 **Tip:** Assistant sessions can pin a specialist when needed, but there is no global current agent anymore.")

	response := map[string]any{
		"response": agentsResponse.String(),
	}
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
