package overview

import (
	"slices"
	"strings"
	"testing"
)

// eligibleAgent is a roster row that meets every structural requirement, so a
// test only has to state the one thing it is breaking.
func eligibleAgent(mutate ...func(*Agent)) Agent {
	agent := Agent{
		Feature:            "managed-feature",
		Scope:              AgentScopeFeature,
		Role:               "builder",
		Managed:            true,
		Kind:               claudeKind,
		Saved:              Identity{Workspace: "w-1", Pane: "w-1:p1", Session: "sess-1", Kind: claudeKind},
		Live:               Identity{Workspace: "w-1", Pane: "w-1:p1", Session: "sess-1", Kind: claudeKind},
		Status:             AgentIdle,
		StatusAvailability: AvailabilityAvailable,
		Binding:            BindingExact,
		MatchedPath:        "/w/managed-feature",
	}
	for _, apply := range mutate {
		apply(&agent)
	}
	return agent
}

// readyFeature is a feature whose plan can be read and whose worktree exists.
func readyFeature(mutate ...func(*Feature)) Feature {
	row := Feature{
		Slug: "managed-feature",
		Git:  GitState{Availability: AvailabilityAvailable, WorktreePath: "/w/managed-feature"},
		Plan: Plan{
			Copy:                 PlanCopyActive,
			TaskListPath:         "/w/managed-feature/tasks/tasks-managed-feature.md",
			TaskListAvailability: AvailabilityAvailable,
			Progress:             PlanProgress{Availability: AvailabilityAvailable, SubtasksTotal: 4, SubtasksCompleted: 1},
		},
	}
	for _, apply := range mutate {
		apply(&row)
	}
	return row
}

// TestEligibilityStopsShortOfClaimingReadiness is the safety property: a
// structurally perfect Claude agent is still not eligible, because nothing has
// yet established that its next prompt uses included plan capacity. FR19, FR127.
func TestEligibilityStopsShortOfClaimingReadiness(t *testing.T) {
	feature := readyFeature()
	result := evaluateEligibility(eligibleAgent(), &feature, nil)

	if result.State != EligibilityUnverified {
		t.Fatalf("state = %q, want unverified until Claude readiness is checked", result.State)
	}
	if !slices.Contains(result.Blockers, BlockerClaudeReadinessUnverified) {
		t.Fatalf("blockers = %v, want the unverified Claude readiness blocker", result.Blockers)
	}
	if result.Reason == "" {
		t.Fatal("an agent that may not be enrolled was given no reason")
	}
}

func TestEligibilityRejectsEachMissingRequirement(t *testing.T) {
	cases := []struct {
		name    string
		agent   Agent
		feature *Feature
		blocker EligibilityBlocker
	}{
		{
			name:    "a non-Claude agent",
			agent:   eligibleAgent(func(a *Agent) { a.Kind, a.Live.Kind, a.Saved.Kind = "codex", "codex", "codex" }),
			blocker: BlockerNotClaude,
		},
		{
			name:    "an agent no bridge role claims",
			agent:   eligibleAgent(func(a *Agent) { a.Managed = false }),
			blocker: BlockerUnmanaged,
		},
		{
			name:    "an agent working outside a feature",
			agent:   eligibleAgent(func(a *Agent) { a.Scope, a.Feature = AgentScopeRepository, "" }),
			blocker: BlockerNotFeatureScoped,
		},
		{
			name:    "an agent that could not be placed",
			agent:   eligibleAgent(func(a *Agent) { a.Scope, a.MatchedPath = AgentScopeUnknown, "" }),
			blocker: BlockerNotFeatureScoped,
		},
		{
			name:    "a binding that is not exact",
			agent:   eligibleAgent(func(a *Agent) { a.Binding = BindingPossibleDrift }),
			blocker: BlockerBindingNotExact,
		},
		{
			name:    "an ambiguous binding",
			agent:   eligibleAgent(func(a *Agent) { a.Binding = BindingAmbiguous }),
			blocker: BlockerBindingNotExact,
		},
		{
			name:    "no saved native session",
			agent:   eligibleAgent(func(a *Agent) { a.Saved.Session = "" }),
			blocker: BlockerNoNativeSession,
		},
		{
			name:    "an unreadable live status",
			agent:   eligibleAgent(func(a *Agent) { a.StatusAvailability = AvailabilityUnavailable }),
			blocker: BlockerStatusUnavailable,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			feature := readyFeature()
			result := evaluateEligibility(testCase.agent, &feature, nil)
			if result.State != EligibilityIneligible {
				t.Fatalf("state = %q, want ineligible", result.State)
			}
			if !slices.Contains(result.Blockers, testCase.blocker) {
				t.Fatalf("blockers = %v, want %q", result.Blockers, testCase.blocker)
			}
			if result.Reason == "" {
				t.Fatal("an ineligible agent was given no plain-language reason")
			}
		})
	}
}

