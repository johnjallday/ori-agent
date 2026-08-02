package workspace

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// Goal brief and recommendation coverage (tasks 4.13, 4.14, 4.16;
// PRD FR-92–FR-106).

func acceptedBrief(brief GoalBrief) *GoalBrief {
	now := time.Now()
	brief.AcceptedAt = &now
	brief.Version = 1
	brief.Normalize()
	return &brief
}

func newRecommendationFixture(t *testing.T) *Workspace {
	t.Helper()
	ws := &Workspace{
		ID:             "ws-recommend",
		Version:        3,
		AgentInstances: []AgentInstance{{ID: "inst-1", Name: "Coder", NodeID: "coder-1", EntryPoint: true}},
		SkillBindings: []SkillBinding{
			{ID: "sb-summarize", SkillName: "summarize", Enabled: true},
			{ID: "sb-citations", SkillName: "citations", Enabled: true},
		},
		MCPBindings: []MCPBinding{
			{
				ID: "mb-web", ServerName: "web", Alias: "Web", Enabled: true,
				AllowedTools: []string{"search_web"}, DefaultSideEffect: SideEffectRead,
			},
			{
				ID: "mb-files", ServerName: "filesystem", Alias: "Files", Enabled: true,
				AllowedTools:      []string{"read_file", "write_file"},
				DefaultSideEffect: SideEffectRead,
				ToolOverrides:     map[string]SideEffect{"write_file": SideEffectWrite},
			},
		},
	}

	// Lean: exactly what a read-only research goal needs.
	if _, err := ws.CreateToolbox(ToolboxDefinition{
		ID:   "tbx-lean",
		Name: "Research Kit",
		Skills: []ToolboxSkillRef{
			{CapabilityID: "summarize", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-summarize", Required: true},
		},
		MCPBindings: []ToolboxMCPRef{{BindingID: "mb-web", AllowedTools: []string{"search_web"}, Required: true}},
	}); err != nil {
		t.Fatalf("CreateToolbox(lean) error = %v", err)
	}

	// Wide: covers the same goal but also carries a write tool and an unneeded
	// skill.
	if _, err := ws.CreateToolbox(ToolboxDefinition{
		ID:   "tbx-wide",
		Name: "Everything Kit",
		Skills: []ToolboxSkillRef{
			{CapabilityID: "summarize", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-summarize", Required: true},
			{CapabilityID: "citations", Source: ToolboxSourceWorkspaceProvided, BindingID: "sb-citations", Required: true},
		},
		MCPBindings: []ToolboxMCPRef{
			{BindingID: "mb-web", AllowedTools: []string{"search_web"}, Required: true},
			{BindingID: "mb-files", AllowedTools: []string{"read_file", "write_file"}, Required: true},
		},
	}); err != nil {
		t.Fatalf("CreateToolbox(wide) error = %v", err)
	}
	return ws
}

func recommendFor(t *testing.T, ws *Workspace, brief *GoalBrief) ToolboxRecommendationResult {
	t.Helper()
	instance := findAgentInstanceByID(ws, "inst-1")
	return RecommendToolboxes(ws, instance, brief, nil, 6, false, DefaultFocusThresholds())
}

// FR-94: an unaccepted brief drives nothing.
func TestRecommendToolboxes_RequiresAnAcceptedBrief(t *testing.T) {
	ws := newRecommendationFixture(t)
	draft := ProposeGoalBrief("search the web and summarize the findings", AutonomyWatch)

	result := recommendFor(t, ws, &draft)
	if len(result.Recommendations) != 0 {
		t.Fatalf("expected a draft brief to produce no ranking, got %d candidates", len(result.Recommendations))
	}
	if !strings.Contains(result.Message, "accept") {
		t.Fatalf("expected the message to say the brief needs accepting, got %q", result.Message)
	}
}

// FR-97: the smallest toolbox that covers the goal wins.
func TestRecommendToolboxes_PrefersTheSmallestCoveringToolbox(t *testing.T) {
	ws := newRecommendationFixture(t)
	brief := acceptedBrief(GoalBrief{
		Summary:              "Search the web and summarize",
		Operations:           []string{"search"},
		RequiredCapabilities: []string{"summarize"},
		MaxAutonomy:          GoalAutonomyRead,
	})

	result := recommendFor(t, ws, brief)
	if len(result.Recommendations) != 2 {
		t.Fatalf("expected both toolboxes to be ranked, got %d", len(result.Recommendations))
	}
	if result.BestMatch != "tbx-lean" {
		t.Fatalf("expected the lean toolbox to win, got %q (%+v)", result.BestMatch, result.Recommendations)
	}
	if !result.AnyFullyCovers {
		t.Fatalf("expected full coverage to be reported")
	}
	// The bigger one is ranked down for carrying what the goal did not ask for,
	// and for exceeding the read-only ceiling — but it is still offered.
	wide := result.Recommendations[1]
	if wide.ToolboxID != "tbx-wide" || !wide.ExceedsAutonomy {
		t.Fatalf("expected the wide toolbox to be ranked second and flagged, got %+v", wide)
	}
	if len(wide.Extra) == 0 {
		t.Fatalf("expected the unneeded capabilities to be named, got %+v", wide)
	}
}

// FR-95: identical inputs produce an identical ranking, every time.
func TestRecommendToolboxes_IsDeterministic(t *testing.T) {
	ws := newRecommendationFixture(t)
	brief := acceptedBrief(GoalBrief{
		Operations:           []string{"search", "read"},
		RequiredCapabilities: []string{"summarize"},
		MaxAutonomy:          GoalAutonomyWrite,
	})

	first := recommendFor(t, ws, brief)
	for range 15 {
		next := recommendFor(t, ws, brief)
		if len(next.Recommendations) != len(first.Recommendations) {
			t.Fatalf("ranking length changed between runs")
		}
		for i := range first.Recommendations {
			if next.Recommendations[i].ToolboxID != first.Recommendations[i].ToolboxID ||
				next.Recommendations[i].Score != first.Recommendations[i].Score {
				t.Fatalf("ranking changed between runs: %+v vs %+v", first.Recommendations, next.Recommendations)
			}
		}
	}
}

// FR-98: a recommendation explains coverage, gaps, and what it introduces.
func TestRecommendToolboxes_ExplainsItself(t *testing.T) {
	ws := newRecommendationFixture(t)
	brief := acceptedBrief(GoalBrief{
		Operations:           []string{"search"},
		RequiredCapabilities: []string{"summarize", "citations"},
		MaxAutonomy:          GoalAutonomyRead,
	})

	result := recommendFor(t, ws, brief)
	byID := make(map[string]ToolboxRecommendation, len(result.Recommendations))
	for _, candidate := range result.Recommendations {
		byID[candidate.ToolboxID] = candidate
	}

	// With citations required, the kit that has it outranks the smaller one —
	// coverage beats size (FR-96 before FR-97).
	if result.BestMatch != "tbx-wide" {
		t.Fatalf("expected the covering toolbox to win when the smaller one lacks a requirement, got %q", result.BestMatch)
	}

	lean := byID["tbx-lean"]
	if !containsString(lean.Missing, "citations") {
		t.Fatalf("expected the missing requirement to be named, got %+v", lean.Missing)
	}
	if !strings.Contains(lean.Explanation, "missing") {
		t.Fatalf("expected the explanation to mention what is missing, got %q", lean.Explanation)
	}

	// The covering kit introduces a write operation, which has to be visible
	// before the user picks it (FR-98).
	wide := byID["tbx-wide"]
	if len(wide.Covers) == 0 || len(wide.Missing) != 0 {
		t.Fatalf("expected the wide kit to cover everything, got %+v", wide)
	}
	if len(wide.IntroducesPermissions) == 0 {
		t.Fatalf("expected the write operation to be reported as an introduced permission, got %+v", wide)
	}
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}

// FR-101: when nothing fully covers the brief, say so and offer an INERT
// variant rather than claiming a match.
func TestRecommendToolboxes_ProposesAnInertVariantWhenNothingCovers(t *testing.T) {
	ws := newRecommendationFixture(t)
	brief := acceptedBrief(GoalBrief{
		Operations:           []string{"search"},
		RequiredCapabilities: []string{"summarize", "translation"},
		MaxAutonomy:          GoalAutonomyRead,
	})

	before := len(ws.GetToolboxes())
	result := recommendFor(t, ws, brief)

	if result.AnyFullyCovers {
		t.Fatalf("expected no full coverage when a required capability is absent")
	}
	if !strings.Contains(result.Message, "No saved toolbox covers everything") {
		t.Fatalf("expected an honest message, got %q", result.Message)
	}
	if result.ProposedVariant == nil {
		t.Fatalf("expected a proposed variant")
	}
	// FR-102: the variant is unsaved and unselected.
	if len(ws.GetToolboxes()) != before {
		t.Fatalf("expected the proposal to create nothing, got %d toolboxes", len(ws.GetToolboxes()))
	}
	if len(result.ProposedVariant.UnavailableRequirements) != 1 ||
		result.ProposedVariant.UnavailableRequirements[0] != "translation" {
		t.Fatalf("expected translation to be named as unavailable, got %+v", result.ProposedVariant)
	}
}

// FR-99: ranking changes nothing at all.
func TestRecommendToolboxes_ChangesNothing(t *testing.T) {
	ws := newRecommendationFixture(t)
	brief := acceptedBrief(GoalBrief{RequiredCapabilities: []string{"summarize"}})

	beforeVersion := ws.Version
	beforeAssignments := len(ws.ToolboxAssignments)
	beforeToolboxes := len(ws.GetToolboxes())
	beforeBindings := ws.GetMCPBindings()

	for range 3 {
		recommendFor(t, ws, brief)
	}

	if ws.Version != beforeVersion || len(ws.ToolboxAssignments) != beforeAssignments ||
		len(ws.GetToolboxes()) != beforeToolboxes {
		t.Fatalf("ranking mutated workspace state")
	}
	for i, binding := range ws.GetMCPBindings() {
		if binding.Enabled != beforeBindings[i].Enabled {
			t.Fatalf("ranking changed a binding's connection state")
		}
	}
}

// FR-20: an archived toolbox is not recommended — the user could not act on it.
func TestRecommendToolboxes_SkipsArchivedToolboxes(t *testing.T) {
	ws := newRecommendationFixture(t)
	if _, err := ws.SetToolboxStatus("tbx-wide", ToolboxStatusArchived); err != nil {
		t.Fatalf("SetToolboxStatus() error = %v", err)
	}
	brief := acceptedBrief(GoalBrief{RequiredCapabilities: []string{"summarize"}})

	result := recommendFor(t, ws, brief)
	for _, candidate := range result.Recommendations {
		if candidate.ToolboxID == "tbx-wide" {
			t.Fatalf("expected an archived toolbox to be excluded")
		}
	}
}

// The deterministic fallback proposer reads the workspace's own autonomy policy
// rather than guessing from prose (FR-94).
func TestProposeGoalBrief_UsesTheWorkspaceAutonomyPolicy(t *testing.T) {
	watch := ProposeGoalBrief("search the web and write a summary file", AutonomyWatch)
	if watch.MaxAutonomy != GoalAutonomyRead {
		t.Fatalf("expected a watch-only workspace to propose a read ceiling, got %q", watch.MaxAutonomy)
	}
	propose := ProposeGoalBrief("search the web and write a summary file", AutonomyPropose)
	if propose.MaxAutonomy != GoalAutonomyWrite {
		t.Fatalf("expected a propose workspace to allow writes, got %q", propose.MaxAutonomy)
	}
	if watch.Source != GoalBriefSourceFallback || watch.Accepted() {
		t.Fatalf("expected a proposal to be an unaccepted fallback, got %+v", watch)
	}
	// It reads what the text plainly says and nothing more.
	if len(watch.Operations) == 0 {
		t.Fatalf("expected the proposal to infer operations from the goal text")
	}
	if len(watch.RequiredCapabilities) != 0 {
		t.Fatalf("expected the proposal to invent no capability requirements, got %+v", watch.RequiredCapabilities)
	}
}

// --- Preflight (FR-103–FR-105) ---

func TestPreflightGoalToolbox_PinnedVersionSurvivesALaterEdit(t *testing.T) {
	ws := newRecommendationFixture(t)
	ws.GoalToolboxPolicy = &GoalToolboxPolicy{
		EntryAgentInstanceID: "inst-1",
		ToolboxID:            "tbx-lean",
		ToolboxVersion:       1,
	}

	// Editing the toolbox creates v2; the pin must stay on v1 (FR-104).
	if _, err := ws.SaveToolboxVersion("tbx-lean", nil, nil, ToolboxProvenanceUser, "tester"); err != nil {
		t.Fatalf("SaveToolboxVersion() error = %v", err)
	}

	result := PreflightGoalToolbox(ws, nil, 6, false, DefaultFocusThresholds())
	if !result.OK {
		t.Fatalf("expected a still-resolvable pin to pass, got %+v", result)
	}
	if result.ToolboxVersion != 1 {
		t.Fatalf("expected the pin to stay on version 1, got %d", result.ToolboxVersion)
	}
	if result.UsedCurrentAtStart {
		t.Fatalf("expected a pinned policy not to resolve current-at-start")
	}
}

func TestPreflightGoalToolbox_StopsOnMissingArchivedAndDrifted(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(ws *Workspace)
		wantSub string
	}{
		{
			name:    "deleted toolbox",
			mutate:  func(ws *Workspace) { ws.Toolboxes = nil },
			wantSub: "no longer exists",
		},
		{
			name: "archived toolbox",
			mutate: func(ws *Workspace) {
				if _, err := ws.SetToolboxStatus("tbx-lean", ToolboxStatusArchived); err != nil {
					panic(err)
				}
			},
			wantSub: "archived",
		},
		{
			name: "removed connection",
			mutate: func(ws *Workspace) {
				for i := range ws.MCPBindings {
					if ws.MCPBindings[i].ID == "mb-web" {
						ws.MCPBindings[i].Enabled = false
					}
				}
			},
			wantSub: "needs connection",
		},
		{
			name:    "version that no longer exists",
			mutate:  func(ws *Workspace) { ws.GoalToolboxPolicy.ToolboxVersion = 99 },
			wantSub: "no longer available",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ws := newRecommendationFixture(t)
			ws.GoalToolboxPolicy = &GoalToolboxPolicy{
				EntryAgentInstanceID: "inst-1",
				ToolboxID:            "tbx-lean",
				ToolboxVersion:       1,
			}
			testCase.mutate(ws)

			result := PreflightGoalToolbox(ws, nil, 6, false, DefaultFocusThresholds())
			if result.OK {
				t.Fatalf("expected the preflight to stop the run, got %+v", result)
			}
			if !strings.Contains(strings.ToLower(result.Reason), testCase.wantSub) {
				t.Fatalf("expected the reason to mention %q, got %q", testCase.wantSub, result.Reason)
			}

			// The stop is recorded so the Goal surface can explain it (FR-105).
			MarkGoalNeedsAttention(ws, result.Reason)
			if !ws.GoalToolboxPolicy.NeedsAttention || ws.GoalToolboxPolicy.NeedsAttentionReason == "" {
				t.Fatalf("expected the goal to be marked Needs attention")
			}
			ClearGoalNeedsAttention(ws)
			if ws.GoalToolboxPolicy.NeedsAttention {
				t.Fatalf("expected the flag to clear once resolved")
			}
		})
	}
}

