package overview

// Eligibility answers one question about every agent on the roster: may an
// Overnight Run control it unattended?
//
// It is deliberately a three-state answer. "Eligible" and "ineligible" are
// claims about evidence that was actually examined; "unverified" is the honest
// answer while a required check has not run yet. Collapsing unverified into
// eligible would let an agent be enrolled on the strength of checks nobody
// performed, and collapsing it into ineligible would hide agents that are one
// readiness check away from being usable.

// EligibilityState is the resolved answer for one agent.
type EligibilityState string

const (
	// EligibilityEligible means every requirement was checked and met.
	EligibilityEligible EligibilityState = "eligible"
	// EligibilityIneligible means a requirement was checked and failed.
	EligibilityIneligible EligibilityState = "ineligible"
	// EligibilityUnverified means the structural requirements are met but a
	// required check has not been performed, so the agent must not be enrolled.
	EligibilityUnverified EligibilityState = "unverified"
)

// Label is the full textual state for human output.
func (e EligibilityState) Label() string {
	switch e {
	case EligibilityEligible:
		return "eligible"
	case EligibilityIneligible:
		return "not eligible"
	default:
		return "unverified"
	}
}

// EligibilityBlocker is a stable machine-readable requirement that failed or
// has not been checked. Codes are part of the JSON contract.
type EligibilityBlocker string

const (
	// BlockerStatusUnavailable means the agent's live state could not be read,
	// so nothing about it can be claimed.
	BlockerStatusUnavailable EligibilityBlocker = "status_unavailable"
	// BlockerNotFeatureScoped means the agent is not working in a feature
	// worktree, so there is no task list to advance.
	BlockerNotFeatureScoped EligibilityBlocker = "not_feature_scoped"
	// BlockerUnmanaged means no bridge role claims this agent.
	BlockerUnmanaged EligibilityBlocker = "unmanaged"
	// BlockerNotClaude means the agent is not a Claude agent.
	BlockerNotClaude EligibilityBlocker = "not_claude"
	// BlockerBindingNotExact means the saved role does not resolve to exactly
	// one live agent.
	BlockerBindingNotExact EligibilityBlocker = "binding_not_exact"
	// BlockerNoNativeSession means no exact native Claude session is saved, so
	// a later continuation could not be aimed at the same conversation.
	BlockerNoNativeSession EligibilityBlocker = "no_native_session"
	// BlockerNoWorktree means the feature has no canonical checkout.
	BlockerNoWorktree EligibilityBlocker = "no_worktree"
	// BlockerTaskListUnreadable means the feature's task list is missing or
	// could not be parsed.
	BlockerTaskListUnreadable EligibilityBlocker = "task_list_unreadable"
	// BlockerClaudeReadinessUnverified means the structural requirements hold
	// but Claude's session-limit, authentication, and billing posture have not
	// been established for this session.
	BlockerClaudeReadinessUnverified EligibilityBlocker = "claude_readiness_unverified"
)

// Eligibility is the roster's answer plus the evidence behind it.
type Eligibility struct {
	State EligibilityState `json:"state"`
	// Reason is a plain-language sentence naming the first outstanding
	// requirement. It is always present unless the agent is eligible.
	Reason string `json:"reason,omitempty"`
	// Blockers lists every failed or outstanding requirement, most decisive
	// first, so a selector can explain itself without re-deriving the rules.
	Blockers []EligibilityBlocker `json:"blockers,omitempty"`
}

// claudeKind is the only agent kind an Overnight Run may control.
const claudeKind = "claude"

// evaluateEligibility grades one roster row against the structural
// requirements this snapshot can observe.
//
// It never inspects Claude itself: whether a session is plan-backed, whether a
// structured limit adapter is installed, and whether the next prompt would
// spend credits are separate checks, and until they have run the answer stays
// unverified rather than optimistic.
func evaluateEligibility(agent Agent, feature *Feature) Eligibility {
	var blockers []EligibilityBlocker
	reason := ""
	block := func(code EligibilityBlocker, explanation string) {
		blockers = append(blockers, code)
		if reason == "" {
			reason = explanation
		}
	}

	if !agent.StatusAvailability.OK() {
		block(BlockerStatusUnavailable, "This agent's live state could not be read.")
	}
	if agent.Scope != AgentScopeFeature {
		block(BlockerNotFeatureScoped, "This agent is not working in a feature worktree, so it has no task list to advance.")
	}
	if !agent.Managed {
		block(BlockerUnmanaged, "No bridge role claims this agent, so it is not managed.")
	}
	if !isClaude(agent) {
		block(BlockerNotClaude, "Overnight Runs control Claude agents only.")
	}
	if agent.Binding != BindingExact {
		block(BlockerBindingNotExact, "This agent's saved role does not resolve to exactly one live agent.")
	}
	if agent.Saved.Session == "" {
		block(BlockerNoNativeSession, "No exact native Claude session is saved for this agent.")
	}
	if feature == nil || feature.Git.WorktreePath == "" {
		block(BlockerNoWorktree, "This agent's feature has no canonical worktree.")
	}
	if feature != nil && !feature.Plan.TaskListAvailability.OK() {
		block(BlockerTaskListUnreadable, "This feature's task list could not be read.")
	}

	if len(blockers) > 0 {
		return Eligibility{State: EligibilityIneligible, Reason: reason, Blockers: blockers}
	}
	return Eligibility{
		State:    EligibilityUnverified,
		Reason:   "Claude session-limit, authentication, and billing readiness have not been checked for this session.",
		Blockers: []EligibilityBlocker{BlockerClaudeReadinessUnverified},
	}
}

// isClaude reports whether either the live or saved kind names Claude. The two
// are compared separately because a saved record can outlive a renamed pane.
func isClaude(agent Agent) bool {
	if agent.Live.Kind != "" {
		return agent.Live.Kind == claudeKind
	}
	return agent.Kind == claudeKind || agent.Saved.Kind == claudeKind
}
