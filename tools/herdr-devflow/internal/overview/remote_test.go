package overview

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
)

func pull(number int, head, state string, mutate ...func(*github.PullRequest)) github.PullRequest {
	built := github.PullRequest{
		Number:    number,
		URL:       "https://github.com/o/r/pull/" + string(rune('0'+number%10)),
		Head:      head,
		Base:      "dev",
		State:     state,
		Merged:    state == "merged",
		Checks:    github.ChecksPassing,
		UpdatedAt: observed,
	}
	if built.Merged {
		built.MergedAt = observed
	}
	for _, apply := range mutate {
		apply(&built)
	}
	return built
}

func matchOne(t *testing.T, slug string, pulls ...github.PullRequest) (Feature, []Finding) {
	t.Helper()
	row := feature(slug)
	findings := MatchRemote(&row, pulls, "dev", observed)
	return row, findings
}

func TestMatchRemoteRequiresAnExactSlug(t *testing.T) {
	// A branch that merely contains the slug belongs to somebody else.
	row, _ := matchOne(t, "backlog",
		pull(1, "feature/workspace-backlog", "open"),
		pull(2, "feature/backlog-v2", "open"),
	)
	if row.Remote.PullRequest != nil {
		t.Fatalf("a near-miss branch was attributed: %+v", row.Remote.PullRequest)
	}
	if row.Remote.Availability != AvailabilityAbsent {
		t.Fatalf("availability = %q, want absent", row.Remote.Availability)
	}
}

func TestMatchRemoteAcceptsEveryWorkBranchPrefix(t *testing.T) {
	// PRs have landed from fix/ and feat/ branches; the slug identifies the
	// feature, the prefix only records intent.
	for _, head := range []string{"feature/x", "feat/x", "fix/x", "refactor/x", "docs/x", "test/x", "chore/x"} {
		row, _ := matchOne(t, "x", pull(7, head, "open"))
		if row.Remote.PullRequest == nil {
			t.Fatalf("head %q was not matched", head)
		}
	}
}

func TestMatchRemoteIgnoresPullRequestsForOtherBases(t *testing.T) {
	row, findings := matchOne(t, "x", pull(9, "feature/x", "open", func(p *github.PullRequest) {
		p.Base = "main"
	}))
	if row.Remote.PullRequest != nil {
		t.Fatal("a pull request against the wrong base was treated as delivery")
	}
	if _, ok := findingFor(findings, FindingPRUnexpectedBase); !ok {
		t.Fatalf("findings = %v, want an unexpected-base finding", findings)
	}
	if len(row.Remote.Candidates) != 1 {
		t.Fatal("the unexpected-base pull request was hidden entirely")
	}
}

func TestMatchRemoteReportsMultipleOpenPullRequests(t *testing.T) {
	row, findings := matchOne(t, "x",
		pull(11, "feature/x", "open"),
		pull(12, "feature/x", "open"),
	)
	finding, ok := findingFor(findings, FindingPRAmbiguous)
	if !ok || finding.Severity != SeverityError {
		t.Fatalf("finding = %+v, ok=%v, want an ambiguity error", finding, ok)
	}
	if row.Remote.PullRequest != nil {
		t.Fatal("one of several open pull requests was silently chosen")
	}
	if len(row.Remote.Candidates) != 2 {
		t.Fatalf("candidates = %d, want both preserved", len(row.Remote.Candidates))
	}
}

func TestMatchRemotePrefersTheOpenPullRequestOverHistory(t *testing.T) {
	row, _ := matchOne(t, "x",
		pull(20, "feature/x", "merged"),
		pull(21, "feature/x", "open"),
	)
	if row.Remote.PullRequest == nil || row.Remote.PullRequest.Number != 21 {
		t.Fatalf("selected = %+v, want the open pull request", row.Remote.PullRequest)
	}
}

func TestMatchRemoteClosedUnmergedIsNotDelivery(t *testing.T) {
	row, findings := matchOne(t, "x", pull(30, "feature/x", "closed"))
	if row.Remote.PullRequest == nil {
		t.Fatal("a closed pull request was hidden instead of shown")
	}
	if row.Remote.PullRequest.Merged {
		t.Fatal("a closed-unmerged pull request was reported as merged")
	}
	if _, ok := findingFor(findings, FindingPRClosedUnmerged); !ok {
		t.Fatalf("findings = %v, want a closed-unmerged finding", findings)
	}
}

