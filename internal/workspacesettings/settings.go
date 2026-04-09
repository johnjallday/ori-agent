package workspacesettings

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const SharedDataKey = "workspace_settings"

type Settings struct {
	Version   int              `json:"version"`
	Preset    string           `json:"preset,omitempty"`
	Workflow  WorkflowSettings `json:"workflow"`
	Planning  PlanningSettings `json:"planning"`
	UpdatedAt time.Time        `json:"updated_at,omitempty"`
}

type WorkflowSettings struct {
	Mode                       string `json:"mode,omitempty"`
	RequireRepoScan            bool   `json:"require_repo_scan"`
	SaveOutputsAsNotes         bool   `json:"save_outputs_as_notes"`
	SyncPlansToTasks           bool   `json:"sync_plans_to_tasks"`
	AskBeforeSpecialistHandoff bool   `json:"ask_before_specialist_handoff"`
	ConfirmationMode           string `json:"confirmation_mode,omitempty"`
}

type PlanningSettings struct {
	Enabled              bool   `json:"enabled"`
	Mode                 string `json:"mode,omitempty"`
	WritePRD             bool   `json:"write_prd"`
	WriteTaskList        bool   `json:"write_task_list"`
	TasksDir             string `json:"tasks_dir,omitempty"`
	ClarificationMode    string `json:"clarification_mode,omitempty"`
	DefaultExecutionMode string `json:"default_execution_mode,omitempty"`
	RequireBranch        bool   `json:"require_branch"`
}

type EffectiveBehavior struct {
	Workflow      WorkflowSettings `json:"workflow"`
	Planning      PlanningSettings `json:"planning"`
	ManagedSkills []ManagedSkill   `json:"managed_skills,omitempty"`
	Summary       []string         `json:"summary,omitempty"`
}

type ManagedSkill struct {
	SkillName string                 `json:"skill_name"`
	Source    string                 `json:"source"`
	Active    bool                   `json:"active"`
	Reason    string                 `json:"reason,omitempty"`
	Config    map[string]interface{} `json:"config,omitempty"`
}

func DefaultSettings() Settings {
	return PresetDefaults("guided")
}

func PresetDefaults(preset string) Settings {
	normalizedPreset := normalizePreset(preset)

	settings := Settings{
		Version: 1,
		Preset:  normalizedPreset,
		Workflow: WorkflowSettings{
			Mode:                       "guided",
			RequireRepoScan:            false,
			SaveOutputsAsNotes:         true,
			SyncPlansToTasks:           false,
			AskBeforeSpecialistHandoff: true,
			ConfirmationMode:           "destructive_only",
		},
		Planning: PlanningSettings{
			Enabled:              false,
			Mode:                 "feature",
			WritePRD:             true,
			WriteTaskList:        true,
			TasksDir:             "tasks",
			ClarificationMode:    "standard",
			DefaultExecutionMode: "step_through",
			RequireBranch:        true,
		},
	}

	switch normalizedPreset {
	case "minimal":
		settings.Workflow.Mode = "direct"
		settings.Workflow.SaveOutputsAsNotes = false
		settings.Workflow.AskBeforeSpecialistHandoff = false
		settings.Workflow.ConfirmationMode = "destructive_only"
		settings.Planning.Enabled = false
		settings.Planning.WritePRD = false
		settings.Planning.WriteTaskList = false
	case "planner":
		settings.Workflow.Mode = "plan_then_execute"
		settings.Workflow.RequireRepoScan = true
		settings.Workflow.SaveOutputsAsNotes = true
		settings.Workflow.SyncPlansToTasks = true
		settings.Workflow.AskBeforeSpecialistHandoff = true
		settings.Workflow.ConfirmationMode = "destructive_only"
		settings.Planning.Enabled = true
	case "autonomous":
		settings.Workflow.Mode = "plan_then_execute"
		settings.Workflow.RequireRepoScan = true
		settings.Workflow.SaveOutputsAsNotes = true
		settings.Workflow.SyncPlansToTasks = true
		settings.Workflow.AskBeforeSpecialistHandoff = false
		settings.Workflow.ConfirmationMode = "none"
		settings.Planning.Enabled = true
		settings.Planning.DefaultExecutionMode = "auto"
	case "custom":
		settings.Preset = "custom"
	}

	return settings
}

func Normalize(settings Settings) Settings {
	return decode(toMap(settings))
}

