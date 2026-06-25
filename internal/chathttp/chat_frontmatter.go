package chathttp

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/skills"
)

// chat_frontmatter.go holds the bounded "front-matter" phases of ChatHandler -
// the work that runs before the main LLM/tool execution path. Extracting these
// keeps ChatHandler readable while preserving the exact original behavior and
// ordering.

// partitionUploadedFiles splits uploaded files into image and text files so
// each can be handled appropriately by the downstream API call.
func partitionUploadedFiles(files []UploadedFile) (imageFiles, textFiles []UploadedFile) {
	for _, file := range files {
		if isImageMimeType(file.Type) {
			imageFiles = append(imageFiles, file)
		} else {
			textFiles = append(textFiles, file)
		}
	}
	return imageFiles, textFiles
}

// buildQuestionWithUploadedFiles prepends the content of any uploaded text
// files to the question. If there are no text files, the question is returned
// unchanged.
func buildQuestionWithUploadedFiles(q string, textFiles []UploadedFile) string {
	if len(textFiles) == 0 {
		return q
	}

	var filesContext strings.Builder
	filesContext.WriteString("Here are the uploaded documents:\n\n")

	for _, file := range textFiles {
		fileText := file.Content
		if isParseableDocument(file.Name) {
			parsedText, err := parseUploadedFileText(file)
			if err != nil {
				logger.Warn("Failed to extract text from uploaded file", logger.Fields{
					"name":  file.Name,
					"type":  file.Type,
					"error": err.Error(),
				})
				fileText = fmt.Sprintf("[Unable to extract text from %s]", file.Name)
			} else if strings.TrimSpace(parsedText) == "" {
				fileText = fmt.Sprintf("[No extractable text found in %s]", file.Name)
			} else {
				fileText = parsedText
			}
		}

		filesContext.WriteString(fmt.Sprintf("=== File: %s ===\n", file.Name))
		filesContext.WriteString(fileText)
		filesContext.WriteString("\n\n")
	}

	filesContext.WriteString("User's question about the documents:\n")
	filesContext.WriteString(q)

	return filesContext.String()
}

// dispatchSlashCommand handles the slash commands that route directly to the
// command handler. It returns true when a command was handled (and a response
// written), in which case ChatHandler should return.
func (h *Handler) dispatchSlashCommand(w http.ResponseWriter, r *http.Request, q string, executionAgent executionAgentResolution) bool {
	switch {
	case q == "/help":
		h.commandHandler.HandleHelp(w, r)
	case q == "/agent":
		h.commandHandler.HandleAgentStatus(w, r, executionAgent)
	case q == "/agents":
		h.commandHandler.HandleAgentsList(w, r)
	case q == "/tools":
		h.commandHandler.HandleToolsList(w, r, executionAgent)
	case q == "/skills":
		h.commandHandler.HandleSkillsList(w, r, executionAgent)
	case q == "/exit":
		h.commandHandler.HandleExit(w, r)
	case q == "/version":
		h.commandHandler.HandleVersion(w, r)
	case strings.HasPrefix(q, "/switch"):
		// Parse the agent name from the command
		parts := strings.Fields(q)
		var agentName string
		if len(parts) > 1 {
			agentName = parts[1]
		}
		h.commandHandler.HandleSwitch(w, r, agentName)
	case strings.HasPrefix(q, "/workspace"):
		// Parse args after "/workspace"
		args := strings.TrimPrefix(q, "/workspace")
		h.commandHandler.HandleWorkspace(w, r, args)
	case strings.HasPrefix(q, "/openapp"):
		appName := strings.TrimSpace(strings.TrimPrefix(q, "/openapp"))
		h.commandHandler.HandleOpenApp(w, r, appName)
	default:
		return false
	}
	return true
}

