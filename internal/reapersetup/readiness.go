// Package reapersetup computes one normalized, truthful readiness model for
// REAPER workspaces, shared by workspace-creation feedback, the workspace UI,
// repair, and the setup-task auto-start decision. It exists so the frontend does
// not independently guess readiness from several unrelated endpoints.
//
// A central rule: Ori-ready is not REAPER-connected. This resolver can verify
// plugin components, agent provider compatibility, and explicit native-CLI
// permissions. It must never report that REAPER, Web Remote, the open project,
// or the runner is live — that is checked by the setup agent at execution time,
// and is always reported here as not-yet-checked (LiveVerification).
package reapersetup

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/pluginworkspace"
	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ReaperPluginName is the plugin whose components back live REAPER control.
const ReaperPluginName = "reaper-plugin"

// ReaperSongTemplateID is the built-in template id for REAPER song workspaces.
const ReaperSongTemplateID = "reaper-song"

// Task context markers written by the template seed / setup-start endpoint. These
// live here so readiness and the setup-start endpoint agree on the same keys.
const (
	// TaskContextTemplateSetup marks a task as the template's first-open setup task.
	TaskContextTemplateSetup = "template_setup"
	// TaskContextSetupConsumedAt is stamped once the setup task's auto-start is
	// consumed; its presence means auto-start must not run again.
	TaskContextSetupConsumedAt = "template_setup_autostart_consumed_at"
)

// Status is the normalized readiness status code. Exactly one is returned per
// resolution, chosen by deterministic precedence (see Resolve).
type Status string

const (
	// StatusFileOnly: identified REAPER workspace that is usable as a file
	// project but is not pursuing live control (no pending setup task to run and
	// not fully Ori-ready). It is a supported mode, never an error.
	StatusFileOnly Status = "file_only"
	// StatusPluginMissing: reaper-plugin is not installed globally.
	StatusPluginMissing Status = "plugin_missing"
	// StatusPluginDisabled: reaper-plugin is installed but its global record is disabled.
	StatusPluginDisabled Status = "plugin_disabled"
	// StatusPluginDetached: installed and enabled, but recorded components are not all attached.
	StatusPluginDetached Status = "plugin_detached"
	// StatusCLIAgentRequired: the effective setup agent does not use Codex or Claude Code.
	StatusCLIAgentRequired Status = "cli_agent_required"
	// StatusNativeCLIAccessRequired: compatible agent, but workspace and/or agent native CLI access is off.
	StatusNativeCLIAccessRequired Status = "native_cli_access_required"
	// StatusOriReady: plugin components, a compatible agent, and both explicit
	// native-CLI permissions are ready for the setup agent to check REAPER.
	StatusOriReady Status = "ori_ready"
)

// Readiness is the normalized result. Every field is safe to expose to the UI.
type Readiness struct {
	// Identified reports whether this is a REAPER workspace at all. When false,
	// the UI must render no REAPER readiness surface.
	Identified   bool   `json:"identified"`
	IdentifiedBy string `json:"identified_by,omitempty"` // provenance | setup_task | project_entry | reaper_evidence

	Status      Status `json:"status"`
	ProjectMode string `json:"project_mode"` // "file_only" until "ori_ready"
	Explanation string `json:"explanation"`
	NextAction  string `json:"next_action,omitempty"`

	// Plugin state (from the shared reconciler's read-only inspection).
	PluginInstalled    bool                        `json:"plugin_installed"`
	PluginEnabled      bool                        `json:"plugin_enabled"`
	PluginAttached     bool                        `json:"plugin_attached"`
	MissingComponents  []pluginworkspace.Component `json:"missing_components,omitempty"`
	AttachedComponents []pluginworkspace.Component `json:"attached_components,omitempty"`

	// Effective setup agent.
	SetupAgent         string `json:"setup_agent,omitempty"`
	SetupAgentProvider string `json:"setup_agent_provider,omitempty"`
	SetupAgentIsCLI    bool   `json:"setup_agent_is_cli"`

	// Native CLI access gates (both must be on for live control).
	WorkspaceNativeCLIEnabled bool `json:"workspace_native_cli_enabled"`
	AgentNativeCLIEnabled     bool `json:"agent_native_cli_enabled"`

	// Setup task.
	HasPendingSetupTask bool   `json:"has_pending_setup_task"`
	SetupTaskID         string `json:"setup_task_id,omitempty"`

	// LiveVerification is always "not_checked" here: readiness never proves
	// REAPER/Web Remote/runner are live. Only the setup agent verifies that at
	// execution time.
	LiveVerification string `json:"live_verification"`

	// Runtime is the generalized durable/live status when this workspace has a
	// persisted reaper_live_control contract. Legacy callers can keep reading
	// the fields above while newer callers migrate to this authoritative model.
	Runtime *runtimecapability.Status `json:"runtime,omitempty"`
}