func Extract(sharedData map[string]interface{}) Settings {
	if len(sharedData) == 0 {
		return DefaultSettings()
	}
	raw, ok := sharedData[SharedDataKey]
	if !ok {
		return DefaultSettings()
	}
	return decode(raw)
}

func ExtractRaw(sharedData map[string]interface{}) map[string]interface{} {
	if len(sharedData) == 0 {
		return toMap(DefaultSettings())
	}
	raw, ok := sharedData[SharedDataKey]
	if !ok {
		return toMap(DefaultSettings())
	}
	if asMap := mapValue(raw); len(asMap) > 0 {
		return asMap
	}
	return toMap(decode(raw))
}

func Store(sharedData map[string]interface{}, settings Settings) map[string]interface{} {
	out := cloneMap(sharedData)
	if out == nil {
		out = make(map[string]interface{})
	}
	settings = Normalize(settings)
	if settings.UpdatedAt.IsZero() {
		settings.UpdatedAt = time.Now().UTC()
	}
	out[SharedDataKey] = toMap(settings)
	return out
}

func ApplyPatch(sharedData map[string]interface{}, patch map[string]interface{}) (map[string]interface{}, Settings) {
	base := ExtractRaw(sharedData)
	if presetValue := strings.TrimSpace(stringValue(patch["preset"])); presetValue != "" {
		base = toMap(PresetDefaults(presetValue))
	}
	merged := mergeMaps(base, patch)
	settings := decode(merged)
	settings.UpdatedAt = time.Now().UTC()
	return Store(sharedData, settings), settings
}

func BuildEffectiveBehavior(settings Settings) EffectiveBehavior {
	settings = Normalize(settings)
	behavior := EffectiveBehavior{
		Workflow: settings.Workflow,
		Planning: settings.Planning,
		Summary: []string{
			fmt.Sprintf("Interaction mode: %s", settings.Workflow.Mode),
			fmt.Sprintf("Require repo scan before code work: %t", settings.Workflow.RequireRepoScan),
			fmt.Sprintf("Save useful outputs as workspace notes: %t", settings.Workflow.SaveOutputsAsNotes),
			fmt.Sprintf("Sync approved plans to workspace tasks: %t", settings.Workflow.SyncPlansToTasks),
			fmt.Sprintf("Ask before specialist handoff: %t", settings.Workflow.AskBeforeSpecialistHandoff),
			fmt.Sprintf("Confirmation mode: %s", settings.Workflow.ConfirmationMode),
			fmt.Sprintf("Planning profile enabled: %t", settings.Planning.Enabled),
		},
	}
	if settings.Planning.Enabled {
		behavior.ManagedSkills = []ManagedSkill{
			{
				SkillName: "workspace-planning",
				Source:    "settings",
				Active:    true,
				Reason:    "planning.enabled",
				Config:    ToPlanningSkillConfig(settings),
			},
		}
	}
	return behavior
}

func ToPlanningSkillConfig(settings Settings) map[string]interface{} {
	settings = Normalize(settings)
	return map[string]interface{}{
		"profile_type":           "workspace_planning",
		"mode":                   settings.Planning.Mode,
		"write_prd":              settings.Planning.WritePRD,
		"write_task_list":        settings.Planning.WriteTaskList,
		"tasks_dir":              settings.Planning.TasksDir,
		"clarification_mode":     settings.Planning.ClarificationMode,
		"sync_workspace_tasks":   settings.Workflow.SyncPlansToTasks,
		"default_execution_mode": settings.Planning.DefaultExecutionMode,
		"require_branch":         settings.Planning.RequireBranch,
	}
}