// resolveInvokedSkill resolves an explicit ("/skill <name>") or implicit skill
// invocation from the question. It returns the resolved skill (which may be nil
// when no skill applies) and a done flag; when done is true a response has
// already been written and ChatHandler should return.
func (h *Handler) resolveInvokedSkill(w http.ResponseWriter, q string, executionAgent executionAgentResolution) (*skillInvocation, bool) {
	var invokedSkill *skillInvocation
	if strings.HasPrefix(q, "/skill") {
		if h.skillsManager == nil {
			orihttp.WriteJSON(w, map[string]any{
				"response": "❌ Skills are not enabled.",
			})
			return nil, true
		}
		name, args, err := parseSkillCommand(q)
		if err != nil {
			orihttp.WriteJSON(w, map[string]any{
				"response": fmt.Sprintf("❌ **Invalid command**: %v\n\nFormat: `/skill <name> <args>`", err),
			})
			return nil, true
		}
		skill, found, err := h.skillsManager.GetSkill(executionAgent.Name, name)
		if err != nil {
			var conflicts *skills.SkillConflictError
			if errors.As(err, &conflicts) {
				orihttp.WriteJSON(w, map[string]any{
					"response":  "❌ Duplicate skill names detected. Resolve conflicts in your skills folders before running skills.",
					"conflicts": conflicts.Conflicts,
				})
				return nil, true
			}
			orihttp.InternalError(w, err.Error())
			return nil, true
		}
		if !found {
			writeJSONResponse(w, attachDependencyResolution(map[string]any{
				"response": fmt.Sprintf("❌ Skill '%s' not found. Use /skills to list available skills.", name),
			}, buildSkillDependencyResolution(name, dependencyTypeSkillMissing)))
			return nil, true
		}
		if len(skill.ValidationErrors) > 0 {
			orihttp.WriteJSON(w, map[string]any{
				"response": fmt.Sprintf("❌ Skill '%s' has validation errors: %s", skill.Name, strings.Join(skill.ValidationErrors, "; ")),
			})
			return nil, true
		}
		if !skill.Enabled {
			writeJSONResponse(w, attachDependencyResolution(map[string]any{
				"response": fmt.Sprintf("❌ Skill '%s' is disabled.", skill.Name),
			}, buildSkillDependencyResolution(skill.Name, dependencyTypeSkillDisabled)))
			return nil, true
		}
		if skill.HasScripts && !skill.Trusted {
			writeJSONResponse(w, attachDependencyResolution(map[string]any{
				"response": fmt.Sprintf("❌ Skill '%s' requires trust before it can run.", skill.Name),
			}, buildSkillDependencyResolution(skill.Name, dependencyTypeSkillTrustRequired)))
			return nil, true
		}
		invokedSkill = &skillInvocation{Skill: skill, Args: args, Explicit: true}
	}

	if invokedSkill == nil {
		if name, args, ok := parseImplicitSkillCommand(q); ok && h.skillsManager != nil {
			skill, found, err := h.skillsManager.GetSkill(executionAgent.Name, name)
			if err != nil {
				var conflicts *skills.SkillConflictError
				if errors.As(err, &conflicts) {
					orihttp.WriteJSON(w, map[string]any{
						"response":  "❌ Duplicate skill names detected. Resolve conflicts in your skills folders before running skills.",
						"conflicts": conflicts.Conflicts,
					})
					return nil, true
				}
				orihttp.InternalError(w, err.Error())
				return nil, true
			}
			if found {
				if len(skill.ValidationErrors) > 0 {
					orihttp.WriteJSON(w, map[string]any{
						"response": fmt.Sprintf("❌ Skill '%s' has validation errors: %s", skill.Name, strings.Join(skill.ValidationErrors, "; ")),
					})
					return nil, true
				}
				if !skill.Enabled {
					writeJSONResponse(w, attachDependencyResolution(map[string]any{
						"response": fmt.Sprintf("❌ Skill '%s' is disabled.", skill.Name),
					}, buildSkillDependencyResolution(skill.Name, dependencyTypeSkillDisabled)))
					return nil, true
				}
				if skill.HasScripts && !skill.Trusted {
					writeJSONResponse(w, attachDependencyResolution(map[string]any{
						"response": fmt.Sprintf("❌ Skill '%s' requires trust before it can run.", skill.Name),
					}, buildSkillDependencyResolution(skill.Name, dependencyTypeSkillTrustRequired)))
					return nil, true
				}
				invokedSkill = &skillInvocation{Skill: skill, Args: args, Explicit: false}
			}
		}
	}
	return invokedSkill, false
}
