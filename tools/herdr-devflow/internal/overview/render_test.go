package overview

import (
	"strings"
	"testing"
	"time"
)

func renderToString(t *testing.T, snapshot Snapshot) string {
	t.Helper()
	var out strings.Builder
	if err := RenderCompact(&out, snapshot, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderCompact: %v", err)
	}
	return out.String()
}

func baseSnapshot(features ...Feature) Snapshot {
	return Snapshot{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Date(2026, 7, 25, 9, 30, 0, 0, time.UTC),
		Repository:    Repository{ID: "repo-1", Baseline: "dev"},
		Complete:      true,
		Features:      features,
		Sources:       []Source{{Kind: SourceGitHub, Availability: AvailabilityAvailable, Required: true}},
	}
}

func TestRenderCompactShowsEveryColumnAsText(t *testing.T) {
	row := feature("downloads-janitor", withWorktree("/w/downloads-janitor"),
		withPlan(AvailabilityAvailable, AvailabilityAvailable),
		withGit(func(git *GitState) {
			git.Availability = AvailabilityAvailable
			git.DivergenceAvailability = AvailabilityAvailable
			git.Ahead, git.Behind = 6, 4
			git.DirtyAvailability = AvailabilityAvailable
			git.Dirty = true
		}))
	row.Phase = PhaseState{Phase: PhaseImplementing, Confirmed: true}

	output := renderToString(t, baseSnapshot(row))
	for _, want := range []string{"FEATURE", "PHASE", "ATTENTION", "downloads-janitor", "Implementing", "+6/-4", "dirty", "ok"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "\x1b[") {
		t.Fatalf("no-color output contained escape sequences:\n%s", output)
	}
}

func TestRenderCompactMarksUnconfirmedPhases(t *testing.T) {
	row := feature("guessing", withWorktree("/w/guessing"))
	row.Phase = PhaseState{Phase: PhaseImplementing, Confirmed: false}

	output := renderToString(t, baseSnapshot(row))
	if !strings.Contains(output, "Implementing (unconfirmed)") {
		t.Fatalf("an unconfirmed phase was presented as settled:\n%s", output)
	}
}

func TestRenderCompactNeverEmitsTrailingWhitespace(t *testing.T) {
	active := feature("active", withWorktree("/w/active"))
	active.Phase = PhaseState{Phase: PhaseImplementing}
	shipped := feature("history")
	shipped.Phase = PhaseState{Phase: PhaseShipped}

	for _, line := range strings.Split(renderToString(t, baseSnapshot(active, shipped)), "\n") {
		if line != strings.TrimRight(line, " \t") {
			t.Fatalf("line has trailing whitespace: %q", line)
		}
	}
}

func TestRenderCompactDistinguishesEmptyFromDegradedCells(t *testing.T) {
	noWorktree := feature("planned", withPlan(AvailabilityAvailable, AvailabilityAvailable))
	noWorktree.Phase = PhaseState{Phase: PhaseReady, Confirmed: true}
	noWorktree.Git.Availability = AvailabilityAbsent

	brokenGit := feature("opaque", withWorktree("/w/opaque"))
	brokenGit.Phase = PhaseState{Phase: PhaseImplementing, Confirmed: true}
	brokenGit.Git.Availability = AvailabilityUnavailable

	output := renderToString(t, baseSnapshot(noWorktree, brokenGit))
	if !strings.Contains(output, "no worktree") {
		t.Fatalf("a genuinely absent worktree was not stated:\n%s", output)
	}
	if !strings.Contains(output, "unavailable") {
		t.Fatalf("an unreadable Git state was not distinguished from an absent one:\n%s", output)
	}
}

