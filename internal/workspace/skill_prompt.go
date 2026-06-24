package workspace

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AppendSkillPromptsFromResolved appends an "Active Skills" section built from
// the agent's resolved (enabled + bound) skills to a base system prompt. It is
// shared by the chat path and the task/orchestration path so both deliver skill
// instructions to the agent identically. When no skill carries a prompt or
// runtime settings, the base prompt is returned unchanged (so skill-less agents
// pay nothing).
func AppendSkillPromptsFromResolved(base string, skills []ResolvedSkill) string {
	var hasPrompt bool
	for _, s := range skills {
		if strings.TrimSpace(s.Prompt) != "" || strings.TrimSpace(formatResolvedSkillRuntimeSettings(s)) != "" {
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
		settings := strings.TrimSpace(formatResolvedSkillRuntimeSettings(s))
		if prompt == "" && settings == "" {
			continue
		}
		sb.WriteString("\n## ")
		sb.WriteString(s.Name)
		sb.WriteString("\n")
		if prompt != "" {
			sb.WriteString(prompt)
			sb.WriteString("\n")
		}
		if settings != "" {
			sb.WriteString("\n### Workspace Binding Settings\n")
			sb.WriteString(settings)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func formatResolvedSkillRuntimeSettings(skill ResolvedSkill) string {
	if !skill.PlanningProfile || len(skill.Config) == 0 {
		return ""
	}

	type planningSettings struct {
		ProfileType          string `json:"profile_type,omitempty"`
		Mode                 string `json:"mode,omitempty"`
		WritePRD             bool   `json:"write_prd,omitempty"`
		WriteTaskList        bool   `json:"write_task_list,omitempty"`
		TasksDir             string `json:"tasks_dir,omitempty"`
		ClarificationMode    string `json:"clarification_mode,omitempty"`
		SyncWorkspaceTasks   bool   `json:"sync_workspace_tasks,omitempty"`
		DefaultExecutionMode string `json:"default_execution_mode,omitempty"`
		RequireBranch        bool   `json:"require_branch,omitempty"`
	}

	settings := planningSettings{
		ProfileType:          "workspace_planning",
		Mode:                 "feature",
		WritePRD:             true,
		WriteTaskList:        true,
		TasksDir:             "tasks",
		ClarificationMode:    "standard",
		SyncWorkspaceTasks:   true,
		DefaultExecutionMode: "step_through",
		RequireBranch:        true,
	}
	if value := strings.TrimSpace(stringConfigValue(skill.Config, "profile_type")); value != "" {
		settings.ProfileType = value
	}
	if value := strings.TrimSpace(stringConfigValue(skill.Config, "mode")); value != "" {
		settings.Mode = value
	}
	if value := strings.TrimSpace(stringConfigValue(skill.Config, "tasks_dir")); value != "" {
		settings.TasksDir = value
	}
	if value := strings.TrimSpace(stringConfigValue(skill.Config, "clarification_mode")); value != "" {
		settings.ClarificationMode = value
	}
	if value := strings.TrimSpace(stringConfigValue(skill.Config, "default_execution_mode")); value != "" {
		settings.DefaultExecutionMode = value
	}
	settings.WritePRD = boolConfigValue(skill.Config, "write_prd", settings.WritePRD)
	settings.WriteTaskList = boolConfigValue(skill.Config, "write_task_list", settings.WriteTaskList)
	settings.SyncWorkspaceTasks = boolConfigValue(skill.Config, "sync_workspace_tasks", settings.SyncWorkspaceTasks)
	settings.RequireBranch = boolConfigValue(skill.Config, "require_branch", settings.RequireBranch)

	artifacts := make([]string, 0, 2)
	if settings.WritePRD {
		artifacts = append(artifacts, "PRD")
	}
	if settings.WriteTaskList {
		artifacts = append(artifacts, "task list")
	}
	if len(artifacts) == 0 {
		artifacts = append(artifacts, "none by default")
	}

	lines := []string{
		"Use these workspace-level planning defaults unless the user explicitly asks for something different.",
		fmt.Sprintf("- Planning mode: %s", settings.Mode),
		fmt.Sprintf("- Preferred planning artifacts: %s", strings.Join(artifacts, ", ")),
		fmt.Sprintf("- Save planning files under: %s", settings.TasksDir),
		fmt.Sprintf("- Clarification depth: %s", settings.ClarificationMode),
		fmt.Sprintf("- Sync approved plans into workspace tasks: %t", settings.SyncWorkspaceTasks),
		fmt.Sprintf("- Default workspace task execution mode: %s", settings.DefaultExecutionMode),
		fmt.Sprintf("- Require feature branch before implementation: %t", settings.RequireBranch),
	}

	if payload, err := json.MarshalIndent(settings, "", "  "); err == nil {
		lines = append(lines, "- Normalized config JSON:", string(payload))
	}

	return strings.Join(lines, "\n")
}

func stringConfigValue(config map[string]any, key string) string {
	if len(config) == 0 {
		return ""
	}
	value, ok := config[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", value)
	}
}

func boolConfigValue(config map[string]any, key string, fallback bool) bool {
	if len(config) == 0 {
		return fallback
	}
	value, ok := config[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		normalized := strings.TrimSpace(strings.ToLower(typed))
		switch normalized {
		case "true", "yes", "1":
			return true
		case "false", "no", "0":
			return false
		default:
			return fallback
		}
	default:
		return fallback
	}
}
