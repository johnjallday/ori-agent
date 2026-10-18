package workspace

import "strings"

// readPrefixes are tool-name prefixes that strongly suggest the tool only
// observes state (no mutation). Used by SuggestSideEffect when the user is
// classifying bindings for the first time — purely advisory; the user always
// confirms before the suggestion takes effect.
var readPrefixes = []string{
	"read_",
	"get_",
	"list_",
	"search_",
	"find_",
	"describe_",
	"inspect_",
	"fetch_",
	"query_",
	"show_",
}

// SuggestSideEffect returns a heuristic classification for the given tool name
// based on its prefix. Returns SideEffectRead for names matching any of
// readPrefixes (case-insensitive). Returns the empty SideEffect for anything
// else — callers should treat "no suggestion" as "leave it to the user."
//
// Write and external classifications are deliberately not heuristic-suggested:
// the cost of false-positives (auto-classifying a tool as write/external when
// it really shouldn't be) is much lower than auto-classifying as read.
func SuggestSideEffect(toolName string) SideEffect {
	lower := strings.ToLower(toolName)
	for _, p := range readPrefixes {
		if strings.HasPrefix(lower, p) {
			return SideEffectRead
		}
	}
	return ""
}

// ResolveSideEffect determines the effective SideEffect classification for a
// tool call: per-tool override wins, then binding default, then empty (meaning
// "unclassified" — the autonomy gate must treat this as denied until the user
// classifies). This helper does NOT consult SuggestSideEffect at gate time so
// that the gate is fully deterministic and driven by user-confirmed state.
func ResolveSideEffect(defaultSE SideEffect, overrides map[string]SideEffect, toolName string) SideEffect {
	if override, ok := overrides[toolName]; ok && override != "" {
		return override
	}
	return defaultSE
}

// isValidSideEffect reports whether se is one of the known SideEffect values.
// Empty is also accepted by callers as "unclassified" — this helper only rejects
// strings that look like SideEffect but aren't one of the defined constants.
func isValidSideEffect(se SideEffect) bool {
	switch se {
	case SideEffectRead, SideEffectWrite, SideEffectExternal:
		return true
	}
	return false
}

// UnclassifiedBindings returns the IDs of bindings enabled on the workspace
// that lack a DefaultSideEffect — the per-tool overrides alone are not enough
// here, since any tool that isn't listed in overrides would fall through to an
// empty default. Used by:
//   - the autonomy gate (defense-in-depth) to deny calls into unclassified bindings
//   - the mission-enable flow to surface the one-time classification prompt
//   - the run setup path to refuse to start a mission run while bindings are
//     still unclassified
//
// Disabled bindings are ignored; a user who has explicitly turned off a binding
// shouldn't be blocked from running a mission just because that binding was
// never classified.
func UnclassifiedBindings(ws *Workspace) (mcpIDs, skillIDs []string) {
	if ws == nil {
		return nil, nil
	}
	for _, b := range ws.MCPBindings {
		if !b.Enabled {
			continue
		}
		if defaultSE, _ := EffectiveMCPBindingClassification(b); !isValidSideEffect(defaultSE) {
			mcpIDs = append(mcpIDs, b.ID)
		}
	}
	for _, b := range ws.SkillBindings {
		if !b.Enabled {
			continue
		}
		if !isValidSideEffect(b.DefaultSideEffect) {
			skillIDs = append(skillIDs, b.ID)
		}
	}
	return mcpIDs, skillIDs
}

// MissionBindingsReady reports whether all enabled bindings on a workspace
// have a valid DefaultSideEffect. Mission runs must not start when this is
// false; callers should surface the "classify your bindings" prompt instead.
func MissionBindingsReady(ws *Workspace) bool {
	mcp, sk := UnclassifiedBindings(ws)
	return len(mcp) == 0 && len(sk) == 0
}

// Workspace memory tool names. Defined here rather than in chathttp so the
// autonomy gate, which works by tool name, has a single source of truth.
const (
	MemoryWriteToolName  = "memory_write"
	MemoryForgetToolName = "memory_forget"
	// MemoryProposeGlobalToolName sends a fact about the user to the Personal
	// HQ review queue. It writes no canonical memory anywhere.
	MemoryProposeGlobalToolName = "memory_propose_global"
)