func TestRenderCompactStatesIncompletenessProminently(t *testing.T) {
	snapshot := baseSnapshot(feature("anything"))
	snapshot.Complete = false
	snapshot.Sources = []Source{{
		Kind: SourceGitHub, Availability: AvailabilityUnavailable, Required: true,
		Detail: "remote delivery status has not been queried",
	}}
	snapshot.Findings = []Finding{{
		Code: FindingGitHubUnavailable, Severity: SeverityError,
		Message: "Remote delivery status is unavailable.",
	}}

	output := renderToString(t, snapshot)
	if !strings.Contains(output, "INCOMPLETE") {
		t.Fatalf("an incomplete snapshot did not say so:\n%s", output)
	}
	if !strings.Contains(output, "remote delivery status has not been queried") {
		t.Fatalf("the unavailable required source was not explained:\n%s", output)
	}
	if !strings.Contains(output, "[error]") {
		t.Fatalf("severity was not rendered as text:\n%s", output)
	}
}

func TestRenderCompactEmptyRepositoryIsExplicit(t *testing.T) {
	output := renderToString(t, baseSnapshot())
	if !strings.Contains(output, "No features were found") {
		t.Fatalf("an empty repository rendered silently:\n%s", output)
	}
	if !strings.Contains(output, "Snapshot: complete") {
		t.Fatalf("the footer was dropped for an empty repository:\n%s", output)
	}
}

func TestRenderCompactCountsAdditionalFindings(t *testing.T) {
	row := feature("busy", withWorktree("/w/busy"))
	row.Phase = PhaseState{Phase: PhaseImplementing, Confirmed: true}
	row.Findings = []Finding{
		{Code: FindingBranchBehindBase, Severity: SeverityWarning},
		{Code: FindingWorktreeDirty, Severity: SeverityInfo},
		{Code: FindingBacklogDrift, Severity: SeverityInfo},
	}

	output := renderToString(t, baseSnapshot(row))
	if !strings.Contains(output, "warning: branch_behind_base (+2)") {
		t.Fatalf("additional findings were hidden without a count:\n%s", output)
	}
}

func TestRenderCompactPlanPlaceholderIsNotZeroProgress(t *testing.T) {
	// Progress parsing arrives in a later slice; until then the cell must be
	// an explicit placeholder, never "0/0", which would read as no work done.
	row := feature("unparsed", withPlan(AvailabilityAvailable, AvailabilityAvailable))
	row.Phase = PhaseState{Phase: PhaseReady, Confirmed: true}

	output := renderToString(t, baseSnapshot(row))
	if strings.Contains(output, "0/0") {
		t.Fatalf("an unparsed plan rendered as zero progress:\n%s", output)
	}
	if !strings.Contains(output, placeholderUnknown) {
		t.Fatalf("an unparsed plan had no placeholder:\n%s", output)
	}
}

func detailToString(t *testing.T, snapshot Snapshot, row Feature) string {
	t.Helper()
	var out strings.Builder
	if err := RenderDetail(&out, snapshot, row, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderDetail: %v", err)
	}
	return out.String()
}

func progressed(row *Feature) {
	row.Plan.Copy = PlanCopyActive
	row.Plan.PRDAvailability = AvailabilityAvailable
	row.Plan.TaskListAvailability = AvailabilityAvailable
	row.Plan.PRDPath = "/w/x/tasks/prd-x.md"
	row.Plan.TaskListPath = "/w/x/tasks/tasks-x.md"
	row.Plan.Progress = PlanProgress{
		Availability:                 AvailabilityAvailable,
		MilestonesTotal:              7,
		MilestonesCompleted:          4,
		SubtasksTotal:                118,
		SubtasksCompleted:            66,
		ActiveMilestone:              PlanItem{Ordinal: "5.0", Text: "Fifth group"},
		NextActionable:               PlanItem{Ordinal: "5.1", Text: "The next step"},
		DeliveryCheckpointsRemaining: 4,
		DeliveryCheckpoints:          []PlanItem{{Ordinal: "5.9", Text: "Commit: the group", Checkpoint: true}},
	}
}

