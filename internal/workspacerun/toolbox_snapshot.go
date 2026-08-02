package workspacerun

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// The run Toolbox snapshot: the exact capabilities a run was given, frozen
// before the model was invoked (PRD FR-107–FR-113).
//
// It is DENORMALIZED on purpose. Storing "toolbox tbx-7 v3" and resolving it
// later would make a completed run's history depend on state that is free to
// change afterwards — and it does change: toolboxes get edited, archived, and
// deleted, bindings get disconnected, agents get re-pointed. A run that
// finished last Tuesday must still be able to say exactly what it had, so the
// snapshot copies the answer rather than a pointer to it (FR-110).
//
// The same copy is what the runtime builds its prompt and tool list from for
// the life of the run, which is what makes "editing a toolbox mid-run cannot
// change the run" true rather than merely intended (FR-112).

// RunToolboxSnapshot is the immutable capability record for one run.
type RunToolboxSnapshot struct {
	// --- Identity: who ran, in which workspace, at which version ---
	WorkspaceID      string `json:"workspace_id"`
	WorkspaceVersion int64  `json:"workspace_version"`
	// AgentInstanceID is the stable instance. AgentName is carried alongside
	// because a name is what a person reads, and the instance may be gone by
	// the time anyone reads the report.
	AgentInstanceID string `json:"agent_instance_id,omitempty"`
	AgentName       string `json:"agent_name,omitempty"`
	// AgentModel / AgentProvider record the global agent identity in force.
	AgentModel    string `json:"agent_model,omitempty"`
	AgentProvider string `json:"agent_provider,omitempty"`

	// --- The Toolbox ---
	ToolboxID      string `json:"toolbox_id,omitempty"`
	ToolboxName    string `json:"toolbox_name,omitempty"`
	ToolboxVersion int64  `json:"toolbox_version,omitempty"`
	// Hash is a content hash of the effective capabilities below. Two runs with
	// the same hash were given the same thing, even across toolboxes — which is
	// what makes "was this actually the same setup?" answerable without diffing
	// by eye.
	Hash string `json:"hash,omitempty"`
	// PinnedByGoal records that a Goal pinned this version rather than the run
	// resolving the agent's current assignment (FR-103).
	PinnedByGoal bool `json:"pinned_by_goal,omitempty"`

	// --- Effective capabilities ---
	Skills      []SnapshotSkill `json:"skills,omitempty"`
	MCPBindings []SnapshotMCP   `json:"mcp_bindings,omitempty"`

	// --- Assessment and policy at start time ---
	FocusState   string         `json:"focus_state,omitempty"`
	FocusReasons []string       `json:"focus_reasons,omitempty"`
	FocusInputs  map[string]any `json:"focus_inputs,omitempty"`
	SkillSpaces  int            `json:"skill_spaces"`
	// AutonomyPolicy is the ceiling the run executed under.
	AutonomyPolicy string `json:"autonomy_policy,omitempty"`

	// --- Phase 2 reservation ---
	//
	// These stay EMPTY in V1. They exist now so the snapshot's shape does not
	// change when Field Notes arrive, and so a Phase 2 run can say which
	// remembered thing influenced it without a migration (FR-109, deferred
	// FR-136, FR-139–FR-143).
	FieldNoteRefs      []SnapshotMemoryRef `json:"field_note_refs,omitempty"`
	WorkspaceMemoryRef []SnapshotMemoryRef `json:"workspace_memory_refs,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// SnapshotSkill is one skill the run was given, with the exact source it came
// from (FR-108).
type SnapshotSkill struct {
	CapabilityID string `json:"capability_id"`
	DisplayName  string `json:"display_name,omitempty"`
	// Source is agent_learned, workspace_provided, or core.
	Source string `json:"source"`
	// BindingID names the workspace binding for a workspace-provided skill.
	BindingID string `json:"binding_id,omitempty"`
	// PromptChars records how much instruction text it contributed, which is
	// what lets a Wrap-up talk about context cost without storing the prompt.
	PromptChars int `json:"prompt_chars,omitempty"`
}

// SnapshotMCP is one binding the run was given, with the exact operations it
// could actually call.
type SnapshotMCP struct {
	BindingID  string `json:"binding_id"`
	ServerName string `json:"server_name,omitempty"`
	Alias      string `json:"alias,omitempty"`
	// RuntimeServerName is the materialized name the runtime used. It is what
	// trace events carry, so it is the join key between the snapshot and what
	// actually happened.
	RuntimeServerName string `json:"runtime_server_name,omitempty"`
	// AllowedTools is the exact operation list. A run may call these and
	// nothing else (FR-112).
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// Scope, DefaultSideEffect, and ToolRisks record what those operations were
	// permitted to reach and what they do.
	Scope             map[string]any    `json:"scope,omitempty"`
	DefaultSideEffect string            `json:"default_side_effect,omitempty"`
	ToolRisks         map[string]string `json:"tool_risks,omitempty"`
}

// SnapshotMemoryRef identifies one remembered item supplied to a run, by ID and
// content hash so a later reader can tell whether it has since changed.
// Reserved for Phase 2; always empty in V1.
type SnapshotMemoryRef struct {
	ID          string `json:"id"`
	ContentHash string `json:"content_hash,omitempty"`
}

// ComputeHash returns a stable content hash of the effective capabilities.
//
// Deliberately excludes identity and timestamps: the question it answers is
// "were these two runs given the same capabilities?", not "were they the same
// run". Sorting before hashing is what makes it stable across the order the
// capabilities happened to be assembled in.
func (s *RunToolboxSnapshot) ComputeHash() string {
	if s == nil {
		return ""
	}

	parts := make([]string, 0, len(s.Skills)+len(s.MCPBindings))
	for _, skill := range s.Skills {
		parts = append(parts, "skill:"+skill.Source+":"+skill.CapabilityID+":"+skill.BindingID)
	}
	for _, binding := range s.MCPBindings {
		tools := append([]string(nil), binding.AllowedTools...)
		sort.Strings(tools)
		parts = append(parts, "mcp:"+strings.ToLower(binding.BindingID)+":"+strings.Join(tools, ","))
	}
	sort.Strings(parts)

	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

// Clone returns a deep copy. Snapshots are immutable by contract, and a deep
// copy is how that contract survives a caller that does not know it.
func (s *RunToolboxSnapshot) Clone() *RunToolboxSnapshot {
	if s == nil {
		return nil
	}
	cp := *s
	cp.FocusReasons = append([]string(nil), s.FocusReasons...)
	if s.FocusInputs != nil {
		cp.FocusInputs = make(map[string]any, len(s.FocusInputs))
		for key, value := range s.FocusInputs {
			cp.FocusInputs[key] = value
		}
	}
	if len(s.Skills) > 0 {
		cp.Skills = append([]SnapshotSkill(nil), s.Skills...)
	}
	if len(s.MCPBindings) > 0 {
		cp.MCPBindings = make([]SnapshotMCP, len(s.MCPBindings))
		for i, binding := range s.MCPBindings {
			cp.MCPBindings[i] = binding
			cp.MCPBindings[i].AllowedTools = append([]string(nil), binding.AllowedTools...)
			if binding.Scope != nil {
				scope := make(map[string]any, len(binding.Scope))
				for key, value := range binding.Scope {
					scope[key] = value
				}
				cp.MCPBindings[i].Scope = scope
			}
			if binding.ToolRisks != nil {
				risks := make(map[string]string, len(binding.ToolRisks))
				for key, value := range binding.ToolRisks {
					risks[key] = value
				}
				cp.MCPBindings[i].ToolRisks = risks
			}
		}
	}
	cp.FieldNoteRefs = append([]SnapshotMemoryRef(nil), s.FieldNoteRefs...)
	cp.WorkspaceMemoryRef = append([]SnapshotMemoryRef(nil), s.WorkspaceMemoryRef...)
	return &cp
}

// AllowsTool reports whether the snapshot permits calling one operation on one
// materialized runtime server.
//
// This is the enforcement question FR-112 asks: a run may not dynamically
// acquire a capability its snapshot does not name. An unknown server or an
// unlisted tool answers false, so the default is refusal rather than
// permission.
func (s *RunToolboxSnapshot) AllowsTool(runtimeServerName, tool string) bool {
	if s == nil {
		return false
	}
	wantServer := strings.TrimSpace(runtimeServerName)
	wantTool := strings.ToLower(strings.TrimSpace(tool))
	for _, binding := range s.MCPBindings {
		if binding.RuntimeServerName != wantServer {
			continue
		}
		for _, allowed := range binding.AllowedTools {
			if strings.ToLower(strings.TrimSpace(allowed)) == wantTool {
				return true
			}
		}
		return false
	}
	return false
}

// OperationCount returns the number of concrete operations the run could call.
func (s *RunToolboxSnapshot) OperationCount() int {
	if s == nil {
		return 0
	}
	total := 0
	for _, binding := range s.MCPBindings {
		total += len(binding.AllowedTools)
	}
	return total
}

// ToolAllowlist rebuilds the runtime's server → tools map from the snapshot.
//
// The runtime constructs its tool list from THIS rather than from live
// workspace state, which is what makes an edit during a run unable to change
// what the run can do (FR-110, FR-112).
func (s *RunToolboxSnapshot) ToolAllowlist() map[string][]string {
	if s == nil || len(s.MCPBindings) == 0 {
		return nil
	}
	allowlist := make(map[string][]string, len(s.MCPBindings))
	for _, binding := range s.MCPBindings {
		if binding.RuntimeServerName == "" {
			continue
		}
		allowlist[binding.RuntimeServerName] = append([]string(nil), binding.AllowedTools...)
	}
	return allowlist
}

// RuntimeServers returns the materialized server names in snapshot order.
func (s *RunToolboxSnapshot) RuntimeServers() []string {
	if s == nil {
		return nil
	}
	servers := make([]string, 0, len(s.MCPBindings))
	for _, binding := range s.MCPBindings {
		if binding.RuntimeServerName != "" {
			servers = append(servers, binding.RuntimeServerName)
		}
	}
	return servers
}

// BoundedBy narrows this snapshot to the intersection with a parent's.
//
// A delegated subtask inherits the parent run's maximum capability and
// permission boundary (FR-111): it may use less than the parent had, never
// more. Intersecting rather than replacing is what makes that true even when
// the child was resolved from a toolbox that has since grown — the parent
// snapshot is the ceiling, and a separately reviewed run is the only way past
// it.
func (s *RunToolboxSnapshot) BoundedBy(parent *RunToolboxSnapshot) *RunToolboxSnapshot {
	if s == nil || parent == nil {
		return s.Clone()
	}

	bounded := s.Clone()

	parentSkills := make(map[string]struct{}, len(parent.Skills))
	for _, skill := range parent.Skills {
		parentSkills[skill.Source+":"+skill.CapabilityID] = struct{}{}
	}
	skills := make([]SnapshotSkill, 0, len(bounded.Skills))
	for _, skill := range bounded.Skills {
		if _, ok := parentSkills[skill.Source+":"+skill.CapabilityID]; ok {
			skills = append(skills, skill)
		}
	}
	bounded.Skills = skills

	parentTools := make(map[string]map[string]struct{}, len(parent.MCPBindings))
	for _, binding := range parent.MCPBindings {
		tools := make(map[string]struct{}, len(binding.AllowedTools))
		for _, tool := range binding.AllowedTools {
			tools[strings.ToLower(strings.TrimSpace(tool))] = struct{}{}
		}
		parentTools[strings.ToLower(strings.TrimSpace(binding.BindingID))] = tools
	}
	bindings := make([]SnapshotMCP, 0, len(bounded.MCPBindings))
	for _, binding := range bounded.MCPBindings {
		allowed, ok := parentTools[strings.ToLower(strings.TrimSpace(binding.BindingID))]
		if !ok {
			continue
		}
		tools := make([]string, 0, len(binding.AllowedTools))
		for _, tool := range binding.AllowedTools {
			if _, permitted := allowed[strings.ToLower(strings.TrimSpace(tool))]; permitted {
				tools = append(tools, tool)
			}
		}
		binding.AllowedTools = tools
		bindings = append(bindings, binding)
	}
	bounded.MCPBindings = bindings

	bounded.Hash = bounded.ComputeHash()
	return bounded
}
