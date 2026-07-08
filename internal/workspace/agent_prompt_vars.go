package workspace

import (
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/promptvars"
)

// PromptVarInputs carries the already-fetched pieces needed to populate the
// closed prompt-variable vocabulary (internal/promptvars). Callers supply what
// they can cheaply access from their path; any block variable left blank
// self-omits at resolution time, so partial inputs degrade gracefully.
type PromptVarInputs struct {
	Workspace   *Workspace
	Instance    AgentInstance
	AgentName   string
	Memory      string // raw MEMORY.md (fenced at render)
	NotesRecent string // recent notes text (fenced at render)
	Tools       string // available tool/skill names
	TaskGoal    string // current task goal (task path only)
}

// BuildPromptVarValues maps PromptVarInputs onto the closed vocabulary keys.
func BuildPromptVarValues(in PromptVarInputs) map[string]string {
	values := map[string]string{
		"workspace.custom_instructions": in.Instance.CustomInstructions,
		"agent.name":                    strings.TrimSpace(in.AgentName),
		"agent.role":                    in.Instance.Role,
		"agent.description":             in.Instance.Description,
		"workspace.memory":              in.Memory,
		"workspace.notes.recent":        in.NotesRecent,
		"workspace.tools":               in.Tools,
		"task.goal":                     in.TaskGoal,
		"runtime.date":                  time.Now().Format("January 2, 2006"),
	}
	if in.Workspace != nil {
		values["workspace.name"] = in.Workspace.Name
		values["workspace.description"] = in.Workspace.Description
	}
	return values
}

// FormatToolNames renders a compact, de-duplicated, comma-separated list of the
// agent's effective tool/skill names for the workspace.tools variable.
func FormatToolNames(skillNames, mcpNames []string) string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(skillNames)+len(mcpNames))
	for _, group := range [][]string{skillNames, mcpNames} {
		for _, n := range group {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			key := strings.ToLower(n)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, n)
		}
	}
	return strings.Join(out, ", ")
}

// ResolveAgentBasePrompt resolves the closed prompt variables in prompt using
// inputs. When prompt contains no variables it is returned unchanged with
// hadVars=false, letting callers decide whether to suppress the generic
// workspace-context layer (PRD FR24): a variable-bearing prompt has the author
// place context explicitly, so the generic block is not also appended.
func ResolveAgentBasePrompt(prompt string, in PromptVarInputs) (resolved string, hadVars bool) {
	if !promptvars.HasVariables(prompt) {
		return prompt, false
	}
	return promptvars.Resolve(prompt, BuildPromptVarValues(in)), true
}
