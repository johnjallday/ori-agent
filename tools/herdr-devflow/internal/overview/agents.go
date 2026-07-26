package overview

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/planning"
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
}

// AgentEvidence is one collection of live and saved agent facts.
type AgentEvidence struct {
	// Availability is the outcome of consulting Herdr.
	Availability Availability
	// Detail explains an unavailable result in operator-safe language.
	Detail string
	// Live are the panes Herdr currently reports.
	Live []herdr.AgentInfo
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
			Role:    saved.Role,
			Managed: true,
			Kind:    saved.Kind,
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
		unmanaged := discoverUnmanaged(feature, state, evidence.Live, claimed)
		feature.Agents = append(feature.Agents, unmanaged...)
		for _, row := range unmanaged {
			raise(FindingAgentUnmanaged, SeverityInfo, "",
				"A live agent is running in this feature's workspace with no bridge role.",
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
					saved.WorkspaceID + ", live " + matches[0].WorkspaceID,
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
			detail: "no live agent in workspace " + saved.WorkspaceID + " matches saved pane " + saved.PaneID}
	default:
		var candidates []herdr.AgentInfo
		for _, index := range plausible {
			candidates = append(candidates, live[index])
		}
		return bindingMatch{health: BindingAmbiguous, candidates: identities(candidates),
			detail: "several live agents in workspace " + saved.WorkspaceID + " plausibly match this role"}
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
	compare("name", saved.Name, live.Name)
	compare("pane", saved.PaneID, live.PaneID)
	compare("terminal", saved.TerminalID, live.TerminalID)
	compare("kind", saved.Kind, live.Agent)
	if saved.NativeSession.Value != "" {
		liveSession := ""
		if live.AgentSession != nil {
			liveSession = live.AgentSession.Value
		}
		compare("session", saved.NativeSession.Value, liveSession)
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

// discoverUnmanaged finds live agents in this feature's workspace that no
// saved role claimed. They are surfaced, never adopted: the bridge cannot know
// whether a pane a human opened is meant to be managed.
func discoverUnmanaged(feature *Feature, state model.FeatureState, live []herdr.AgentInfo, claimed map[string]struct{}) []Agent {
	workspace := state.WorkspaceID
	if workspace == "" {
		return nil
	}
	var rows []Agent
	for _, candidate := range live {
		if candidate.WorkspaceID != workspace {
			continue
		}
		if _, taken := claimed[candidate.PaneID]; taken {
			continue
		}
		rows = append(rows, Agent{
			Feature:            feature.Slug,
			Managed:            false,
			Kind:               candidate.Agent,
			Live:               liveIdentity(candidate),
			Status:             AgentStatus(normalizeStatus(candidate.AgentStatus)),
			StatusAvailability: AvailabilityAvailable,
			Binding:            BindingMissing,
			BindingDetail:      "this agent has no bridge role",
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
			ID:      schedule.ID,
			State:   string(schedule.State),
			Summary: "scheduled action " + schedule.ID + " (" + string(schedule.State) + ")",
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

func savedIdentity(saved model.RoleAgent) Identity {
	return Identity{
		Workspace: saved.WorkspaceID,
		Pane:      saved.PaneID,
		Terminal:  saved.TerminalID,
		Session:   saved.NativeSession.Value,
		Kind:      saved.Kind,
		Source:    saved.NativeSession.Source,
	}
}

func liveIdentity(live herdr.AgentInfo) Identity {
	identity := Identity{
		Workspace: live.WorkspaceID,
		Pane:      live.PaneID,
		Terminal:  live.TerminalID,
		Kind:      live.Agent,
	}
	if live.AgentSession != nil {
		identity.Session = live.AgentSession.Value
		identity.Source = live.AgentSession.Source
	}
	if identity.Session == "" {
		identity.Session = live.Name
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
