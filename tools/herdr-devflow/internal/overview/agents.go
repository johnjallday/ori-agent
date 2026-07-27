package overview

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/planning"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// BridgeReader loads the bridge's saved records. Saved identity is a record of
// what the bridge once bound, never evidence of what is running now.
type BridgeReader interface {
	Load() (model.BridgeState, error)
}

// AgentCollector reports what Herdr can currently see. It reads structured API
// results only: no terminal text, no prompt content, no screen scraping.
type AgentCollector interface {
	AgentListInfo(ctx context.Context) ([]herdr.AgentInfo, error)
	// WorkspaceListInfo supplies each workspace's worktree binding, used as a
	// fallback when a pane reports no usable working directory.
	WorkspaceListInfo(ctx context.Context) ([]herdr.WorkspaceInfo, error)
}

// AgentEvidence is one collection of live and saved agent facts.
type AgentEvidence struct {
	// Availability is the outcome of consulting Herdr.
	Availability Availability
	// Detail explains an unavailable result in operator-safe language.
	Detail string
	// Live are the panes Herdr currently reports.
	Live []herdr.AgentInfo
	// Workspaces are the open workspaces and their worktree bindings.
	Workspaces []herdr.WorkspaceInfo
	// Bridge is the saved bridge state.
	Bridge model.BridgeState
	// ObservedAt is when Herdr was consulted.
	ObservedAt time.Time
}

// CollectAgents gathers live and saved agent facts. A Herdr outage degrades to
// an unavailable result rather than an error: the rest of the board is still
// worth rendering.
func CollectAgents(ctx context.Context, agents AgentCollector, bridge BridgeReader, now time.Time) AgentEvidence {
	evidence := AgentEvidence{Availability: AvailabilityAvailable, ObservedAt: now}
	if bridge != nil {
		if state, err := bridge.Load(); err == nil {
			evidence.Bridge = state
		}
	}
	if agents == nil {
		evidence.Availability = AvailabilityUnavailable
		evidence.Detail = "Herdr was not consulted"
		return evidence
	}
	live, err := agents.AgentListInfo(ctx)
	if err != nil {
		evidence.Availability = AvailabilityUnavailable
		evidence.Detail = "Herdr is unavailable, so no live agent status could be read"
		return evidence
	}
	evidence.Live = live
	// The workspace listing is a fallback for panes with no usable cwd. Its
	// failure degrades that fallback only; live agent status is still good.
	if workspaces, err := agents.WorkspaceListInfo(ctx); err == nil {
		evidence.Workspaces = workspaces
	}
	return evidence
}

// BridgeSlugs returns the exact feature slugs the bridge holds records for.
func BridgeSlugs(state model.BridgeState) []string {
	slugs := make([]string, 0, len(state.Features))
	for _, feature := range state.Features {
		if planning.ValidSlug(feature.Feature.Name) {
			slugs = append(slugs, feature.Feature.Name)
		}
	}
	sort.Strings(slugs)
	return slugs
}