func TestRenderCompactShowsHierarchicalProgress(t *testing.T) {
	row := feature("measured", withWorktree("/w/measured"))
	row.Phase = PhaseState{Phase: PhaseImplementing, Confirmed: true}
	progressed(&row)

	output := renderToString(t, baseSnapshot(row))
	if !strings.Contains(output, "4/7 milestones · 66/118 subtasks · next 5.1") {
		t.Fatalf("compact progress was not rendered:\n%s", output)
	}
}

func TestRenderCompactSaysImplementationCompleteInsteadOfANextItem(t *testing.T) {
	row := feature("delivering", withWorktree("/w/delivering"))
	row.Phase = PhaseState{Phase: PhaseImplementing, Confirmed: true}
	progressed(&row)
	row.Plan.Progress.ImplementationComplete = true
	row.Plan.Progress.NextActionable = PlanItem{}

	output := renderToString(t, baseSnapshot(row))
	if !strings.Contains(output, "delivery only (4 left)") {
		t.Fatalf("a delivery-only feature was not called out:\n%s", output)
	}
	if strings.Contains(output, "next 5.1") {
		t.Fatalf("a completed implementation still advertised a next step:\n%s", output)
	}
}

func TestRenderDetailShowsProvenanceAndFullText(t *testing.T) {
	row := feature("measured", withWorktree("/w/measured"), withBacklog(BacklogDoing))
	row.Title = "PRD: Measured"
	row.Sources = []SourceKind{SourcePlanning, SourceBacklog, SourceWorktree}
	row.Phase = PhaseState{Phase: PhaseImplementing, Confirmed: true, Reason: "a feature worktree exists on disk"}
	progressed(&row)
	row.Git.Availability = AvailabilityAvailable
	row.Git.DivergenceAvailability = AvailabilityAvailable
	row.Git.DirtyAvailability = AvailabilityAvailable
	row.Git.Ahead, row.Git.Behind = 6, 4
	row.Git.HeadSHA = "0123456789abcdef"
	row.Findings = []Finding{{
		Code: FindingBranchBehindBase, Severity: SeverityWarning,
		Message: "This branch is behind dev.", Detail: "4 commits behind, 6 ahead.",
	}}

	output := detailToString(t, baseSnapshot(row), row)
	for _, want := range []string{
		"measured — PRD: Measured",
		"a feature worktree exists on disk",
		"Evidence: planning, backlog, worktree",
		"Authoritative copy: active_worktree",
		"/w/x/tasks/prd-x.md",
		"Active milestone: 5.0 Fifth group",
		"Next: 5.1 The next step",
		"Delivery checkpoints in 5.0 (4 remaining across the whole plan)",
		"Branch: feature/measured",
		"[warning] branch_behind_base",
		"4 commits behind, 6 ahead.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("detail output missing %q:\n%s", want, output)
		}
	}
}

func TestRenderDetailStatesAnAbsenceOfFindings(t *testing.T) {
	row := feature("tidy", withWorktree("/w/tidy"))
	row.Phase = PhaseState{Phase: PhaseImplementing, Confirmed: true}

	output := detailToString(t, baseSnapshot(row), row)
	if !strings.Contains(output, "Findings: none") {
		t.Fatalf("a clean feature did not say so explicitly:\n%s", output)
	}
}

func TestRenderDetailExplainsAnUnparsedPlan(t *testing.T) {
	row := feature("broken", withWorktree("/w/broken"))
	row.Phase = PhaseState{Phase: PhaseImplementing, Confirmed: true}
	row.Plan.Copy = PlanCopyActive
	row.Plan.PRDAvailability = AvailabilityAvailable
	row.Plan.TaskListAvailability = AvailabilityAvailable
	row.Plan.Progress = PlanProgress{
		Availability: AvailabilityMalformed,
		ParseIssue:   "no numbered checklist items were found",
	}

	output := detailToString(t, baseSnapshot(row), row)
	if !strings.Contains(output, "no numbered checklist items were found") {
		t.Fatalf("the parse issue was not surfaced:\n%s", output)
	}
	if strings.Contains(output, "0/0") {
		t.Fatalf("an unparsed plan rendered as zero progress:\n%s", output)
	}
}