// workspaceReader is the read surface the resolver needs (satisfied by the
// workspace SyncStore).
type workspaceReader interface {
	Get(id string) (*workspace.Workspace, error)
	GetWorkspaceAgent(workspaceID, agentName string) (*agent.Agent, bool, error)
}

// PluginInspector reports plugin attachment without mutating anything
// (satisfied by *pluginworkspace.Reconciler).
type PluginInspector interface {
	Inspect(workspaceID string, plugins []string) ([]pluginworkspace.PluginResult, error)
}

// Resolver computes readiness for a workspace.
type Resolver struct {
	store   workspaceReader
	plugins PluginInspector
}

// NewResolver builds a readiness resolver over the workspace store and the shared
// plugin reconciler.
func NewResolver(store workspaceReader, plugins PluginInspector) *Resolver {
	return &Resolver{store: store, plugins: plugins}
}

// Resolve computes the normalized readiness for the workspace. A fresh Resolve
// reflects the current plugin install/enable/attachment, binding, task, provider,
// and permission state — it never caches a stale ready result.
func (r *Resolver) Resolve(workspaceID string) (Readiness, error) {
	out := Readiness{LiveVerification: "not_checked", ProjectMode: "file_only"}
	if r.store == nil {
		return out, nil
	}
	ws, err := r.store.Get(workspaceID)
	if err != nil {
		return out, err
	}
	if ws == nil {
		return out, nil
	}

	out.Identified, out.IdentifiedBy = identify(ws)
	if !out.Identified {
		return out, nil
	}

	// Plugin state via the shared read-only inspection.
	if r.plugins != nil {
		results, ierr := r.plugins.Inspect(workspaceID, []string{ReaperPluginName})
		if ierr != nil {
			return out, ierr
		}
		if len(results) > 0 {
			pr := results[0]
			out.PluginInstalled = pr.Installed
			out.PluginEnabled = pr.Enabled
			out.PluginAttached = pr.Installed && pr.Enabled && len(pr.Missing) == 0
			out.MissingComponents = pr.Missing
			out.AttachedComponents = pr.Attached
		}
	}

	// Effective setup agent + its provider and native-access opt-in.
	setupTaskID, hasPending := pendingSetupTask(ws)
	out.SetupTaskID = setupTaskID
	out.HasPendingSetupTask = hasPending

	out.SetupAgent = effectiveSetupAgent(ws)
	out.WorkspaceNativeCLIEnabled = ws.AllowNativeMCPCLI
	if out.SetupAgent != "" {
		if ag, ok, aerr := r.store.GetWorkspaceAgent(workspaceID, out.SetupAgent); aerr == nil && ok && ag != nil {
			out.SetupAgentProvider = ag.Settings.Provider
			out.SetupAgentIsCLI = isCLIProvider(ag.Settings.Provider)
			out.AgentNativeCLIEnabled = ag.Settings.IsNativeMCPToolsAllowed()
		}
	}

	out.Status = classify(out)
	out.ProjectMode = projectMode(out.Status)
	out.Explanation, out.NextAction = describe(out)
	return out, nil
}

