package overview

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// richSnapshot is one generated snapshot exercising every column: progress,
// divergence, a pull request with checks, an agent with drift, and findings.
func richSnapshot(t *testing.T) Snapshot {
	t.Helper()

	building := feature("downloads-janitor", withWorktree("/w/downloads-janitor"))
	building.Title = "PRD: Downloads Janitor"
	building.Sources = []SourceKind{SourcePlanning, SourceWorktree, SourceHerdr}
	building.Phase = PhaseState{Phase: PhaseImplementing, Confirmed: true, Reason: "a feature worktree exists on disk"}
	progressed(&building)
	building.Git.Availability = AvailabilityAvailable
	building.Git.DivergenceAvailability = AvailabilityAvailable
	building.Git.DirtyAvailability = AvailabilityAvailable
	building.Git.Ahead, building.Git.Behind = 6, 4
	building.Git.Dirty = true
	building.Git.Branch = "feature/downloads-janitor"
	selected := PullRequest{Number: 248, State: "open", Head: "feature/downloads-janitor", Base: "dev", Checks: ChecksPassing}
	building.Remote = Remote{Availability: AvailabilityAvailable, PullRequest: &selected}
	building.Agents = []Agent{{
		Feature: "downloads-janitor", Role: "builder", Managed: true, Kind: "claude",
		Saved:              Identity{Workspace: "ws-1", Pane: "pane-1", Kind: "claude"},
		Live:               Identity{Workspace: "ws-1", Pane: "pane-1", Kind: "claude"},
		Status:             AgentIdle,
		StatusAvailability: AvailabilityAvailable,
		Binding:            BindingPossibleDrift,
		BindingDetail:      `name: saved "ori-builder", live (none)`,
	}}
	building.Findings = []Finding{{
		Code: FindingAgentDrift, Severity: SeverityWarning, Feature: "downloads-janitor",
		Role: "builder", Message: "A live agent matches this role only partially; the saved identity may be stale.",
	}}

	shipped := feature("herdr-devflow-bridge", withCompletedArchive())
	shipped.Phase = PhaseState{Phase: PhaseShipped, Confirmed: true, Reason: "the pull request merged"}
	shipped.Git.Availability = AvailabilityAbsent
	merged := PullRequest{Number: 258, State: "merged", Merged: true, Checks: ChecksPassing}
	shipped.Remote = Remote{Availability: AvailabilityAvailable, PullRequest: &merged}

	snapshot := baseSnapshot(building, shipped)
	snapshot.Sources = []Source{
		{Kind: SourceGitHub, Availability: AvailabilityAvailable, Required: true},
		{Kind: SourceHerdr, Availability: AvailabilityAvailable},
	}
	return snapshot
}