// nativeToolSideEffects classifies the tools Ori itself offers in a workspace:
// chathttp's workspace tools, the utility tools and the Home library tools.
// They belong to no MCP or skill binding, so a binding's default must never
// classify them (it used to: a read-default Calendar binding made
// workspace_save_note and delegate_task "reads"). The autonomy gate consults
// this table before any binding (workspace-goal-suggestions FR10).
//
//   - read: observes what the workspace owns; allowed under Watch.
//   - write: changes something inside this workspace; allowed under Propose.
//   - external: reaches beyond the workspace or starts work (delegation,
//     project scaffolding, the user profile, mail, the web); never allowed
//     in a goal run.
//
// chathttp's TestNativeWorkspaceToolsAreClassified fails when a tool it
// defines is missing here.
var nativeToolSideEffects = map[string]SideEffect{
	"workspace_notes":                     SideEffectRead,
	"workspace_tasks":                     SideEffectRead,
	"workspace_sessions":                  SideEffectRead,
	"workspace_session_detail":            SideEffectRead,
	"workspace_files":                     SideEffectRead,
	"workspace_directories":               SideEffectRead,
	"workspace_directory_list":            SideEffectRead,
	"workspace_directory_read":            SideEffectRead,
	"workspace_project_templates":         SideEffectRead,
	"current_task":                        SideEffectRead,
	"task_runs":                           SideEffectRead,
	"hq_overview":                         SideEffectRead,
	"home_library_search":                 SideEffectRead,
	"home_library_detail":                 SideEffectRead,
	"home_library_sessions":               SideEffectRead,
	"home_library_handoff_receipts":       SideEffectRead,
	"home_profile_read":                   SideEffectRead,
	"time":                                SideEffectRead,
	"workspace_save_note":                 SideEffectWrite,
	"github_propose_change":               SideEffectWrite,
	MemoryWriteToolName:                   SideEffectWrite,
	MemoryForgetToolName:                  SideEffectWrite,
	MemoryProposeGlobalToolName:           SideEffectWrite,
	"delegate_task":                       SideEffectExternal,
	"workspace_create_project":            SideEffectExternal,
	"workspace_manage_agents":             SideEffectExternal,
	"workspace_manage_mcp":                SideEffectExternal,
	"workspace_manage_skills":             SideEffectExternal,
	"profile_set":                         SideEffectExternal,
	"mail_search_threads":                 SideEffectExternal,
	"mail_get_thread":                     SideEffectExternal,
	"mail_draft_reply":                    SideEffectExternal,
	"home_library_save_brief":             SideEffectExternal,
	"home_library_propose_next_action":    SideEffectExternal,
	"home_library_propose_session_recap":  SideEffectExternal,
	"home_library_propose_root_review":    SideEffectExternal,
	"home_library_propose_session_goal":   SideEffectExternal,
	"home_library_propose_project_review": SideEffectExternal,
	"weather":                             SideEffectExternal,
	"air_quality":                         SideEffectExternal,
	"web_search":                          SideEffectExternal,
	"web_fetch":                           SideEffectExternal,
	"browser":                             SideEffectExternal,
}

// NativeToolSideEffect returns the fixed classification of one of Ori's own
// workspace tools, and false for any other tool.
func NativeToolSideEffect(toolName string) (SideEffect, bool) {
	se, ok := nativeToolSideEffects[toolName]
	return se, ok
}

// WorkspaceFilesBindingAlias is the alias of the filesystem MCP binding Ori
// creates over every workspace's own folder.
const WorkspaceFilesBindingAlias = "workspace-files"

// workspaceFilesReadTools are the filesystem MCP server's read-only tools.
var workspaceFilesReadTools = []string{
	"read_file", "read_text_file", "read_media_file", "read_multiple_files",
	"list_directory", "list_directory_with_sizes", "directory_tree",
	"search_files", "get_file_info", "list_allowed_directories",
}

// isBuiltinWorkspaceFilesBinding reports whether b is the filesystem binding
// Ori created over the workspace's own folder.
func isBuiltinWorkspaceFilesBinding(b MCPBinding) bool {
	return b.Alias == WorkspaceFilesBindingAlias && b.ServerName == "filesystem"
}

// EffectiveMCPBindingClassification returns the classification the autonomy
// gate applies to b. The user's own default and per-tool overrides always win.
// Ori's workspace-files binding, which reaches only the workspace's own
// folder, carries a built-in classification when the user set none: its read
// tools are reads, everything else writes inside the workspace. Without it
// every new workspace would refuse goal runs until someone classified a
// binding they never created.
func EffectiveMCPBindingClassification(b MCPBinding) (SideEffect, map[string]SideEffect) {
	if !isBuiltinWorkspaceFilesBinding(b) {
		return b.DefaultSideEffect, b.ToolOverrides
	}
	defaultSE := b.DefaultSideEffect
	if !isValidSideEffect(defaultSE) {
		defaultSE = SideEffectWrite
	}
	overrides := make(map[string]SideEffect, len(workspaceFilesReadTools)+len(b.ToolOverrides))
	for _, name := range workspaceFilesReadTools {
		overrides[name] = SideEffectRead
	}
	for name, se := range b.ToolOverrides {
		overrides[name] = se
	}
	return defaultSE, overrides
}

// EffectiveAutonomyPolicy is the policy a goal run applies: Propose only when
// the user chose it, Watch otherwise — including a workspace that never set
// one, which is how an automatic first run starts.
func EffectiveAutonomyPolicy(p AutonomyPolicy) AutonomyPolicy {
	if p == AutonomyPropose {
		return AutonomyPropose
	}
	return AutonomyWatch
}

// IsWorkspaceMemoryTool reports whether toolName is a native workspace-memory
// tool. Memory tools are workspace-internal writes permitted under every
// autonomy policy, including Watch: memory is the agent's own operational
// state, and a Watch mission that cannot remember what it watched can never
// deduplicate findings or track trends across runs. The user-facing Watch
// guarantee is correspondingly worded "no writes anywhere — except workspace
// memory". memory_propose_global is included: it only queues an inert
// candidate for the user's review and writes no canonical memory.
func IsWorkspaceMemoryTool(toolName string) bool {
	return toolName == MemoryWriteToolName || toolName == MemoryForgetToolName ||
		toolName == MemoryProposeGlobalToolName
}

// IsAllowedUnderPolicy reports whether a tool call classified with `se` is
// permitted under the given autonomy policy. The empty SideEffect (unclassified)
// is always denied. Watch allows only read; Propose allows read and write.
// Higher policies (Act-with-approval, Autopilot) land in v1.5+.
// Workspace memory tools are exempted upstream (see IsWorkspaceMemoryTool and
// the EvaluateMissionToolCall* entry points) — this function stays
// classification-pure.
func IsAllowedUnderPolicy(policy AutonomyPolicy, se SideEffect) bool {
	if se == "" {
		return false
	}
	switch policy {
	case AutonomyWatch:
		return se == SideEffectRead
	case AutonomyPropose:
		return se == SideEffectRead || se == SideEffectWrite
	}
	return false
}
