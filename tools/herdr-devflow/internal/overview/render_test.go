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
