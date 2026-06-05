package agenthttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newHomeWorkspaceResolverTestWorkspace(id, name, description string, agents ...string) *workspace.Workspace {
	return &workspace.Workspace{
		ID:          id,
		Name:        name,
		Description: description,
		Agents:      agents,
		Status:      workspace.StatusActive,
		SharedData: map[string]any{
			"workspace_bootstrap": map[string]any{
				"goal":         description,
				"systems":      "",
				"capabilities": "",
				"context":      "",
			},
		},
	}
}

func newHomeWorkspaceResolverForTest(t *testing.T, agentStore store.Store, workspaces ...*workspace.Workspace) *HomeAssistantWorkspaceResolver {
	t.Helper()
	wsStore := workspace.NewInMemoryStore()
	for _, ws := range workspaces {
		if err := wsStore.Save(ws); err != nil {
			t.Fatalf("seed workspace: %v", err)
		}
	}
	return NewHomeAssistantWorkspaceResolver(wsStore, agentStore)
}

type homeWorkspaceResolverFeedbackReaderStub struct {
	preferredWorkspaceID string
	ok                   bool
	recentCorrections    []HomeAssistantWorkspaceCorrection
}

// setWorkspaceBootstrapField mutates a workspace's workspace_bootstrap shared
// data so tests can exercise the per-field scoring that used to be fed by the
// (now-removed) canonical "Workspace Description" note.
func setWorkspaceBootstrapField(ws *workspace.Workspace, key, value string) {
	bootstrap, _ := ws.SharedData["workspace_bootstrap"].(map[string]any)
	if bootstrap == nil {
		bootstrap = map[string]any{}
		if ws.SharedData == nil {
			ws.SharedData = map[string]any{}
		}
		ws.SharedData["workspace_bootstrap"] = bootstrap
	}
	bootstrap[key] = value
}

func (s *homeWorkspaceResolverFeedbackReaderStub) PreferredWorkspaceForPrompt(_ context.Context, _ string) (string, bool, error) {
	return s.preferredWorkspaceID, s.ok, nil
}

func (s *homeWorkspaceResolverFeedbackReaderStub) RecentWorkspaceCorrections(_ context.Context, _ int) ([]HomeAssistantWorkspaceCorrection, error) {
	return append([]HomeAssistantWorkspaceCorrection(nil), s.recentCorrections...), nil
}

func TestHomeAssistantWorkspaceResolver_ConfidentMatch(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Launch Manager", &store.CreateAgentConfig{Type: "general"}, "", nil, nil)

	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		newHomeWorkspaceResolverTestWorkspace("ws-launch", "Launch Ops", "Ship the launch deck", "Launch Manager"),
		newHomeWorkspaceResolverTestWorkspace("ws-marketing", "Marketing Site", "Maintain the public website", "Launch Manager"),
	)

	got := resolver.Resolve("finish the launch deck today", normalizedHomeAssistantRouteContext{})
	if got.State != homeAssistantWorkspaceStateConfident {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateConfident, got)
	}
	if got.SelectedWorkspaceID != "ws-launch" {
		t.Fatalf("expected ws-launch, got %q", got.SelectedWorkspaceID)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(got.Candidates))
	}
}

func TestHomeAssistantWorkspaceResolver_AmbiguousMatch(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Project Manager", &store.CreateAgentConfig{Type: "general"}, "", nil, nil)

	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		newHomeWorkspaceResolverTestWorkspace("ws-alpha", "Launch Alpha", "Ship launch tasks", "Project Manager"),
		newHomeWorkspaceResolverTestWorkspace("ws-beta", "Launch Beta", "Ship launch tasks", "Project Manager"),
	)

	got := resolver.Resolve("ship launch tasks", normalizedHomeAssistantRouteContext{})
	if got.State != homeAssistantWorkspaceStateAmbiguous {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateAmbiguous, got)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(got.Candidates))
	}
}

func TestHomeAssistantWorkspaceResolver_NoFit(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Travel Manager", &store.CreateAgentConfig{Type: "general"}, "", nil, nil)

	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		newHomeWorkspaceResolverTestWorkspace("ws-trip", "Trip Planning", "Plan Portugal travel", "Travel Manager"),
	)

	got := resolver.Resolve("review payroll numbers", normalizedHomeAssistantRouteContext{})
	if got.State != homeAssistantWorkspaceStateNoFit {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateNoFit, got)
	}
}