// classify chooses the status by deterministic precedence: the most specific
// fixable blocker wins; ori_ready when nothing blocks; file_only when the
// workspace is usable but not pursuing live control (nothing pending, not ready).
func classify(r Readiness) Status {
	ready := r.PluginInstalled && r.PluginEnabled && r.PluginAttached &&
		r.SetupAgentIsCLI && r.WorkspaceNativeCLIEnabled && r.AgentNativeCLIEnabled
	if ready {
		return StatusOriReady
	}
	if !r.HasPendingSetupTask {
		return StatusFileOnly
	}
	switch {
	case !r.PluginInstalled:
		return StatusPluginMissing
	case !r.PluginEnabled:
		return StatusPluginDisabled
	case !r.PluginAttached:
		return StatusPluginDetached
	case !r.SetupAgentIsCLI:
		return StatusCLIAgentRequired
	default: // compatible agent, but a native-access gate is off
		return StatusNativeCLIAccessRequired
	}
}

func projectMode(s Status) string {
	if s == StatusOriReady {
		return "ori_ready"
	}
	return "file_only"
}

// describe returns calm, outcome-oriented copy and a recommended next action.
// It never equates ori_ready with a verified REAPER connection.
func describe(r Readiness) (explanation, action string) {
	switch r.Status {
	case StatusOriReady:
		return "Ori is configured to check REAPER. Live REAPER, Web Remote, and runner readiness are verified when setup runs.", ""
	case StatusFileOnly:
		return "This is a usable file-only REAPER project. Live control prerequisites are not configured.", "Repair REAPER setup to enable live control."
	case StatusPluginMissing:
		return "File-only project: the reaper-plugin is not installed. The project and its .rpp file are intact.", "Install reaper-plugin."
	case StatusPluginDisabled:
		return "reaper-plugin is installed but globally disabled.", "Enable reaper-plugin, then attach it to this workspace."
	case StatusPluginDetached:
		return "reaper-plugin is installed but some components are not attached to this workspace.", "Repair REAPER setup to attach the missing components."
	case StatusCLIAgentRequired:
		return "Live local REAPER control requires a Codex or Claude Code agent. File-only use remains available.", "Assign a Codex or Claude Code agent as the setup agent."
	case StatusNativeCLIAccessRequired:
		return "A compatible agent is set, but native CLI access is off. Native CLI tools run outside Ori's per-call confirmation gate.", "Enable native CLI access for the workspace and the setup agent."
	default:
		return "", ""
	}
}

// identify determines whether the workspace is a REAPER workspace, conservatively.
// Order: reaper-song template provenance, then Reaper Song setup-task provenance,
// then a persisted .rpp project entry, then a reaper tag combined with .rpp
// evidence. A workspace name or reaper tag alone is never sufficient.
func identify(ws *workspace.Workspace) (bool, string) {
	if ws.IsFromTemplate(ReaperSongTemplateID) {
		return true, "provenance"
	}
	for _, t := range ws.Tasks {
		if t.TemplateRef != nil && strings.EqualFold(strings.TrimSpace(t.TemplateRef.TemplateID), ReaperSongTemplateID) {
			return true, "setup_task"
		}
	}
	if hasReaperProjectEntry(ws) {
		return true, "project_entry"
	}
	if hasTag(ws, "reaper") && hasReaperProjectEntry(ws) {
		return true, "reaper_evidence"
	}
	return false, ""
}

// hasReaperProjectEntry reports whether the workspace has a persisted .rpp
// project entry (the authoritative REAPER project file).
func hasReaperProjectEntry(ws *workspace.Workspace) bool {
	if entry, err := workspace.GetProjectEntryPath(ws.SharedData); err == nil && strings.HasSuffix(strings.ToLower(strings.TrimSpace(entry)), ".rpp") {
		return true
	}
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(ws.ProjectPath)), ".rpp")
}

func hasTag(ws *workspace.Workspace, tag string) bool {
	for _, t := range ws.Tags {
		if strings.EqualFold(strings.TrimSpace(t), tag) {
			return true
		}
	}
	return false
}

// pendingSetupTask returns the id of an assigned-or-pending, unconsumed template
// setup task and whether one exists. Consumed, running, completed, cancelled, or
// absent setup tasks do not count as pending.
func pendingSetupTask(ws *workspace.Workspace) (string, bool) {
	for i := range ws.Tasks {
		t := &ws.Tasks[i]
		if t.Context == nil || t.Context[TaskContextTemplateSetup] != true {
			continue
		}
		if consumed, ok := t.Context[TaskContextSetupConsumedAt]; ok {
			if s, _ := consumed.(string); strings.TrimSpace(s) != "" {
				continue
			}
		}
		if t.Status != workspace.TaskStatusPending && t.Status != workspace.TaskStatusAssigned {
			continue
		}
		return t.ID, true
	}
	return "", false
}

