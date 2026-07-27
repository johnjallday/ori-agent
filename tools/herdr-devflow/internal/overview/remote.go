package overview

import (
	"sort"
	"strconv"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// MatchRemote attaches remote delivery evidence to one feature.
//
// Matching is exact: only a pull request whose head branch is precisely
// feature/<slug> counts as this feature's delivery. A branch that merely
// contains or resembles the slug is somebody else's work, and attributing it
// here would report a feature as shipped on the strength of a coincidence.
func MatchRemote(feature *Feature, pulls []github.PullRequest, baseline string, observedAt time.Time) []Finding {
	if baseline == "" {
		baseline = defaultBaseline
	}
	var matches []github.PullRequest
	var unexpectedBase []github.PullRequest
	for _, pull := range pulls {
		// The slug identifies the feature; the branch prefix only records
		// intent. Requiring `feature/` here would miss delivered work, since
		// pull requests routinely land from `fix/` and `feat/` branches.
		slug, ok := worktree.SlugFromBranch(pull.Head)
		if !ok || slug != feature.Slug {
			continue
		}
		if pull.Base != baseline {
			unexpectedBase = append(unexpectedBase, pull)
			continue
		}
		matches = append(matches, pull)
	}

	feature.Remote = Remote{Availability: AvailabilityAvailable, ObservedAt: observedAt}
	var findings []Finding
	raise := func(code FindingCode, severity Severity, message, detail string) {
		findings = append(findings, Finding{
			Code: code, Severity: severity, Feature: feature.Slug,
			Source: SourceGitHub, Message: message, Detail: detail,
		})
	}

	for _, pull := range unexpectedBase {
		raise(FindingPRUnexpectedBase, SeverityWarning,
			"A pull request for this branch targets an unexpected base.",
			"#"+strconv.Itoa(pull.Number)+" targets "+pull.Base+", not "+baseline+".")
	}

	if len(matches) == 0 {
		feature.Remote.Availability = AvailabilityAbsent
		if len(unexpectedBase) > 0 {
			// The branch has remote work, just not toward the baseline.
			feature.Remote.Candidates = convertAll(unexpectedBase)
		}
		return findings
	}

	// Prefer the open pull request when history also holds merged or closed
	// ones for the same branch, which is normal after a reverted merge.
	open := filter(matches, func(pull github.PullRequest) bool { return pull.State == "open" })
	switch {
	case len(open) > 1:
		feature.Remote.Candidates = convertAll(open)
		feature.Remote.Detail = "more than one open pull request matches this branch"
		raise(FindingPRAmbiguous, SeverityError,
			"More than one open pull request matches this branch; none was chosen.",
			"Matching pull requests: "+numbers(open))
		return findings
	case len(open) == 1:
		selected := convert(open[0])
		feature.Remote.PullRequest = &selected
	default:
		selected := convert(mostRecent(matches))
		feature.Remote.PullRequest = &selected
	}

	pull := feature.Remote.PullRequest
	if len(matches) > 1 {
		feature.Remote.Candidates = convertAll(matches)
	}
	switch {
	case pull.State == "closed" && !pull.Merged:
		// A closed, unmerged pull request is visible history, not delivery.
		raise(FindingPRClosedUnmerged, SeverityWarning,
			"The pull request for this branch was closed without merging.",
			"#"+strconv.Itoa(pull.Number)+" is closed and unmerged.")
	case pull.State == "open" && pull.Checks == ChecksFailing:
		// Only an open pull request can act on failing checks. A merged one
		// reports whatever its checks did at merge time, and re-raising that
		// as an error would permanently flag delivered work.
		raise(FindingChecksFailing, SeverityError,
			"Required checks are failing on this feature's open pull request.",
			"#"+strconv.Itoa(pull.Number)+" reports failing checks.")
	}
	return findings
}

func filter(pulls []github.PullRequest, keep func(github.PullRequest) bool) []github.PullRequest {
	var kept []github.PullRequest
	for _, pull := range pulls {
		if keep(pull) {
			kept = append(kept, pull)
		}
	}
	return kept
}

// mostRecent picks the latest pull request by merge time, falling back to
// update time, so a re-opened-and-merged branch reports its real delivery.
func mostRecent(pulls []github.PullRequest) github.PullRequest {
	best := pulls[0]
	for _, pull := range pulls[1:] {
		if stamp(pull).After(stamp(best)) {
			best = pull
		}
	}
	return best
}

func stamp(pull github.PullRequest) time.Time {
	if !pull.MergedAt.IsZero() {
		return pull.MergedAt
	}
	return pull.UpdatedAt
}

func numbers(pulls []github.PullRequest) string {
	values := make([]string, 0, len(pulls))
	for _, pull := range pulls {
		values = append(values, "#"+strconv.Itoa(pull.Number))
	}
	sort.Strings(values)
	return joinBounded(values)
}

func convertAll(pulls []github.PullRequest) []PullRequest {
	converted := make([]PullRequest, 0, len(pulls))
	for _, pull := range pulls {
		converted = append(converted, convert(pull))
	}
	return converted
}

func convert(pull github.PullRequest) PullRequest {
	return PullRequest{
		Number:    pull.Number,
		URL:       pull.URL,
		Head:      pull.Head,
		Base:      pull.Base,
		Draft:     pull.Draft,
		State:     pull.State,
		Merged:    pull.Merged,
		Checks:    CheckState(pull.Checks),
		UpdatedAt: pull.UpdatedAt,
		MergedAt:  pull.MergedAt,
	}
}

// RemoteSlugCandidates derives feature slugs from remote branches, so a
// feature that exists only as a pull request still appears on the board.
func RemoteSlugCandidates(pulls []github.PullRequest) []string {
	seen := map[string]struct{}{}
	var slugs []string
	for _, pull := range pulls {
		slug, ok := worktree.SlugFromBranch(pull.Head)
		if !ok {
			continue
		}
		if _, exists := seen[slug]; exists {
			continue
		}
		seen[slug] = struct{}{}
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}