func TestPreflightGoalToolbox_CurrentAtStartResolvesTheAssignment(t *testing.T) {
	ws := newRecommendationFixture(t)
	if _, err := ws.SetToolboxAssignment(AgentToolboxAssignment{AgentInstanceID: "inst-1", ToolboxID: "tbx-lean"}); err != nil {
		t.Fatalf("SetToolboxAssignment() error = %v", err)
	}
	ws.GoalToolboxPolicy = &GoalToolboxPolicy{
		EntryAgentInstanceID: "inst-1",
		UseCurrentAtStart:    true,
	}

	result := PreflightGoalToolbox(ws, nil, 6, false, DefaultFocusThresholds())
	if !result.OK || !result.UsedCurrentAtStart {
		t.Fatalf("expected current-at-start to resolve the assignment, got %+v", result)
	}
	if result.ToolboxID != "tbx-lean" {
		t.Fatalf("expected the current assignment to be used, got %q", result.ToolboxID)
	}
}

// A goal with no policy at all keeps working exactly as it did before
// Toolboxes existed.
func TestPreflightGoalToolbox_NoPolicyPasses(t *testing.T) {
	ws := newRecommendationFixture(t)

	if result := PreflightGoalToolbox(ws, nil, 6, false, DefaultFocusThresholds()); !result.OK {
		t.Fatalf("expected a goal with no toolbox policy to pass, got %+v", result)
	}
}
