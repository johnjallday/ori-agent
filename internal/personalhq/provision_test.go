package personalhq

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
)

// wsWithAgents builds an in-memory workspace with the named agents (the first
// marked as the entry agent), for pure PlanUpgrade/ReadProvisionState tests
// that need no store.
func wsWithAgents(owner string, names ...string) *session.Workspace {
	ws := &session.Workspace{
		ID:          "ws-1",
		Name:        "HQ",
		Kind:        session.WorkspaceKindWorkspace,
		OwnerUserID: owner,
		Status:      session.WorkspaceStatusActive,
	}
	for i, n := range names {
		ws.AgentInstances = append(ws.AgentInstances, session.AgentInstance{
			ID:         n + "-id",
			Name:       n,
			EntryPoint: i == 0,
		})
	}
	return ws
}

func TestReadProvisionStateDefaultsToZero(t *testing.T) {
	if got := ReadProvisionState(nil); got.Version != 0 || got.LastUpgradeOutcome != UpgradeOutcomeNone {
		t.Fatalf("nil workspace should read as zero state, got %+v", got)
	}
	ws := wsWithAgents("")
	if got := ReadProvisionState(ws); got.Version != 0 {
		t.Fatalf("workspace with no record should read version 0, got %+v", got)
	}
	// A malformed value must degrade to zero, never panic.
	ws.SharedData = map[string]any{provisioningSharedDataKey: "not-an-object"}
	if got := ReadProvisionState(ws); got.Version != 0 {
		t.Fatalf("malformed record should read version 0, got %+v", got)
	}
}

func TestWriteReadProvisionStateRoundTrip(t *testing.T) {
	ws := wsWithAgents("")
	want := ProvisionState{Version: 1, LastUpgradeOutcome: UpgradeOutcomeSuccess}
	if err := writeProvisionState(ws, want); err != nil {
		t.Fatalf("writeProvisionState: %v", err)
	}
	got := ReadProvisionState(ws)
	if got.Version != want.Version || got.LastUpgradeOutcome != want.LastUpgradeOutcome {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("writeProvisionState should stamp UpdatedAt")
	}
}

func TestFindRoleInstanceMatchesByName(t *testing.T) {
	ws := wsWithAgents("", "Personal Chief of Staff", "journal") // case-insensitive
	// V1Roster is [Chief, Journal] after the Email Ops spin-off dropped Inbox.
	if _, ok := FindRoleInstance(ws, V1Roster[1]); !ok {
		t.Fatal("expected to find the Journal role case-insensitively")
	}
	// A role the workspace does not have must not be found.
	if _, ok := FindRoleInstance(ws, SpecialistRole{Slug: "inbox", AgentName: "Inbox"}); ok {
		t.Fatal("did not expect to find an Inbox role")
	}
}

// assistantBuiltHQ builds an in-memory HQ created around a hired personal
// assistant: the entry agent carries the user's chosen name (never "Personal
// Chief of Staff") and the workspace carries the presentation marker the
// personal-assistant creation path stamps.
func assistantBuiltHQ(names ...string) *session.Workspace {
	ws := wsWithAgents("user-1", names...)
	ws.SharedData = map[string]any{
		PersonalAssistantPresentationKey: map[string]any{"version": 1, "assistant_id": "assistant-1"},
	}
	return ws
}

func TestFindRoleInstanceAssistantEntryFulfilsEntryRole(t *testing.T) {
	ws := assistantBuiltHQ("Assistant", "Journal")
	inst, ok := FindRoleInstance(ws, V1Roster[0])
	if !ok || inst.Name != "Assistant" || !inst.EntryPoint {
		t.Fatalf("the hired assistant must fulfil the entry role, got %+v ok=%v", inst, ok)
	}
	if AssistantEntryInstance(ws) == nil {
		t.Fatal("assistant-built HQ must expose its assistant entry instance")
	}

	// Without the presentation marker the entry role is still matched by name
	// only: an arbitrary entry agent is not the Chief of Staff.
	plain := wsWithAgents("user-1", "Assistant", "Journal")
	if _, ok := FindRoleInstance(plain, V1Roster[0]); ok {
		t.Fatal("an unmarked workspace must not treat its entry agent as the Chief of Staff")
	}
	if AssistantEntryInstance(plain) != nil {
		t.Fatal("unmarked workspace must not report an assistant entry instance")
	}
}

func TestPlanUpgradeAssistantBuiltHQNeedsNoChiefOfStaff(t *testing.T) {
	// Contract: a confirmed Map HQ build yields one entry instance (the hired
	// assistant), one Journal support instance and zero Personal Chief of
	// Staff. The planner must read that roster as complete rather than offer to
	// add a second orchestrator beside the assistant.
	ws := assistantBuiltHQ("Assistant", "Journal")
	if err := writeProvisionState(ws, ProvisionState{Version: CurrentProvisioningVersion}); err != nil {
		t.Fatalf("writeProvisionState: %v", err)
	}
	plan := PlanUpgrade(ws, "user-1")
	if plan.HasChanges() || len(plan.Conflicts) > 0 {
		t.Fatalf("assistant-built HQ must need no roster changes, got %+v", plan)
	}
	if !plan.UpToDate {
		t.Fatalf("assistant-built HQ at the current version must be up to date: %+v", plan)
	}
	if containsSubstr(plan.PreservedCustomizations, "Assistant") {
		t.Fatalf("the assistant is the roster's entry agent, not an 'other' agent: %v", plan.PreservedCustomizations)
	}
}