func TestMatchRemoteFailingChecksOnlyMatterWhileOpen(t *testing.T) {
	failing := func(p *github.PullRequest) { p.Checks = github.ChecksFailing }

	_, open := matchOne(t, "x", pull(40, "feature/x", "open", failing))
	if _, ok := findingFor(open, FindingChecksFailing); !ok {
		t.Fatal("failing checks on an open pull request raised nothing")
	}

	// Merged work reports whatever its checks did at merge time; re-raising
	// that would permanently flag delivered features.
	_, merged := matchOne(t, "x", pull(41, "feature/x", "merged", failing))
	if _, ok := findingFor(merged, FindingChecksFailing); ok {
		t.Fatal("a merged pull request was flagged for its historical checks")
	}
}

func TestDerivePhaseRemotePrecedence(t *testing.T) {
	remoteOptions := DeriveOptions{Baseline: "dev", RemoteAvailable: true}

	cases := []struct {
		name  string
		build func() Feature
		want  Phase
	}{
		{
			name: "open pull request is review",
			build: func() Feature {
				row := feature("x", withWorktree("/w/x"))
				selected := PullRequest{Number: 1, State: "open"}
				row.Remote = Remote{Availability: AvailabilityAvailable, PullRequest: &selected}
				return row
			},
			want: PhaseReview,
		},
		{
			name: "draft pull request is still review",
			build: func() Feature {
				row := feature("x", withWorktree("/w/x"))
				selected := PullRequest{Number: 1, State: "open", Draft: true}
				row.Remote = Remote{Availability: AvailabilityAvailable, PullRequest: &selected}
				return row
			},
			want: PhaseReview,
		},
		{
			name: "merged with a surviving worktree needs cleanup",
			build: func() Feature {
				row := feature("x", withWorktree("/w/x"))
				selected := PullRequest{Number: 1, State: "merged", Merged: true}
				row.Remote = Remote{Availability: AvailabilityAvailable, PullRequest: &selected}
				return row
			},
			want: PhaseMergedCleanup,
		},
		{
			name: "merged with an unticked archive needs cleanup",
			build: func() Feature {
				row := feature("x", withProgress(func(progress *PlanProgress) {
					progress.SubtasksTotal, progress.SubtasksCompleted = 136, 0
				}))
				selected := PullRequest{Number: 1, State: "merged", Merged: true}
				row.Remote = Remote{Availability: AvailabilityAvailable, PullRequest: &selected}
				return row
			},
			want: PhaseMergedCleanup,
		},
		{
			name: "merged and tidy is shipped",
			build: func() Feature {
				row := feature("x", withProgress(func(progress *PlanProgress) {
					progress.SubtasksTotal, progress.SubtasksCompleted = 103, 103
				}))
				selected := PullRequest{Number: 1, State: "merged", Merged: true}
				row.Remote = Remote{Availability: AvailabilityAvailable, PullRequest: &selected}
				return row
			},
			want: PhaseShipped,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := DerivePhase(testCase.build(), remoteOptions)
			if got.Phase != testCase.want {
				t.Fatalf("phase = %q, want %q", got.Phase, testCase.want)
			}
			if !got.Confirmed {
				t.Fatal("a phase backed by a fresh remote query was left unconfirmed")
			}
		})
	}
}

func TestDerivePhaseDeliveredEvidenceOverridesStaleLocalState(t *testing.T) {
	// The whole point of the remote query: a merged pull request settles the
	// question even when the task list was never ticked.
	row := feature("x", withProgress(func(progress *PlanProgress) {
		progress.SubtasksTotal, progress.SubtasksCompleted = 100, 3
	}))
	selected := PullRequest{Number: 1, State: "merged", Merged: true}
	row.Remote = Remote{Availability: AvailabilityAvailable, PullRequest: &selected}

	got := DerivePhase(row, DeriveOptions{Baseline: "dev", RemoteAvailable: true})
	if got.Phase != PhaseMergedCleanup {
		t.Fatalf("phase = %q, want merged_cleanup rather than an implementing guess", got.Phase)
	}
}

func TestRemoteSlugCandidatesSurfaceRemoteOnlyFeatures(t *testing.T) {
	slugs := RemoteSlugCandidates([]github.PullRequest{
		pull(1, "feature/remote-only", "open"),
		pull(2, "fix/also-remote", "merged"),
		pull(3, "not-a-work-branch", "open"),
		pull(4, "feature/remote-only", "merged"),
	})
	if len(slugs) != 2 || slugs[0] != "also-remote" || slugs[1] != "remote-only" {
		t.Fatalf("slugs = %v, want the two deduplicated work branches", slugs)
	}
}

func TestMatchRemoteStampsObservationTime(t *testing.T) {
	when := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	row := feature("x")
	MatchRemote(&row, []github.PullRequest{pull(1, "feature/x", "open")}, "dev", when)
	if !row.Remote.ObservedAt.Equal(when) {
		t.Fatalf("observed at %v, want %v", row.Remote.ObservedAt, when)
	}
}