func TestHomeAssistantWorkspaceResolver_UsesActiveWorkspaceContext(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Workspace Manager", &store.CreateAgentConfig{Type: "general"}, "", nil, nil)

	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		newHomeWorkspaceResolverTestWorkspace("ws-active", "Active Workspace", "General work", "Workspace Manager"),
		newHomeWorkspaceResolverTestWorkspace("ws-better", "Launch Ops", "Ship the launch deck", "Workspace Manager"),
	)

	got := resolver.Resolve("finish the launch deck", normalizedHomeAssistantRouteContext{WorkspaceID: "ws-active"})
	if got.State != homeAssistantWorkspaceStateConfident {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateConfident, got)
	}
	if got.SelectedWorkspaceID != "ws-active" {
		t.Fatalf("expected active workspace to win, got %q", got.SelectedWorkspaceID)
	}
}

func TestHomeAssistantWorkspaceResolver_ExcludesGroupWorkspaces(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Launch Manager", &store.CreateAgentConfig{Type: "general"}, "", nil, nil)

	group := newHomeWorkspaceResolverTestWorkspace("ws-group", "Launch Ops", "Ship the launch deck", "Launch Manager")
	group.Kind = "group"

	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		group,
		newHomeWorkspaceResolverTestWorkspace("ws-direct", "Operations", "Review launch prep", "Launch Manager"),
	)

	got := resolver.Resolve("ship the launch deck", normalizedHomeAssistantRouteContext{})
	if got.State != homeAssistantWorkspaceStateConfident {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateConfident, got)
	}
	if got.SelectedWorkspaceID != "ws-direct" {
		t.Fatalf("expected direct-use workspace, got %q", got.SelectedWorkspaceID)
	}
}

func TestHomeAssistantWorkspaceResolver_NeedsRepairWhenEntryAgentMissing(t *testing.T) {
	st := newHomeRouteTestStore(t)
	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		newHomeWorkspaceResolverTestWorkspace("ws-broken", "Launch Ops", "Ship the launch deck", "Missing Manager"),
	)

	got := resolver.Resolve("ship the launch deck", normalizedHomeAssistantRouteContext{})
	if got.State != homeAssistantWorkspaceStateNeedsRepair {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateNeedsRepair, got)
	}
	if got.RepairReason == "" {
		t.Fatalf("expected repair reason")
	}
}

func TestHomeAssistantWorkspaceResolver_UsesProjectPathAndDirectoryReferences(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Workspace Manager", &store.CreateAgentConfig{Type: "general"}, "", nil, nil)

	cabinet := newHomeWorkspaceResolverTestWorkspace("ws-cabinet", "Woodworking", "Build home storage", "Workspace Manager")
	cabinet.ProjectPath = "apps/cabinet-api"
	cabinet.DirectoryReferences = []workspace.DirectoryReference{
		{Name: "Cabinet repository", Path: "/Users/jjdev/Projects/cabinet-api"},
	}
	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		cabinet,
		newHomeWorkspaceResolverTestWorkspace("ws-home", "Home Projects", "Track apartment tasks", "Workspace Manager"),
	)

	got := resolver.Resolve("finish the cabinet api auth work", normalizedHomeAssistantRouteContext{})
	if got.State != homeAssistantWorkspaceStateConfident {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateConfident, got)
	}
	if got.SelectedWorkspaceID != "ws-cabinet" {
		t.Fatalf("expected ws-cabinet, got %q", got.SelectedWorkspaceID)
	}
	if !containsReason(got.Reasons, "matched workspace project path") &&
		!containsReason(got.Reasons, "matched workspace directories") {
		t.Fatalf("expected richer metadata reason, got %v", got.Reasons)
	}
}