// AttachAgents builds the agent rows for one feature and returns its findings.
//
// It never adopts, rebinds, starts, or repairs anything. A saved record that no
// longer resolves is reported as drift; a live agent nobody claimed is reported
// as unmanaged. Both stay visible precisely because neither is safe to fix
// automatically.
func AttachAgents(feature *Feature, evidence AgentEvidence) []Finding {
	state, hasRecord := findFeatureState(evidence.Bridge, feature.Slug)
	unavailable := evidence.Availability != AvailabilityAvailable

	var findings []Finding
	raise := func(code FindingCode, severity Severity, role, message, detail string) {
		findings = append(findings, Finding{
			Code: code, Severity: severity, Feature: feature.Slug, Role: role,
			Source: SourceHerdr, Message: message, Detail: detail,
		})
	}

	claimed := map[string]struct{}{}
	roles := sortedRoles(state)
	for _, role := range roles {
		saved := state.Agents[role]
		row := Agent{
			Feature: feature.Slug,
			Role:    identityField(saved.Role),
			Managed: true,
			Kind:    identityField(saved.Kind),
			Saved:   savedIdentity(saved),
			SavedAt: saved.UpdatedAt,
			// A saved status is a record from the last bridge write, never a
			// live observation, so it is not copied into Status.
			Status:             AgentUnknown,
			StatusAvailability: AvailabilityUnavailable,
			Binding:            BindingUnavailable,
		}

		if unavailable {
			// Saved values stay visible, clearly labelled as bridge records.
			row.BindingDetail = evidence.Detail
			feature.Agents = append(feature.Agents, row)
			continue
		}

		match := classifyBinding(saved, evidence.Live)
		row.Binding = match.health
		row.BindingDetail = match.detail
		row.BindingCandidates = match.candidates
		if match.live != nil {
			row.Live = liveIdentity(*match.live)
			row.Status = AgentStatus(normalizeStatus(match.live.AgentStatus))
			row.StatusAvailability = AvailabilityAvailable
			claimed[match.live.PaneID] = struct{}{}
		}

		switch match.health {
		case BindingMissing:
			row.Status = AgentMissing
			row.StatusAvailability = AvailabilityAvailable
			raise(FindingAgentMissing, SeverityWarning, saved.Role,
				"The bridge has a saved agent for this role, but Herdr reports no matching agent.",
				match.detail)
		case BindingAmbiguous:
			raise(FindingAgentAmbiguous, SeverityError, saved.Role,
				"More than one live agent plausibly matches this saved role; none was chosen.",
				match.detail)
		case BindingPossibleDrift:
			raise(FindingAgentDrift, SeverityWarning, saved.Role,
				"A live agent matches this role only partially; the saved identity may be stale.",
				match.detail)
		}
		feature.Agents = append(feature.Agents, row)
	}

	if !unavailable {
		unmanaged := discoverUnmanaged(feature, evidence, claimed)
		feature.Agents = append(feature.Agents, unmanaged...)
		for _, row := range unmanaged {
			raise(FindingAgentUnmanaged, SeverityInfo, "",
				"A live agent is running in this feature's worktree with no bridge role.",
				"Agent "+row.Live.Session+" in workspace "+row.Live.Workspace+".")
		}
	}

	// A feature worktree with no agent at all is worth stating: silence would
	// look identical to a healthy, quietly-working agent.
	if feature.Git.WorktreePath != "" && len(feature.Agents) == 0 && !unavailable {
		detail := "no bridge record"
		if hasRecord {
			detail = "a bridge record exists but declares no roles"
		}
		raise(FindingNoAgent, SeverityInfo, "",
			"This feature has a worktree but no agent is running for it.", detail)
	}

	// A saved record pointing at a worktree that no longer exists is drift to
	// report, never an error and never a blocker on its own.
	if hasRecord && state.Feature.Path != "" && feature.Git.WorktreePath == "" {
		raise(FindingBindingPathStale, SeverityInfo, "",
			"The bridge has a saved binding for a worktree that no longer exists.",
			"Recorded path "+identityField(state.Feature.Path)+".")
	}

	if finding, ok := metadataStaleness(feature, state, hasRecord); ok {
		findings = append(findings, finding)
	}

	feature.Schedules = append(feature.Schedules, featureSchedules(state)...)
	for _, schedule := range feature.Schedules {
		if schedule.State == string(model.ScheduleFailed) {
			raise(FindingScheduleFailed, SeverityWarning, "",
				"A scheduled action for this feature failed.", schedule.Summary)
		}
	}
	return findings
}

// metadataStaleness compares the display metadata the bridge last published
// against the authoritative plan.
//
// Herdr exposes no read-back for source-scoped metadata, so staleness is
// established by timestamps: if the task list changed after the bridge last
// wrote, the board is showing older progress than the plan now reports. The
// finding is informational and never authorizes a write — refreshing metadata
// is a separate, explicitly requested action, and display metadata is never
// identity or semantic-status authority.
func metadataStaleness(feature *Feature, state model.FeatureState, hasRecord bool) (Finding, bool) {
	if !hasRecord || state.UpdatedAt.IsZero() {
		return Finding{}, false
	}
	if state.MetadataEnabled != nil && !*state.MetadataEnabled {
		return Finding{}, false
	}
	changed := feature.Plan.TaskListModTime
	if changed.IsZero() || !changed.After(state.UpdatedAt) {
		return Finding{}, false
	}
	return Finding{
		Code:     FindingMetadataStale,
		Severity: SeverityInfo,
		Feature:  feature.Slug,
		Source:   SourceHerdr,
		Message:  "The Herdr board's displayed progress is older than this feature's task list.",
		Detail: "task list changed " + changed.UTC().Format(time.RFC3339) +
			"; metadata last published " + state.UpdatedAt.UTC().Format(time.RFC3339),
	}, true
}

// bindingMatch is one classification outcome.
type bindingMatch struct {
	health     BindingHealth
	live       *herdr.AgentInfo
	detail     string
	candidates []Identity
}