func decode(raw interface{}) Settings {
	decoded := PresetDefaults(stringValue(mapValue(raw)["preset"]))
	rawMap := mapValue(raw)

	if version := intValue(rawMap["version"]); version > 0 {
		decoded.Version = version
	}
	if updatedAt := timeValue(rawMap["updated_at"]); !updatedAt.IsZero() {
		decoded.UpdatedAt = updatedAt
	}

	workflowMap := mapValue(rawMap["workflow"])
	if workflowMap != nil {
		if value := normalizeWorkflowMode(stringValue(workflowMap["mode"])); value != "" {
			decoded.Workflow.Mode = value
		}
		if value, ok := boolValue(workflowMap["require_repo_scan"]); ok {
			decoded.Workflow.RequireRepoScan = value
		}
		if value, ok := boolValue(workflowMap["save_outputs_as_notes"]); ok {
			decoded.Workflow.SaveOutputsAsNotes = value
		}
		if value, ok := boolValue(workflowMap["sync_plans_to_tasks"]); ok {
			decoded.Workflow.SyncPlansToTasks = value
		}
		if value, ok := boolValue(workflowMap["ask_before_specialist_handoff"]); ok {
			decoded.Workflow.AskBeforeSpecialistHandoff = value
		}
		if value := normalizeConfirmationMode(stringValue(workflowMap["confirmation_mode"])); value != "" {
			decoded.Workflow.ConfirmationMode = value
		}
	}

	planningMap := mapValue(rawMap["planning"])
	if planningMap != nil {
		if value, ok := boolValue(planningMap["enabled"]); ok {
			decoded.Planning.Enabled = value
		}
		if value := normalizePlanningMode(stringValue(planningMap["mode"])); value != "" {
			decoded.Planning.Mode = value
		}
		if value, ok := boolValue(planningMap["write_prd"]); ok {
			decoded.Planning.WritePRD = value
		}
		if value, ok := boolValue(planningMap["write_task_list"]); ok {
			decoded.Planning.WriteTaskList = value
		}
		if value := strings.TrimSpace(stringValue(planningMap["tasks_dir"])); value != "" {
			decoded.Planning.TasksDir = value
		}
		if value := normalizeClarificationMode(stringValue(planningMap["clarification_mode"])); value != "" {
			decoded.Planning.ClarificationMode = value
		}
		if value := normalizeDefaultExecutionMode(stringValue(planningMap["default_execution_mode"])); value != "" {
			decoded.Planning.DefaultExecutionMode = value
		}
		if value, ok := boolValue(planningMap["require_branch"]); ok {
			decoded.Planning.RequireBranch = value
		}
	}

	decoded.Preset = normalizePreset(decoded.Preset)
	if decoded.Version <= 0 {
		decoded.Version = 1
	}
	return decoded
}

func toMap(value interface{}) map[string]interface{} {
	data, err := json.Marshal(value)
	if err != nil {
		return map[string]interface{}{}
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]interface{}{}
	}
	if out == nil {
		return map[string]interface{}{}
	}
	return out
}

func cloneMap(value map[string]interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	return toMap(value)
}

func mergeMaps(base map[string]interface{}, patch map[string]interface{}) map[string]interface{} {
	merged := cloneMap(base)
	if merged == nil {
		merged = make(map[string]interface{})
	}
	for key, value := range patch {
		if value == nil {
			continue
		}
		patchMap := mapValue(value)
		if patchMap != nil {
			baseMap := mapValue(merged[key])
			merged[key] = mergeMaps(baseMap, patchMap)
			continue
		}
		merged[key] = value
	}
	return merged
}

func mapValue(value interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		return cloneMap(typed)
	default:
		mapped := toMap(typed)
		if len(mapped) == 0 {
			return nil
		}
		return mapped
	}
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func intValue(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func boolValue(value interface{}) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func timeValue(value interface{}) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed
	case string:
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(typed)); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func normalizePreset(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "minimal":
		return "minimal"
	case "planner":
		return "planner"
	case "autonomous":
		return "autonomous"
	case "custom":
		return "custom"
	case "guided":
		fallthrough
	default:
		return "guided"
	}
}

func normalizeWorkflowMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "direct":
		return "direct"
	case "plan_then_execute":
		return "plan_then_execute"
	case "guided":
		fallthrough
	default:
		return "guided"
	}
}

func normalizeConfirmationMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none":
		return "none"
	case "always":
		return "always"
	case "destructive_only":
		fallthrough
	default:
		return "destructive_only"
	}
}

func normalizePlanningMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "bugfix":
		return "bugfix"
	case "refactor":
		return "refactor"
	case "investigation":
		return "investigation"
	case "feature":
		fallthrough
	default:
		return "feature"
	}
}

func normalizeClarificationMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "minimal":
		return "minimal"
	case "deep":
		return "deep"
	case "standard":
		fallthrough
	default:
		return "standard"
	}
}

func normalizeDefaultExecutionMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "auto":
		return "auto"
	case "step_through":
		fallthrough
	default:
		return "step_through"
	}
}