func TestEligibilityRequiresAReadableFeaturePlan(t *testing.T) {
	missingWorktree := readyFeature(func(f *Feature) { f.Git.WorktreePath = "" })
	if result := evaluateEligibility(eligibleAgent(), &missingWorktree, nil); !slices.Contains(result.Blockers, BlockerNoWorktree) {
		t.Fatalf("blockers = %v, want no_worktree", result.Blockers)
	}

	for _, availability := range []Availability{AvailabilityAbsent, AvailabilityMalformed, AvailabilityUnavailable, AvailabilityUnknown} {
		unreadable := readyFeature(func(f *Feature) { f.Plan.TaskListAvailability = availability })
		result := evaluateEligibility(eligibleAgent(), &unreadable, nil)
		if result.State != EligibilityIneligible || !slices.Contains(result.Blockers, BlockerTaskListUnreadable) {
			t.Fatalf("task list %q produced %+v, want an ineligible task_list_unreadable result", availability, result)
		}
	}

	if result := evaluateEligibility(eligibleAgent(), nil, nil); result.State != EligibilityIneligible {
		t.Fatalf("an agent with no feature row produced %+v, want ineligible", result)
	}
}

// TestEligibilityReportsEveryBlockerNotJustTheFirst keeps the roster's
// explanation complete: an operator fixing one problem should be able to see
// the others without re-running and being told a new reason each time.
func TestEligibilityReportsEveryBlockerNotJustTheFirst(t *testing.T) {
	agent := eligibleAgent(func(a *Agent) {
		a.Managed = false
		a.Kind, a.Live.Kind, a.Saved.Kind = "codex", "codex", "codex"
		a.Binding = BindingMissing
		a.Saved.Session = ""
	})
	feature := readyFeature()
	result := evaluateEligibility(agent, &feature, nil)

	for _, want := range []EligibilityBlocker{BlockerUnmanaged, BlockerNotClaude, BlockerBindingNotExact, BlockerNoNativeSession} {
		if !slices.Contains(result.Blockers, want) {
			t.Fatalf("blockers = %v, want %q among them", result.Blockers, want)
		}
	}
	if slices.Contains(result.Blockers, BlockerClaudeReadinessUnverified) {
		t.Fatalf("an already-ineligible agent was also marked unverified: %v", result.Blockers)
	}
}

// TestEligibilityUsesTheClaudeAdapterVerdictWhenOneExists covers the other two
// thirds of the three-state answer: once the usage adapter has been consulted,
// a session it approves becomes eligible and a session it refuses becomes
// ineligible carrying the adapter's own reason.
func TestEligibilityUsesTheClaudeAdapterVerdictWhenOneExists(t *testing.T) {
	feature := readyFeature()

	var asked []string
	ready := func(sessionID string) ClaudeReadinessReport {
		asked = append(asked, sessionID)
		return ClaudeReadinessReport{Ready: true, AuthMode: "plan_backed"}
	}
	result := evaluateEligibility(eligibleAgent(), &feature, ready)
	if result.State != EligibilityEligible || len(result.Blockers) != 0 {
		t.Fatalf("result = %+v, want an eligible agent with no blockers", result)
	}
	// The adapter must be asked about the exact saved native session, never a
	// pane or a name that could belong to a different conversation.
	if len(asked) != 1 || asked[0] != "sess-1" {
		t.Fatalf("adapter was asked about %v, want the saved native session", asked)
	}

	refused := func(string) ClaudeReadinessReport {
		return ClaudeReadinessReport{
			Reason: "This session reported no Claude.ai subscription window, so included-plan capacity could not be established.",
		}
	}
	result = evaluateEligibility(eligibleAgent(), &feature, refused)
	if result.State != EligibilityIneligible {
		t.Fatalf("result = %+v, want ineligible", result)
	}
	if !slices.Contains(result.Blockers, BlockerClaudeNotReady) {
		t.Fatalf("blockers = %v, want claude_not_ready", result.Blockers)
	}
	if !strings.Contains(result.Reason, "included-plan capacity") {
		t.Fatalf("reason = %q, want the adapter's own explanation", result.Reason)
	}
}

// TestEligibilityNeverConsultsClaudeForAStructurallyIneligibleAgent keeps the
// cheap refusals first: a Codex agent is not a Claude session, so asking the
// Claude adapter about it would be meaningless.
func TestEligibilityNeverConsultsClaudeForAStructurallyIneligibleAgent(t *testing.T) {
	feature := readyFeature()
	consulted := false
	claude := func(string) ClaudeReadinessReport {
		consulted = true
		return ClaudeReadinessReport{Ready: true}
	}
	agent := eligibleAgent(func(a *Agent) { a.Kind, a.Live.Kind, a.Saved.Kind = "codex", "codex", "codex" })
	if result := evaluateEligibility(agent, &feature, claude); result.State != EligibilityIneligible {
		t.Fatalf("result = %+v, want ineligible", result)
	}
	if consulted {
		t.Fatal("the Claude usage adapter was consulted about a Codex agent")
	}
}
