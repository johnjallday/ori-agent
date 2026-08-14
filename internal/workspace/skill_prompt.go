package workspace

import (
	"strings"
)

// AppendSkillPromptsFromResolved appends an "Active Skills" section built from
// the agent's resolved (enabled + bound) skills to a base system prompt. It is
// shared by the chat path and the task/orchestration path so both deliver skill
// instructions to the agent identically. When no skill carries a prompt, the
// base prompt is returned unchanged (so skill-less agents pay nothing).
//
// Skill CONFIG is deliberately not rendered here any more.
//
// There used to be a "Workspace Binding Settings" block that serialized a
// planning skill's config into the prompt — write_prd, require_branch,
// default_execution_mode. It read like policy and was, in fact, a paragraph: a
// model could ignore any of it and nothing would notice. Those controls are
// compiled now and live in the workspace's effective planning policy, where
// enabled and enforced mean what they say (FR-181, FR-182).
//
// A skill's PROMPT still reaches the agent. Skills remain context and
// capability; they are simply no longer a place policy can be declared.
func AppendSkillPromptsFromResolved(base string, skills []ResolvedSkill) string {
	var hasPrompt bool
	for _, s := range skills {
		if strings.TrimSpace(s.Prompt) != "" {
			hasPrompt = true
			break
		}
	}
	if !hasPrompt {
		return base
	}

	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\n---\n# Active Skills\n")
	for _, s := range skills {
		prompt := strings.TrimSpace(s.Prompt)
		if prompt == "" {
			continue
		}
		sb.WriteString("\n## ")
		sb.WriteString(s.Name)
		sb.WriteString("\n")
		sb.WriteString(prompt)
		sb.WriteString("\n")
	}
	return sb.String()
}