// effectiveSetupAgent resolves the pending setup task's assignee, falling back to
// the workspace entry agent when the task uses the entry-agent default (no
// explicit assignee).
func effectiveSetupAgent(ws *workspace.Workspace) string {
	for i := range ws.Tasks {
		t := &ws.Tasks[i]
		if t.Context == nil || t.Context[TaskContextTemplateSetup] != true {
			continue
		}
		if to := strings.TrimSpace(t.To); to != "" {
			return to
		}
		break
	}
	return strings.TrimSpace(ws.EntryAgentName())
}

// isCLIProvider reports whether provider is a native-CLI provider (Codex / Claude
// Code) capable of live local REAPER control.
func isCLIProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex", "claude_code":
		return true
	default:
		return false
	}
}

// PluginLister is the read surface the pre-create preview needs (satisfied by
// *plugin.Manager).
type PluginLister interface {
	List() ([]plugin.InstalledPlugin, error)
}

// CreatePreview describes what a Reaper Song workspace's plugin attachment will
// look like at creation time, computed from the same plugin store the live
// readiness resolver reads. It intentionally omits agent/native-access state:
// those depend on the selected Template Agents plan and are decided in-workspace
// after creation. Like readiness, it never claims REAPER is live.
type CreatePreview struct {
	PluginInstalled  bool                        `json:"plugin_installed"`
	PluginEnabled    bool                        `json:"plugin_enabled"`
	WouldAttach      []pluginworkspace.Component `json:"would_attach,omitempty"`
	Status           string                      `json:"status"` // plugin_missing | plugin_disabled | ready_to_attach
	Explanation      string                      `json:"explanation"`
	NextAction       string                      `json:"next_action,omitempty"`
	LiveVerification string                      `json:"live_verification"`
}

// PreviewCreate reports the pre-create plugin attachment state for a Reaper Song
// workspace. Used by the Create Workspace REAPER Setup card so it does not
// independently infer plugin state.
func PreviewCreate(plugins PluginLister) (CreatePreview, error) {
	out := CreatePreview{Status: "plugin_missing", LiveVerification: "not_checked"}
	if plugins == nil {
		out.Explanation = "File-only project: the reaper-plugin is not installed."
		out.NextAction = "Install reaper-plugin."
		return out, nil
	}
	list, err := plugins.List()
	if err != nil {
		return out, err
	}
	for _, p := range list {
		if !strings.EqualFold(strings.TrimSpace(p.Name), ReaperPluginName) {
			continue
		}
		out.PluginInstalled = true
		out.PluginEnabled = p.Enabled
		for _, s := range p.Skills {
			if s = strings.TrimSpace(s); s != "" {
				out.WouldAttach = append(out.WouldAttach, pluginworkspace.Component{Kind: pluginworkspace.ComponentSkill, Name: s})
			}
		}
		for _, s := range p.MCPServers {
			if s = strings.TrimSpace(s); s != "" {
				out.WouldAttach = append(out.WouldAttach, pluginworkspace.Component{Kind: pluginworkspace.ComponentMCP, Name: s})
			}
		}
		break
	}
	switch {
	case !out.PluginInstalled:
		out.Status = "plugin_missing"
		out.Explanation = "File-only project: the reaper-plugin is not installed. Creation still succeeds; the project and its .rpp file are intact."
		out.NextAction = "Install reaper-plugin."
	case !out.PluginEnabled:
		out.Status = "plugin_disabled"
		out.Explanation = "reaper-plugin is installed but globally disabled. Enable it to attach its components on creation."
		out.NextAction = "Enable reaper-plugin."
	default:
		out.Status = "ready_to_attach"
		out.Explanation = "reaper-plugin components will be attached to this workspace when it is created. REAPER, Web Remote, and runner readiness are checked later, when setup runs."
	}
	return out, nil
}

// String renders a readiness for logs.
func (r Readiness) String() string {
	return fmt.Sprintf("reaper readiness: identified=%v status=%s mode=%s", r.Identified, r.Status, r.ProjectMode)
}