func TestHomeAssistantWorkspaceResolver_BootstrapContextBreaksMetadataTie(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Project Manager", &store.CreateAgentConfig{Type: "general"}, "", nil, nil)

	// Two workspaces identical on name/description; only the bootstrap context
	// of ws-alpha mentions the brand guide. This used to live in the canonical
	// "Workspace Description" note; it now comes straight from SharedData.
	alpha := newHomeWorkspaceResolverTestWorkspace("ws-alpha", "Launch Alpha", "Ship launch tasks", "Project Manager")
	setWorkspaceBootstrapField(alpha, "context", "Brand guide rollout and creative review notes")
	beta := newHomeWorkspaceResolverTestWorkspace("ws-beta", "Launch Beta", "Ship launch tasks", "Project Manager")

	resolver := newHomeWorkspaceResolverForTest(t, st, alpha, beta)

	got := resolver.Resolve("review the launch brand guide", normalizedHomeAssistantRouteContext{})
	if got.State != homeAssistantWorkspaceStateConfident {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateConfident, got)
	}
	if got.SelectedWorkspaceID != "ws-alpha" {
		t.Fatalf("expected ws-alpha, got %q", got.SelectedWorkspaceID)
	}
	if !containsReason(got.Reasons, "matched workspace context") {
		t.Fatalf("expected bootstrap-context reason, got %v", got.Reasons)
	}
}

func TestHomeAssistantWorkspaceResolver_DoesNotUseSubstringOnlyMatches(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Workspace Manager", &store.CreateAgentConfig{Type: "general"}, "", nil, nil)

	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		newHomeWorkspaceResolverTestWorkspace("ws-signals", "Signals", "Decode launch signals", "Workspace Manager"),
	)

	got := resolver.Resolve("review code changes", normalizedHomeAssistantRouteContext{})
	if got.State != homeAssistantWorkspaceStateNoFit {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateNoFit, got)
	}
}

func TestHomeAssistantWorkspaceResolver_UsesPriorCorrectionForExactPrompt(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Workspace Manager", &store.CreateAgentConfig{Type: "general"}, "", nil, nil)

	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		newHomeWorkspaceResolverTestWorkspace("ws-cabinet", "Cabinet", "Build the cabinet roadmap", "Workspace Manager"),
		newHomeWorkspaceResolverTestWorkspace("ws-ops", "Ops Hub", "Operational planning", "Workspace Manager"),
	)
	resolver.SetFeedbackReader(&homeWorkspaceResolverFeedbackReaderStub{
		preferredWorkspaceID: "ws-ops",
		ok:                   true,
	})

	got := resolver.Resolve("build the cabinet roadmap", normalizedHomeAssistantRouteContext{})
	if got.State != homeAssistantWorkspaceStateConfident {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateConfident, got)
	}
	if got.SelectedWorkspaceID != "ws-ops" {
		t.Fatalf("expected prior correction to choose ws-ops, got %q", got.SelectedWorkspaceID)
	}
	if !containsReason(got.Reasons, "using prior workspace correction") {
		t.Fatalf("expected prior-correction reason, got %v", got.Reasons)
	}
}

func TestHomeAssistantWorkspaceResolver_IgnoresPriorCorrectionForExplicitSwitch(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Workspace Manager", &store.CreateAgentConfig{Type: "general"}, "", nil, nil)

	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		newHomeWorkspaceResolverTestWorkspace("ws-cabinet", "Cabinet", "Build the cabinet roadmap", "Workspace Manager"),
		newHomeWorkspaceResolverTestWorkspace("ws-ops", "Ops Hub", "Operational planning", "Workspace Manager"),
	)
	resolver.SetFeedbackReader(&homeWorkspaceResolverFeedbackReaderStub{
		preferredWorkspaceID: "ws-ops",
		ok:                   true,
	})

	got := resolver.Resolve("switch workspace to cabinet for the roadmap", normalizedHomeAssistantRouteContext{})
	if got.State != homeAssistantWorkspaceStateConfident {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateConfident, got)
	}
	if got.SelectedWorkspaceID != "ws-cabinet" {
		t.Fatalf("expected explicit switch to route by current prompt, got %q", got.SelectedWorkspaceID)
	}
	if containsReason(got.Reasons, "using prior workspace correction") {
		t.Fatalf("did not expect prior correction on explicit switch, got %v", got.Reasons)
	}
}

