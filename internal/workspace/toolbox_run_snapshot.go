package workspace

import (
	"strings"
	"time"
)

// Building a run's Toolbox snapshot from live workspace state (PRD FR-107,
// FR-108).
//
// This lives in the workspace package rather than in workspacerun because it
// needs the Workshop — bindings, scopes, classifications — and workspacerun
// must not depend on that. The result is a plain value the run package stores
// verbatim, which is also what keeps the dependency one-directional.
//
// The output type is deliberately mirror-shaped rather than shared: the run
// package owns the persisted schema, and coupling the two would mean a
// workspace refactor could silently change what historical runs claim they had.

// RunSnapshotSkill mirrors workspacerun.SnapshotSkill.
type RunSnapshotSkill struct {
	CapabilityID string `json:"capability_id"`
	DisplayName  string `json:"display_name,omitempty"`
	Source       string `json:"source"`
	BindingID    string `json:"binding_id,omitempty"`
	PromptChars  int    `json:"prompt_chars,omitempty"`
}

// RunSnapshotMCP mirrors workspacerun.SnapshotMCP.
type RunSnapshotMCP struct {
	BindingID         string            `json:"binding_id"`
	ServerName        string            `json:"server_name,omitempty"`
	Alias             string            `json:"alias,omitempty"`
	RuntimeServerName string            `json:"runtime_server_name,omitempty"`
	AllowedTools      []string          `json:"allowed_tools,omitempty"`
	Scope             map[string]any    `json:"scope,omitempty"`
	DefaultSideEffect string            `json:"default_side_effect,omitempty"`
	ToolRisks         map[string]string `json:"tool_risks,omitempty"`
}

// RunToolboxSnapshotData is everything the run package needs to persist.
type RunToolboxSnapshotData struct {
	WorkspaceID      string
	WorkspaceVersion int64
	AgentInstanceID  string
	AgentName        string
	AgentModel       string
	AgentProvider    string

	ToolboxID      string
	ToolboxName    string
	ToolboxVersion int64
	PinnedByGoal   bool

	Skills      []RunSnapshotSkill
	MCPBindings []RunSnapshotMCP

	FocusState     string
	FocusReasons   []string
	FocusInputs    map[string]any
	SkillSpaces    int
	AutonomyPolicy string

	CreatedAt time.Time
}