func agentRow(role, status string, binding BindingHealth, managed bool) Agent {
	return Agent{
		Role: role, Managed: managed, Kind: "claude",
		Status: AgentStatus(status), StatusAvailability: AvailabilityAvailable,
		Binding: binding,
	}
}

func TestAgentCellListsSeveralAgents(t *testing.T) {
	row := feature("busy", withWorktree("/w/busy"))
	row.Agents = []Agent{
		agentRow("builder", "idle", BindingExact, true),
		agentRow("", "working", BindingMissing, false),
	}
	row.Occupancy = 2

	cell := agentCell(row)
	for _, want := range []string{"builder idle", "unmanaged working"} {
		if !strings.Contains(cell, want) {
			t.Fatalf("cell = %q, want it to contain %q", cell, want)
		}
	}
}

func TestAgentCellDegradesToACountRatherThanDroppingAnAgent(t *testing.T) {
	// Truncating the list could hide the one drifted agent among healthy ones,
	// so an over-long cell reports the count and the weakest binding instead.
	row := feature("crowded", withWorktree("/w/crowded"))
	row.Agents = []Agent{
		agentRow("builder", "working", BindingExact, true),
		agentRow("reviewer", "idle", BindingExact, true),
		agentRow("tester", "idle", BindingPossibleDrift, true),
		agentRow("watcher", "blocked", BindingExact, true),
	}

	cell := agentCell(row)
	if width(cell) > maxAgentColumn {
		t.Fatalf("cell = %q exceeds the column bound", cell)
	}
	if !strings.Contains(cell, "4 agents") {
		t.Fatalf("cell = %q, want the agent count stated", cell)
	}
	if !strings.Contains(cell, "possible drift") {
		t.Fatalf("cell = %q, want the weakest binding surfaced, not hidden by truncation", cell)
	}
}

func TestAgentCellDistinguishesOccupiedFromEmpty(t *testing.T) {
	occupied := feature("occupied", withWorktree("/w/occupied"))
	occupied.Occupancy = 1
	if got := agentCell(occupied); !strings.Contains(got, "no agent") {
		t.Fatalf("cell = %q, want an occupied-but-agentless worktree stated", got)
	}

	empty := feature("empty", withWorktree("/w/empty"))
	if got := agentCell(empty); got != "none" {
		t.Fatalf("cell = %q, want none", got)
	}
}

func TestExpandedViewStatesOccupancyWithoutAgents(t *testing.T) {
	row := feature("occupied", withWorktree("/w/occupied"))
	row.Phase = PhaseState{Phase: PhaseImplementing, Confirmed: true}
	row.Occupancy = 2

	var out strings.Builder
	if err := RenderExpanded(&out, baseSnapshot(row), RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderExpanded: %v", err)
	}
	if !strings.Contains(out.String(), "2 pane(s) open") {
		t.Fatalf("expanded view hid the occupancy:\n%s", out.String())
	}
}

func TestDetailViewNamesTheMatchedWorktree(t *testing.T) {
	row := feature("traced", withWorktree("/w/traced"))
	row.Phase = PhaseState{Phase: PhaseImplementing, Confirmed: true}
	agent := agentRow("builder", "idle", BindingExact, true)
	agent.MatchedPath = "/w/traced"
	row.Agents = []Agent{agent}

	var out strings.Builder
	if err := RenderDetail(&out, baseSnapshot(row), row, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderDetail: %v", err)
	}
	if !strings.Contains(out.String(), "matched worktree: /w/traced") {
		t.Fatalf("detail view did not show the attribution evidence:\n%s", out.String())
	}
}