func TestHomeAssistantWorkspaceResolver_UsesSimilarPriorCorrectionForStrongPromptVariant(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Workspace Manager", &store.CreateAgentConfig{Type: "general"}, "", nil, nil)

	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		newHomeWorkspaceResolverTestWorkspace("ws-cabinet", "Cabinet", "Build the cabinet roadmap", "Workspace Manager"),
		newHomeWorkspaceResolverTestWorkspace("ws-ops", "Ops Hub", "Operational planning", "Workspace Manager"),
	)
	resolver.SetFeedbackReader(&homeWorkspaceResolverFeedbackReaderStub{
		recentCorrections: []HomeAssistantWorkspaceCorrection{
			{Prompt: "please build the cabinet roadmap", WorkspaceID: "ws-ops"},
		},
	})

	got := resolver.Resolve("build the cabinet roadmap", normalizedHomeAssistantRouteContext{})
	if got.State != homeAssistantWorkspaceStateConfident {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateConfident, got)
	}
	if got.SelectedWorkspaceID != "ws-ops" {
		t.Fatalf("expected fuzzy prior correction to choose ws-ops, got %q", got.SelectedWorkspaceID)
	}
	if !containsReason(got.Reasons, "using similar prior workspace correction") {
		t.Fatalf("expected similar-correction reason, got %v", got.Reasons)
	}
}

func TestHomeAssistantWorkspaceResolver_RejectsWeakFuzzyCorrection(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Workspace Manager", &store.CreateAgentConfig{Type: "general"}, "", nil, nil)

	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		newHomeWorkspaceResolverTestWorkspace("ws-cabinet", "Cabinet", "Build the cabinet roadmap", "Workspace Manager"),
		newHomeWorkspaceResolverTestWorkspace("ws-ops", "Ops Hub", "Operational planning", "Workspace Manager"),
	)
	resolver.SetFeedbackReader(&homeWorkspaceResolverFeedbackReaderStub{
		recentCorrections: []HomeAssistantWorkspaceCorrection{
			{Prompt: "build the cabinet roadmap today", WorkspaceID: "ws-ops"},
		},
	})

	got := resolver.Resolve("build the cabinet roadmap next week", normalizedHomeAssistantRouteContext{})
	if got.State != homeAssistantWorkspaceStateConfident {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateConfident, got)
	}
	if got.SelectedWorkspaceID != "ws-cabinet" {
		t.Fatalf("expected normal scoring to keep ws-cabinet, got %q", got.SelectedWorkspaceID)
	}
	if containsReason(got.Reasons, "using similar prior workspace correction") {
		t.Fatalf("did not expect weak fuzzy correction to win, got %v", got.Reasons)
	}
}

func TestHomeAssistantWorkspaceResolver_RejectsConflictingFuzzyCorrections(t *testing.T) {
	st := newHomeRouteTestStore(t)
	addHomeRouteTestAgent(t, st, "Workspace Manager", &store.CreateAgentConfig{Type: "general"}, "", nil, nil)

	resolver := newHomeWorkspaceResolverForTest(
		t,
		st,
		newHomeWorkspaceResolverTestWorkspace("ws-cabinet", "Cabinet", "Build the cabinet roadmap", "Workspace Manager"),
		newHomeWorkspaceResolverTestWorkspace("ws-ops", "Ops Hub", "Operational planning", "Workspace Manager"),
		newHomeWorkspaceResolverTestWorkspace("ws-finance", "Finance Ops", "Budget planning", "Workspace Manager"),
	)
	resolver.SetFeedbackReader(&homeWorkspaceResolverFeedbackReaderStub{
		recentCorrections: []HomeAssistantWorkspaceCorrection{
			{Prompt: "please build the cabinet roadmap", WorkspaceID: "ws-ops"},
			{Prompt: "build cabinet roadmap", WorkspaceID: "ws-finance"},
		},
	})

	got := resolver.Resolve("build the cabinet roadmap", normalizedHomeAssistantRouteContext{})
	if got.State != homeAssistantWorkspaceStateConfident {
		t.Fatalf("expected state %q, got %#v", homeAssistantWorkspaceStateConfident, got)
	}
	if got.SelectedWorkspaceID != "ws-cabinet" {
		t.Fatalf("expected conflicting fuzzy corrections to fall back to ws-cabinet, got %q", got.SelectedWorkspaceID)
	}
	if containsReason(got.Reasons, "using similar prior workspace correction") {
		t.Fatalf("did not expect conflicting fuzzy correction to win, got %v", got.Reasons)
	}
}

func containsReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}