// BuildRunToolboxSnapshot resolves the capabilities one agent instance would be
// given right now, in the exact form a run should freeze.
//
// It reuses PreviewToolbox rather than re-deriving, so what a run records is
// literally what the user was shown before starting it. Two implementations
// that could disagree would eventually disagree.
//
// Returns ok=false when the instance has no explicit assignment — a workspace
// mid-migration. The caller records no snapshot rather than a misleading empty
// one: absent means "unknown", never "unrestricted".
func BuildRunToolboxSnapshot(
	ws *Workspace,
	agentInstanceID string,
	learned []ResolvedSkill,
	capacity int,
	expertMode bool,
	thresholds FocusThresholds,
) (RunToolboxSnapshotData, bool) {
	if ws == nil {
		return RunToolboxSnapshotData{}, false
	}
	instance := findAgentInstanceByID(ws, agentInstanceID)
	if instance == nil {
		return RunToolboxSnapshotData{}, false
	}

	// A Goal's pinned version wins over the instance's current assignment,
	// which is what makes a recurring goal reproducible (FR-103, FR-104).
	definition, recipe, pinned, ok := resolveRunToolbox(ws, instance)
	if !ok {
		return RunToolboxSnapshotData{}, false
	}

	preview := PreviewToolbox(ws, instance, definition, recipe, learned, capacity, expertMode, thresholds)

	data := RunToolboxSnapshotData{
		WorkspaceID:      ws.ID,
		WorkspaceVersion: ws.Version,
		AgentInstanceID:  instance.ID,
		AgentName:        instance.Name,
		ToolboxID:        definition.ID,
		ToolboxName:      definition.Name,
		ToolboxVersion:   recipe.Version,
		PinnedByGoal:     pinned,
		FocusState:       preview.Focus.State,
		FocusReasons:     append([]string(nil), preview.Focus.Reasons...),
		FocusInputs:      focusInputsMap(preview.Focus.Inputs),
		SkillSpaces:      preview.Capacity.Used,
		AutonomyPolicy:   string(ws.AutonomyPolicy),
		CreatedAt:        time.Now(),
	}

	// Only AVAILABLE capabilities enter the snapshot. An unresolvable optional
	// entry contributed nothing at runtime, so recording it would overstate
	// what the run actually had (FR-14).
	for _, skill := range preview.Skills {
		if !skill.Available {
			continue
		}
		data.Skills = append(data.Skills, RunSnapshotSkill{
			CapabilityID: skill.CapabilityID,
			DisplayName:  skill.DisplayName,
			Source:       skill.Source,
			BindingID:    skill.BindingID,
			PromptChars:  skill.PromptChars,
		})
	}

	for _, binding := range preview.MCPBindings {
		if !binding.Available {
			continue
		}
		data.MCPBindings = append(data.MCPBindings, RunSnapshotMCP{
			BindingID:  binding.BindingID,
			ServerName: binding.ServerName,
			Alias:      binding.Alias,
			// The materialized name is the join key between this snapshot and
			// the trace events the run will emit, so a Wrap-up can attribute a
			// tool call to the binding that provided it rather than guessing
			// from the tool's name.
			RuntimeServerName: RuntimeMCPServerName(ws.ID, binding.ServerName, binding.BindingID),
			AllowedTools:      append([]string(nil), binding.AllowedTools...),
			Scope:             binding.Scope,
			DefaultSideEffect: binding.DefaultSideEffect,
			ToolRisks:         binding.ToolRisks,
		})
	}
	return data, true
}

// resolveRunToolbox picks the Toolbox version a run should use: a Goal's pin
// when one applies to this instance, otherwise the instance's assignment.
func resolveRunToolbox(ws *Workspace, instance *AgentInstance) (ToolboxDefinition, ToolboxRecipe, bool, bool) {
	policy := ws.GoalToolboxPolicy
	if policy.Pinned() && strings.EqualFold(strings.TrimSpace(policy.EntryAgentInstanceID), strings.TrimSpace(instance.ID)) {
		if definition, exists := ws.GetToolbox(policy.ToolboxID); exists {
			if recipe, err := definition.ResolveVersion(policy.ToolboxVersion); err == nil {
				return *definition, recipe, true, true
			}
		}
		// A broken pin is the preflight's problem to report; falling through to
		// the current assignment here would quietly run the goal with something
		// other than what it was pinned to (FR-105).
		return ToolboxDefinition{}, ToolboxRecipe{}, false, false
	}

	definition, recipe, assigned, err := ws.ResolveAssignedToolbox(instance.ID)
	if err != nil || !assigned {
		return ToolboxDefinition{}, ToolboxRecipe{}, false, false
	}
	return definition, recipe, false, true
}

// focusInputsMap flattens the Focus inputs for storage.
//
// A map rather than the struct so the run schema does not have to change every
// time Focus learns to measure something new — a historical run keeps whatever
// was recorded, and a reader shows what it finds.
func focusInputsMap(inputs FocusInputs) map[string]any {
	return map[string]any{
		"active_skills":           inputs.ActiveSkills,
		"skill_capacity":          inputs.SkillCapacity,
		"expert_mode":             inputs.ExpertMode,
		"core_capabilities":       inputs.CoreCapabilities,
		"exposed_operations":      inputs.ExposedOperations,
		"unpinned_bindings":       inputs.UnpinnedBindings,
		"read_operations":         inputs.ReadOperations,
		"write_operations":        inputs.WriteOperations,
		"external_operations":     inputs.ExternalOperations,
		"unclassified_operations": inputs.UnclassifiedOperations,
		"prompt_chars":            inputs.PromptChars,
		"memory_context_chars":    inputs.MemoryContextChars,
		"overlap_groups":          len(inputs.OverlapGroups),
	}
}