// classifyBinding resolves a saved role to a live agent in strength order:
// a unique native session, then a complete saved identity, then a unique
// workspace/pane/kind candidate. It never modifies the saved identity — a
// diagnostic that silently rewrites what it is diagnosing is worthless.
func classifyBinding(saved model.RoleAgent, live []herdr.AgentInfo) bindingMatch {
	// 1. Native session is the strongest identity Herdr exposes: it survives
	// pane renames, splits, and terminal restarts.
	if saved.NativeSession.Value != "" {
		var matches []herdr.AgentInfo
		for _, candidate := range live {
			if candidate.AgentSession != nil && sameNativeSession(saved.NativeSession, *candidate.AgentSession) {
				matches = append(matches, candidate)
			}
		}
		switch len(matches) {
		case 1:
			if matches[0].WorkspaceID == saved.WorkspaceID {
				return bindingMatch{health: BindingExact, live: &matches[0]}
			}
			return bindingMatch{
				health: BindingPossibleDrift, live: &matches[0],
				detail: "native session matches, but workspace differs: saved " +
					identityField(saved.WorkspaceID) + ", live " + identityField(matches[0].WorkspaceID),
			}
		case 0:
			// Fall through to weaker evidence.
		default:
			return bindingMatch{health: BindingAmbiguous, candidates: identities(matches),
				detail: "several live agents report the same native session"}
		}
	}

	// 2. A complete saved identity: every field agrees.
	for index, candidate := range live {
		if candidate.Name == saved.Name && candidate.WorkspaceID == saved.WorkspaceID &&
			candidate.PaneID == saved.PaneID && candidate.TerminalID == saved.TerminalID {
			return bindingMatch{health: BindingExact, live: &live[index]}
		}
	}

	// 3. A unique plausible candidate in the same workspace. This is where
	// real drift shows up: a pane was renamed or replaced, so some fields
	// match and others do not.
	var plausible []int
	for index, candidate := range live {
		if candidate.WorkspaceID != saved.WorkspaceID {
			continue
		}
		if candidate.PaneID == saved.PaneID || candidate.TerminalID == saved.TerminalID ||
			candidate.Name == saved.Name || strings.EqualFold(candidate.Agent, saved.Kind) {
			plausible = append(plausible, index)
		}
	}
	switch len(plausible) {
	case 1:
		candidate := live[plausible[0]]
		return bindingMatch{
			health: BindingPossibleDrift, live: &live[plausible[0]],
			detail: explainDrift(saved, candidate),
		}
	case 0:
		return bindingMatch{health: BindingMissing,
			detail: "no live agent in workspace " + identityField(saved.WorkspaceID) +
				" matches saved pane " + identityField(saved.PaneID)}
	default:
		var candidates []herdr.AgentInfo
		for _, index := range plausible {
			candidates = append(candidates, live[index])
		}
		return bindingMatch{health: BindingAmbiguous, candidates: identities(candidates),
			detail: "several live agents in workspace " + identityField(saved.WorkspaceID) + " plausibly match this role"}
	}
}

// explainDrift states field by field how a saved identity and a live agent
// disagree, so an operator can decide whether to rebind rather than guess.
func explainDrift(saved model.RoleAgent, live herdr.AgentInfo) string {
	var differences []string
	compare := func(field, savedValue, liveValue string) {
		if savedValue != liveValue {
			differences = append(differences, field+": saved "+quoteOrNone(savedValue)+", live "+quoteOrNone(liveValue))
		}
	}
	compare("name", identityField(saved.Name), identityField(live.Name))
	compare("pane", identityField(saved.PaneID), identityField(live.PaneID))
	compare("terminal", identityField(saved.TerminalID), identityField(live.TerminalID))
	compare("kind", identityField(saved.Kind), identityField(live.Agent))
	if saved.NativeSession.Value != "" {
		liveSession := ""
		if live.AgentSession != nil {
			liveSession = live.AgentSession.Value
		}
		compare("session", identityField(saved.NativeSession.Value), identityField(liveSession))
	}
	if len(differences) == 0 {
		return "the saved and live identities agree on every compared field"
	}
	return strings.Join(differences, "; ")
}

func quoteOrNone(value string) string {
	if value == "" {
		return "(none)"
	}
	return `"` + value + `"`
}

// agentWorktree reports the canonical worktree a live agent is working in.
//
// Pane cwd is the primary evidence because it describes where work is actually
// happening. The workspace's recorded binding is only a fallback: a workspace
// created by hand and then navigated into carries no binding at all, which is
// exactly how a working agent became invisible to the bridge.
func agentWorktree(agent herdr.AgentInfo, workspaces []herdr.WorkspaceInfo) string {
	if agent.Cwd != "" {
		return agent.Cwd
	}
	if agent.ForegroundCwd != "" {
		return agent.ForegroundCwd
	}
	for _, workspace := range workspaces {
		if workspace.WorkspaceID != agent.WorkspaceID {
			continue
		}
		if workspace.Worktree != nil && workspace.Worktree.CheckoutPath != "" {
			return workspace.Worktree.CheckoutPath
		}
		return workspace.Cwd
	}
	return ""
}