func TestPlanUpgradeAssistantBuiltHQOnlyAddsMissingSupport(t *testing.T) {
	ws := assistantBuiltHQ("Assistant")
	plan := PlanUpgrade(ws, "user-1")
	if len(plan.MissingRoles) != 1 || plan.MissingRoles[0] != "Journal" {
		t.Fatalf("only the Journal support role may be missing, got %v", plan.MissingRoles)
	}
	if len(plan.Conflicts) > 0 {
		t.Fatalf("no conflicts expected, got %v", plan.Conflicts)
	}
}

func TestPlanUpgradeAssistantBuiltHQTreatsStrayChiefAsUserAgent(t *testing.T) {
	// An HQ that already received a duplicate Personal Chief of Staff from an
	// earlier upgrade: the assistant still fulfils the entry role, and the stray
	// instance is reported as the user's own agent (never removed, never
	// mistaken for a missing or conflicting role).
	ws := assistantBuiltHQ("Assistant", "Journal", "Personal Chief of Staff")
	plan := PlanUpgrade(ws, "user-1")
	if plan.HasChanges() || len(plan.Conflicts) > 0 {
		t.Fatalf("a stray chief must not produce roster changes or conflicts: %+v", plan)
	}
	if !containsSubstr(plan.PreservedCustomizations, "Personal Chief of Staff") {
		t.Fatalf("a stray chief must be listed as a preserved user agent: %v", plan.PreservedCustomizations)
	}
}

func TestPlanUpgradeArbitraryWorkspaceNeedsFullRoster(t *testing.T) {
	// An arbitrary designated workspace (no assistant roster, no record) must
	// converge onto the full roster via the same plan path (task 2.1/2.9),
	// without assuming the personal-ops template.
	ws := wsWithAgents("user-1", "My Notes Agent")
	plan := PlanUpgrade(ws, "user-1")

	if plan.Blocked() {
		t.Fatalf("eligible workspace must not be blocked: %+v", plan.Blockers)
	}
	if plan.UpToDate {
		t.Fatal("a bare workspace must not report up to date")
	}
	// Post-spin-off roster is Chief + Journal (Inbox moved to Email Ops).
	if len(plan.MissingRoles) != len(V1Roster) {
		t.Fatalf("expected all %d specialist roles missing, got %v", len(V1Roster), plan.MissingRoles)
	}
	if !plan.HasChanges() {
		t.Fatal("plan with missing roles must report changes")
	}
	// The user's own agent must be listed as preserved, never removed.
	if !containsSubstr(plan.PreservedCustomizations, "My Notes Agent") {
		t.Fatalf("user agent must be preserved, got %v", plan.PreservedCustomizations)
	}
}

func TestPlanUpgradeFullRosterProvisionedIsUpToDate(t *testing.T) {
	ws := wsWithAgents("user-1", "Personal Chief of Staff", "Inbox", "Journal")
	if err := writeProvisionState(ws, ProvisionState{Version: CurrentProvisioningVersion}); err != nil {
		t.Fatalf("writeProvisionState: %v", err)
	}
	plan := PlanUpgrade(ws, "user-1")
	if !plan.UpToDate {
		t.Fatalf("fully provisioned HQ should be up to date: %+v", plan)
	}
	if plan.HasChanges() {
		t.Fatalf("up-to-date HQ should have no changes: %+v", plan)
	}
}

func TestPlanUpgradeFreshTemplateRosterVersionZero(t *testing.T) {
	// A just-created personal-ops workspace already has the roster but no
	// version stamp yet; the plan reports no roster additions but is not up to
	// date, so apply simply records the version.
	ws := wsWithAgents("user-1", "Personal Chief of Staff", "Inbox", "Journal")
	plan := PlanUpgrade(ws, "user-1")
	if plan.HasChanges() {
		t.Fatalf("full roster should need no additions, got %v", plan.MissingRoles)
	}
	if plan.UpToDate {
		t.Fatal("version 0 must not be up to date even with a full roster")
	}
}

func TestPlanUpgradeSurfacesRetryablePriorFailure(t *testing.T) {
	ws := wsWithAgents("user-1", "Personal Chief of Staff")
	if err := writeProvisionState(ws, ProvisionState{Version: 0, LastUpgradeOutcome: UpgradeOutcomePartial}); err != nil {
		t.Fatalf("writeProvisionState: %v", err)
	}
	plan := PlanUpgrade(ws, "user-1")
	if !plan.RetryablePriorFailure {
		t.Fatal("a partial prior outcome must surface as retryable")
	}
}

func TestPlanUpgradeBlocksIneligibleTargets(t *testing.T) {
	group := wsWithAgents("user-1")
	group.Kind = session.WorkspaceKindGroup
	if plan := PlanUpgrade(group, "user-1"); !plan.Blocked() {
		t.Fatal("group workspace must be blocked")
	}

	wrongOwner := wsWithAgents("someone-else", "Personal Chief of Staff")
	if plan := PlanUpgrade(wrongOwner, "user-1"); !plan.Blocked() {
		t.Fatal("wrong-owner workspace must be blocked")
	}

	if plan := PlanUpgrade(nil, "user-1"); !plan.Blocked() {
		t.Fatal("nil workspace must be blocked")
	}
}

func containsSubstr(items []string, substr string) bool {
	for _, s := range items {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