func renderAll(t *testing.T, snapshot Snapshot) (compact, expanded, detail string, payload map[string]any) {
	t.Helper()
	options := RenderOptions{NoColor: true}

	var compactOut, expandedOut, detailOut strings.Builder
	if err := RenderCompact(&compactOut, snapshot, options); err != nil {
		t.Fatalf("RenderCompact: %v", err)
	}
	if err := RenderExpanded(&expandedOut, snapshot, options); err != nil {
		t.Fatalf("RenderExpanded: %v", err)
	}
	row, _ := snapshot.Feature("downloads-janitor")
	if err := RenderDetail(&detailOut, snapshot, row, options); err != nil {
		t.Fatalf("RenderDetail: %v", err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return compactOut.String(), expandedOut.String(), detailOut.String(), payload
}

func TestEverySurfaceReportsTheSameValues(t *testing.T) {
	snapshot := richSnapshot(t)
	compact, expanded, detail, payload := renderAll(t, snapshot)

	// Every value a reader could compare between two surfaces must agree. This
	// is the property the shared snapshot exists to guarantee.
	shared := []string{
		"downloads-janitor",
		"Implementing",
		"66/118 subtasks",
		"+6/-4",
		"#248",
	}
	for _, want := range shared {
		for surface, output := range map[string]string{"compact": compact, "expanded": expanded, "detail": detail} {
			if !strings.Contains(output, want) {
				t.Fatalf("%s surface is missing the shared value %q:\n%s", surface, want, output)
			}
		}
	}

	// The JSON payload must carry the same facts the human views printed.
	features := payload["features"].([]any)
	first := features[0].(map[string]any)
	progress := first["plan"].(map[string]any)["progress"].(map[string]any)
	if progress["subtasks_completed"] != float64(66) || progress["subtasks_total"] != float64(118) {
		t.Fatalf("JSON progress disagrees with the rendered views: %v", progress)
	}
	git := first["git"].(map[string]any)
	if git["ahead"] != float64(6) || git["behind"] != float64(4) {
		t.Fatalf("JSON divergence disagrees with the rendered views: %v", git)
	}
	remote := first["remote"].(map[string]any)["pull_request"].(map[string]any)
	if remote["number"] != float64(248) {
		t.Fatalf("JSON pull request disagrees with the rendered views: %v", remote)
	}
}

func TestEverySurfaceReportsTheSameAgentState(t *testing.T) {
	snapshot := richSnapshot(t)
	compact, expanded, detail, payload := renderAll(t, snapshot)

	for surface, output := range map[string]string{"compact": compact, "expanded": expanded, "detail": detail} {
		if !strings.Contains(output, "idle") {
			t.Fatalf("%s surface lost the observed agent status:\n%s", surface, output)
		}
		if !strings.Contains(output, "drift") {
			t.Fatalf("%s surface lost the binding health:\n%s", surface, output)
		}
	}

	agents := payload["features"].([]any)[0].(map[string]any)["agents"].([]any)
	agent := agents[0].(map[string]any)
	if agent["status"] != "idle" || agent["binding"] != string(BindingPossibleDrift) {
		t.Fatalf("JSON agent disagrees with the rendered views: %v", agent)
	}
}

func TestEverySurfaceReportsTheSameFindings(t *testing.T) {
	snapshot := richSnapshot(t)
	compact, expanded, detail, _ := renderAll(t, snapshot)

	// The compact table shows the code; the expanded and detail views show the
	// full message. No surface may silently drop a finding.
	if !strings.Contains(compact, string(FindingAgentDrift)) {
		t.Fatalf("compact surface dropped the finding:\n%s", compact)
	}
	for surface, output := range map[string]string{"expanded": expanded, "detail": detail} {
		if !strings.Contains(output, "agent_possible_drift") {
			t.Fatalf("%s surface dropped the finding:\n%s", surface, output)
		}
	}
}

func TestEverySurfaceSeparatesHistoryFromActiveWork(t *testing.T) {
	snapshot := richSnapshot(t)
	SortFeatures(snapshot.Features)
	compact, expanded, _, _ := renderAll(t, snapshot)

	activeIndex := strings.Index(compact, "downloads-janitor")
	historyIndex := strings.Index(compact, "herdr-devflow-bridge")
	if activeIndex < 0 || historyIndex < 0 || activeIndex > historyIndex {
		t.Fatalf("compact surface did not sort history last:\n%s", compact)
	}
	if !strings.Contains(expanded, "--- history ---") {
		t.Fatalf("expanded surface did not separate history:\n%s", expanded)
	}
}

func TestEverySurfaceHonoursNoColor(t *testing.T) {
	snapshot := richSnapshot(t)
	compact, expanded, detail, _ := renderAll(t, snapshot)
	for surface, output := range map[string]string{"compact": compact, "expanded": expanded, "detail": detail} {
		if strings.ContainsRune(output, 0x1b) {
			t.Fatalf("%s surface emitted an escape sequence under NoColor", surface)
		}
	}
}

func TestEverySurfaceStatesIncompleteness(t *testing.T) {
	snapshot := richSnapshot(t)
	snapshot.Complete = false
	snapshot.Stale = true
	snapshot.Sources = []Source{{
		Kind: SourceGitHub, Availability: AvailabilityStale, Required: true,
		Detail: "showing the last successful result until the next refresh",
	}}

	compact, expanded, detail, _ := renderAll(t, snapshot)
	for surface, output := range map[string]string{"compact": compact, "expanded": expanded, "detail": detail} {
		if !strings.Contains(output, "INCOMPLETE") {
			t.Fatalf("%s surface hid the snapshot's incompleteness:\n%s", surface, output)
		}
		if !strings.Contains(output, "stale") {
			t.Fatalf("%s surface hid the snapshot's staleness:\n%s", surface, output)
		}
	}
}

func TestExpandedSurfaceCountsManagedAgents(t *testing.T) {
	snapshot := richSnapshot(t)
	_, expanded, _, _ := renderAll(t, snapshot)
	want := "1 agent(s), 1 managed"
	if !strings.Contains(expanded, want) {
		t.Fatalf("expanded surface = %q, want it to state %q", expanded, want)
	}
	if !strings.Contains(expanded, strconv.Itoa(len(snapshot.Features))+" feature(s)") {
		t.Fatalf("expanded surface did not state the feature count:\n%s", expanded)
	}
}

// multiAgentSnapshot has one feature running two agents with different
// statuses and binding health — the case a single-agent model would flatten.
func multiAgentSnapshot(t *testing.T) Snapshot {
	t.Helper()
	row := feature("busy-feature", withWorktree("/w/busy-feature"),
		withPlan(AvailabilityAvailable, AvailabilityAvailable))
	row.Phase = PhaseState{Phase: PhaseImplementing, Confirmed: true}
	row.Occupancy = 3
	row.Agents = []Agent{
		{
			Feature: "busy-feature", Role: "builder", Managed: true, Kind: "claude",
			Status: AgentWorking, StatusAvailability: AvailabilityAvailable,
			Binding: BindingExact, MatchedPath: "/w/busy-feature",
		},
		{
			Feature: "busy-feature", Managed: false, Kind: "codex",
			Status: AgentIdle, StatusAvailability: AvailabilityAvailable,
			Binding: BindingMissing, MatchedPath: "/w/busy-feature",
			BindingDetail: "this agent has no bridge role for /w/busy-feature",
		},
	}
	snapshot := baseSnapshot(row)
	snapshot.Sources = []Source{{Kind: SourceGitHub, Availability: AvailabilityAvailable, Required: true}}
	return snapshot
}

func TestEverySurfaceReportsBothAgentsIndependently(t *testing.T) {
	snapshot := multiAgentSnapshot(t)
	row, _ := snapshot.Feature("busy-feature")

	var compact, expanded, detail strings.Builder
	if err := RenderCompact(&compact, snapshot, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderCompact: %v", err)
	}
	if err := RenderExpanded(&expanded, snapshot, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderExpanded: %v", err)
	}
	if err := RenderDetail(&detail, snapshot, row, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderDetail: %v", err)
	}

	// Both statuses must survive on every surface: a working builder beside an
	// idle unmanaged agent is two facts, not one.
	for name, output := range map[string]string{
		"compact": compact.String(), "expanded": expanded.String(), "detail": detail.String(),
	} {
		if !strings.Contains(output, "working") {
			t.Fatalf("%s surface lost the working agent:\n%s", name, output)
		}
		if !strings.Contains(output, "idle") {
			t.Fatalf("%s surface lost the idle agent:\n%s", name, output)
		}
	}

	// The expanded and detail views name both roles explicitly.
	for name, output := range map[string]string{"expanded": expanded.String(), "detail": detail.String()} {
		if !strings.Contains(output, "builder") || !strings.Contains(output, "unmanaged") {
			t.Fatalf("%s surface collapsed the two agents:\n%s", name, output)
		}
	}
}

func TestJSONCarriesEveryAgentAndOccupancy(t *testing.T) {
	encoded, err := json.Marshal(multiAgentSnapshot(t))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	row, _ := decoded.Feature("busy-feature")

	if len(row.Agents) != 2 {
		t.Fatalf("JSON agents = %d, want both", len(row.Agents))
	}
	if row.Occupancy != 3 {
		t.Fatalf("JSON occupancy = %d, want 3", row.Occupancy)
	}
	if row.Agents[0].Status != AgentWorking || row.Agents[1].Status != AgentIdle {
		t.Fatalf("JSON collapsed the per-agent statuses: %+v", row.Agents)
	}
	if row.Agents[0].MatchedPath == "" {
		t.Fatalf("JSON dropped the attribution evidence: %+v", row.Agents[0])
	}
}