// agentsInWorktree returns every live agent whose working directory resolves
// inside the feature's worktree.
//
// Matching is by path, never by workspace label: labels are user-editable and
// observed to drift — two workspaces have been seen sharing one label while
// pointing at different checkouts.
func agentsInWorktree(worktreePath string, evidence AgentEvidence) []herdr.AgentInfo {
	if worktreePath == "" {
		return nil
	}
	var matched []herdr.AgentInfo
	for _, agent := range evidence.Live {
		if worktree.Contains(worktreePath, agentWorktree(agent, evidence.Workspaces)) {
			matched = append(matched, agent)
		}
	}
	return matched
}

// discoverUnmanaged finds live agents in this feature's worktree that no saved
// role claimed. They are surfaced, never adopted: the bridge cannot know
// whether a pane a human opened is meant to be managed.
func discoverUnmanaged(feature *Feature, evidence AgentEvidence, claimed map[string]struct{}) []Agent {
	var rows []Agent
	for _, candidate := range agentsInWorktree(feature.Git.WorktreePath, evidence) {
		if _, taken := claimed[candidate.PaneID]; taken {
			continue
		}
		// A pane with no agent running counts as occupancy, not as an agent.
		if candidate.Agent == "" {
			continue
		}
		rows = append(rows, Agent{
			Feature:            feature.Slug,
			Managed:            false,
			Kind:               identityField(candidate.Agent),
			Live:               liveIdentity(candidate),
			Status:             AgentStatus(normalizeStatus(candidate.AgentStatus)),
			StatusAvailability: AvailabilityAvailable,
			Binding:            BindingMissing,
			BindingDetail:      "this agent has no bridge role for " + identityField(feature.Git.WorktreePath),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Live.Pane < rows[j].Live.Pane })
	return rows
}

// featureSchedules summarizes unresolved, failed, and recently delivered
// one-time schedules. Prompt text is deliberately never included.
func featureSchedules(state model.FeatureState) []Schedule {
	if len(state.Schedules) == 0 {
		return nil
	}
	schedules := make([]Schedule, 0, len(state.Schedules))
	for _, schedule := range state.Schedules {
		schedules = append(schedules, Schedule{
			ID:      identityField(schedule.ID),
			State:   identityField(string(schedule.State)),
			Summary: "scheduled action " + identityField(schedule.ID) + " (" + identityField(string(schedule.State)) + ")",
			DueAt:   schedule.DueAt,
		})
	}
	sort.SliceStable(schedules, func(i, j int) bool { return schedules[i].DueAt.Before(schedules[j].DueAt) })
	return schedules
}

func findFeatureState(state model.BridgeState, slug string) (model.FeatureState, bool) {
	for _, feature := range state.Features {
		if feature.Feature.Name == slug {
			return feature, true
		}
	}
	return model.FeatureState{}, false
}

func sortedRoles(state model.FeatureState) []string {
	roles := make([]string, 0, len(state.Agents))
	for role := range state.Agents {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

// maxIdentityRunes bounds one displayed identity field.
const maxIdentityRunes = 120

// identityField sanitizes a value that reaches a terminal, a JSON payload, or a
// Herdr board cell. Pane IDs and agent names originate in a terminal session,
// so they are untrusted for display exactly like remote branch names are.
func identityField(value string) string {
	return planning.Sanitize(value, maxIdentityRunes)
}

func savedIdentity(saved model.RoleAgent) Identity {
	return Identity{
		Workspace: identityField(saved.WorkspaceID),
		Pane:      identityField(saved.PaneID),
		Terminal:  identityField(saved.TerminalID),
		Session:   identityField(saved.NativeSession.Value),
		Kind:      identityField(saved.Kind),
		Source:    identityField(saved.NativeSession.Source),
	}
}

func liveIdentity(live herdr.AgentInfo) Identity {
	identity := Identity{
		Workspace: identityField(live.WorkspaceID),
		Pane:      identityField(live.PaneID),
		Terminal:  identityField(live.TerminalID),
		Kind:      identityField(live.Agent),
	}
	if live.AgentSession != nil {
		identity.Session = identityField(live.AgentSession.Value)
		identity.Source = identityField(live.AgentSession.Source)
	}
	if identity.Session == "" {
		identity.Session = identityField(live.Name)
	}
	return identity
}

func identities(agents []herdr.AgentInfo) []Identity {
	converted := make([]Identity, 0, len(agents))
	for _, agent := range agents {
		converted = append(converted, liveIdentity(agent))
	}
	return converted
}

func sameNativeSession(saved, live model.NativeSession) bool {
	return saved.Value != "" && saved.Value == live.Value &&
		saved.Kind == live.Kind && saved.Source == live.Source
}

// normalizeStatus maps an empty or unrecognized Herdr status onto the explicit
// unknown value, so a blank never reads as idle.
func normalizeStatus(status model.AgentStatus) model.AgentStatus {
	switch status {
	case model.AgentIdle, model.AgentWorking, model.AgentBlocked, model.AgentDone, model.AgentMissing:
		return status
	default:
		return model.AgentUnknown
	}
}
