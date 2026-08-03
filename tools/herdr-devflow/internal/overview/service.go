package overview

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/planning"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/tasklist"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

const (
	// defaultBaseline is the integration branch features target.
	defaultBaseline = "dev"
	// gitWorkers bounds concurrent Git inspection so a large repository
	// cannot spawn an unbounded number of child processes.
	gitWorkers = 6
	// gitTimeout bounds the Git inspection of one checkout.
	gitTimeout = 15 * time.Second
	// remoteWorkers bounds concurrent targeted GitHub lookups.
	remoteWorkers = 4
)

// RemoteCollector performs one fresh authenticated query for the repository's
// pull requests. It is an interface so the service can be exercised against a
// deterministic fixture with no network and no authenticated CLI.
type RemoteCollector interface {
	ListPullRequests(ctx context.Context, base string) (github.Result, error)
	// ListPullRequestsForHead resolves one exact branch the bulk listing did
	// not cover, which is how a repository with more pull requests than one
	// page still reports every feature's delivery.
	ListPullRequestsForHead(ctx context.Context, base, head string) (github.Result, error)
}

// Config configures one Service. Every collector is injectable so the service
// can be exercised without a real repository, Git, GitHub, or Herdr.
type Config struct {
	// RepoRoot is any checkout of the repository to inventory.
	RepoRoot string
	// Baseline is the integration branch, defaulting to dev.
	Baseline string
	// Git runs read-only Git commands. Defaults to the real Git.
	Git worktree.Runner
	// Remote queries GitHub. A nil collector means no remote query is even
	// attempted, which leaves every snapshot incomplete by design.
	Remote RemoteCollector
	// RemoteRefreshInterval is the minimum gap between remote queries while
	// watching. One-shot collection always queries fresh regardless.
	RemoteRefreshInterval time.Duration
	// Agents reports what Herdr can currently see. A nil collector degrades
	// every agent observation to unavailable without hiding anything else.
	Agents AgentCollector
	// Bridge loads the saved bridge records. Saved identity is only ever
	// presented as a record, never as a live observation.
	Bridge BridgeReader
	// ClaudeReadiness answers whether one exact native Claude session may be
	// run unattended. A nil func leaves every agent's Overnight eligibility
	// unverified, which is the honest answer when nothing checked.
	ClaudeReadiness ClaudeReadinessFunc
	// RunMembership reports which native sessions an Overnight Run has
	// enrolled, keyed by session. Nil means no run is being tracked, which is
	// distinct from a run that enrolled nobody.
	RunMembership RunMembershipFunc
	// Now supplies the observation clock. Defaults to time.Now.
	Now func() time.Time
}

// Service collects the shared read-only snapshot behind `wt status`, its JSON
// contract, and the Herdr board.
//
// Collection is local-first: planning, worktree, and Git evidence are gathered
// without a network. Remote enrichment is layered on top and is required before
// a snapshot may call itself complete.
type Service struct {
	config Config
	clock  *remoteClock
}

// NewService builds a Service, applying defaults for omitted collectors.
func NewService(config Config) *Service {
	if config.Baseline == "" {
		config.Baseline = defaultBaseline
	}
	if config.Git == nil {
		config.Git = worktree.GitRunner
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	service := &Service{config: config}
	if config.Remote != nil {
		service.clock = newRemoteClock(config.Remote, config.RemoteRefreshInterval)
	}
	return service
}

// Collect gathers one complete observation. It mutates nothing: every call
// here reads files or runs read-only Git plumbing.
//
// A collector failure degrades its own evidence and is recorded as an
// unavailable Source; it never aborts the snapshot, because a partial board
// that says what it does not know is more useful than no board at all.
func (s *Service) Collect(ctx context.Context) (Snapshot, error) {
	// A one-shot collection always performs a fresh query: a command a human
	// just typed must not answer from a cached remote result.
	return s.collect(ctx, true)
}

// collectRateLimited is the watch path: it reuses remote facts until the
// remote clock's interval has elapsed.
func (s *Service) collectRateLimited(ctx context.Context) (Snapshot, error) {
	return s.collect(ctx, false)
}

func (s *Service) collect(ctx context.Context, forceRemote bool) (Snapshot, error) {
	now := s.config.Now()
	snapshot := Snapshot{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   now,
		Repository:    Repository{Baseline: s.config.Baseline},
	}

	checkouts, err := worktree.ListCheckouts(ctx, s.config.RepoRoot, s.config.Baseline, s.config.Git, now)
	if err != nil {
		// Without the worktree inventory there is no feature list to render,
		// so this is the one collector whose failure is fatal.
		return snapshot, fmt.Errorf("inventory repository checkouts: %w", err)
	}
	snapshot.Repository.ID = checkouts.RepositoryID
	snapshot.Repository.GitCommonDir = checkouts.GitCommonDir
	snapshot.Repository.Root = checkouts.SourcePath
	snapshot.Sources = append(snapshot.Sources, Source{
		Kind: SourceWorktree, Availability: AvailabilityAvailable, ObservedAt: now,
	})

	// Planning lives in the baseline checkout: PRDs are written there before
	// `wt start` and ticked task lists are archived back there by `wt done`.
	planningRoot := checkouts.BaselinePath
	if planningRoot == "" {
		planningRoot = checkouts.SourcePath
	}
	if planningRoot == "" {
		planningRoot = s.config.RepoRoot
	}

	devPlanning := planning.Discover(filepath.Join(planningRoot, "tasks"), now)
	snapshot.Sources = append(snapshot.Sources, Source{
		Kind:         SourcePlanning,
		Availability: planAvailability(devPlanning.State),
		ObservedAt:   now,
		Detail:       devPlanning.Detail,
	})

	// There is no backlog source. The repository's backlog is GitHub Issues, and
	// an unselected Issue is not a feature: it has no plan, no branch, and no
	// worktree, so it would be a row describing nothing. `wt backlog` reads
	// them, and this board describes work that has actually been selected.

	// Herdr and the bridge are collected before the join so a feature known
	// only to the bridge still gets a row.
	agentEvidence := CollectAgents(ctx, s.config.Agents, s.config.Bridge, now)
	snapshot.Sources = append(snapshot.Sources, Source{
		Kind:         SourceHerdr,
		Availability: agentEvidence.Availability,
		ObservedAt:   now,
		Detail:       agentEvidence.Detail,
	})

	features, findings := BuildInventory(Input{
		DevPlanning:      devPlanning,
		Checkouts:        checkouts,
		BridgeSlugs:      BridgeSlugs(agentEvidence.Bridge),
		LookupActivePlan: activePlanLookup(now),
		ReadPlanProgress: tasklist.ReadPlan,
		Now:              now,
	})

	for index := range features {
		findings = append(findings, AttachAgents(&features[index], agentEvidence)...)
	}
	if agentEvidence.Availability != AvailabilityAvailable {
		snapshot.Findings = append(snapshot.Findings, Finding{
			Code:     FindingHerdrUnavailable,
			Severity: SeverityWarning,
			Source:   SourceHerdr,
			Message:  "Herdr is unavailable, so agent status and schedules could not be observed. Saved values are bridge records only.",
			Detail:   agentEvidence.Detail,
		})
	}

	s.inspectGit(ctx, features, now)
	snapshot.Sources = append(snapshot.Sources, Source{
		Kind: SourceGit, Availability: AvailabilityAvailable, ObservedAt: now,
	})

	// A fresh authenticated GitHub query is required before any snapshot may
	// call itself complete: every local phase is falsifiable by an open or
	// merged pull request, so a local-only board must not present its guesses
	// as settled facts.
	remote, remoteFindings := s.collectRemote(ctx, features, now, forceRemote)
	snapshot.Sources = append(snapshot.Sources, remote)
	snapshot.Findings = append(snapshot.Findings, remoteFindings...)

	// Stale remote facts are still real observations, so they may confirm a
	// phase; the snapshot separately reports itself as stale and incomplete.
	remoteUsable := remote.Availability.OK() || remote.Availability == AvailabilityStale
	options := DeriveOptions{Baseline: s.config.Baseline, RemoteAvailable: remoteUsable}
	for index := range features {
		// Phase first: several findings compare themselves against the phase
		// that was ultimately chosen.
		features[index].Phase = DerivePhase(features[index], options)
		features[index].Findings = mergeFindings(
			features[index].Findings,
			findingsFor(findings, features[index].Slug),
			DeriveFindings(features[index], options),
		)
	}

	if baselineStale(features) {
		snapshot.Repository.BaselineStale = true
		snapshot.Findings = append(snapshot.Findings, Finding{
			Code:     FindingBaselineStale,
			Severity: SeverityWarning,
			Source:   SourceGit,
			Message:  "Local " + s.config.Baseline + " is behind its remote, so divergence counts understate how far behind features are.",
		})
	}

	SortFeatures(features)

	// The roster is derived after the features are ordered, so the flat
	// all-agent view and the feature-first view present the same agents in the
	// same sequence. Repository-level agents are appended here rather than
	// collected separately: one observation, two groupings.
	snapshot.Checkouts = BuildCheckouts(checkouts, agentEvidence)
	roster, rosterFindings := BuildRoster(features, checkouts, agentEvidence, s.config.ClaudeReadiness)
	attachRunMembership(features, roster, s.config.RunMembership)
	snapshot.Agents = roster
	snapshot.Findings = append(snapshot.Findings, rosterFindings...)

	sortFindings(snapshot.Findings)
	snapshot.Features = features
	snapshot.Complete = requiredSourcesFresh(snapshot)
	snapshot.Stale = anyStale(snapshot)
	if remote.Availability.OK() || remote.Availability == AvailabilityStale {
		snapshot.GitHubCheckedAt = remote.ObservedAt
	}
	return snapshot, nil
}

// anyStale reports whether any source is being reused past its refresh window.
func anyStale(snapshot Snapshot) bool {
	for _, source := range snapshot.Sources {
		if source.Availability == AvailabilityStale {
			return true
		}
	}
	return false
}

// collectRemote performs the one required network call and attaches its
// evidence to every feature. A failure degrades to an unavailable source with
// a sanitized reason; it never aborts the snapshot, because partial local
// facts plus an honest "remote unknown" beat no board at all.
func (s *Service) collectRemote(ctx context.Context, features []Feature, now time.Time, force bool) (Source, []Finding) {
	source := Source{Kind: SourceGitHub, Required: true, ObservedAt: now}
	if s.clock == nil {
		source.Availability = AvailabilityUnavailable
		source.Detail = "remote delivery status was not queried"
		return source, []Finding{githubUnavailable(source.Detail, "")}
	}

	outcome := s.clock.get(ctx, s.config.Baseline, now, force)
	result, err := outcome.result, outcome.err
	if err != nil && !outcome.stale {
		source.Availability = AvailabilityUnavailable
		detail, recovery := describeRemoteError(err)
		source.Detail = detail
		for index := range features {
			// Mark each row explicitly rather than leaving remote columns
			// looking merely empty.
			features[index].Remote = Remote{Availability: AvailabilityUnavailable, Detail: detail, ObservedAt: now}
		}
		return source, []Finding{githubUnavailable(detail, recovery)}
	}

	source.Availability = AvailabilityAvailable
	source.ObservedAt = result.ObservedAt
	if source.ObservedAt.IsZero() {
		source.ObservedAt = now
	}
	if outcome.stale {
		// Reused remote facts stay usable but must never be presented as
		// current: the board says so, and the snapshot is marked stale.
		source.Availability = AvailabilityStale
		if err != nil {
			detail, _ := describeRemoteError(err)
			source.Detail = "showing the last successful result; the latest query failed: " + detail
		} else {
			source.Detail = "showing the last successful result until the next refresh"
		}
	}

	pulls := result.PullRequests
	// The bulk listing is capped, so in a repository with a long merge history
	// an older pull request falls outside it. Rather than raising the cap
	// forever, resolve the stragglers with one small targeted query each.
	if result.Truncated {
		pulls = append(pulls, s.resolveMissingHeads(ctx, features, pulls)...)
	}

	// Remote findings belong to the feature they describe, not to the
	// repository footer: an unattributed "checks are failing" tells a reader
	// nothing about which feature to go and look at.
	var findings []Finding
	unresolved := 0
	for index := range features {
		raised := MatchRemote(&features[index], pulls, s.config.Baseline, source.ObservedAt)
		features[index].Findings = mergeFindings(features[index].Findings, raised)
		if features[index].Remote.PullRequest == nil && expectsPullRequest(features[index]) {
			unresolved++
		}
	}
	if result.Truncated && unresolved > 0 {
		findings = append(findings, Finding{
			Code:     FindingGitHubUnavailable,
			Severity: SeverityInfo,
			Source:   SourceGitHub,
			Message:  "Some delivered features could not be matched to a pull request within this tool's query limits.",
		})
	}
	return source, findings
}

// expectsPullRequest reports whether a feature should have remote delivery
// evidence, which decides whether it is worth one extra query when the bulk
// page was capped.
//
// It used to ask the backlog whether the feature was shipped. The replacement
// asks the same question of evidence that still exists: the worktree is gone
// and the archived plan is fully ticked — exactly what a completed feature
// leaves behind after `wt done`. Work that is merely planned, or still under
// way, is not chased: it legitimately has no pull request, and querying for
// each one would turn every board render into a burst of API calls.
func expectsPullRequest(feature Feature) bool {
	if feature.Git.WorktreePath != "" || feature.Plan.Copy != PlanCopyDev {
		return false
	}
	progress := feature.Plan.Progress
	return progress.Availability.OK() && progress.SubtasksTotal > 0 &&
		progress.SubtasksCompleted == progress.SubtasksTotal
}

// resolveMissingHeads runs one targeted query per feature the bulk listing
// missed, bounded in both count and concurrency so a large repository cannot
// turn one board render into a burst of API calls.
func (s *Service) resolveMissingHeads(ctx context.Context, features []Feature, pulls []github.PullRequest) []github.PullRequest {
	covered := map[string]struct{}{}
	for _, pull := range pulls {
		if slug, ok := worktree.SlugFromBranch(pull.Head); ok {
			covered[slug] = struct{}{}
		}
	}

	var heads []string
	for _, feature := range features {
		if _, found := covered[feature.Slug]; found || !expectsPullRequest(feature) {
			continue
		}
		if len(heads) >= github.MaxTargetedLookups {
			break
		}
		heads = append(heads, worktree.FeatureBranchPrefix+feature.Slug)
	}
	if len(heads) == 0 {
		return nil
	}

	var (
		mu       sync.Mutex
		resolved []github.PullRequest
		wait     sync.WaitGroup
	)
	gate := make(chan struct{}, remoteWorkers)
	for _, head := range heads {
		wait.Add(1)
		go func(head string) {
			defer wait.Done()
			gate <- struct{}{}
			defer func() { <-gate }()

			// A targeted lookup is best-effort: failing to resolve one older
			// pull request must not degrade the whole snapshot.
			found, err := s.config.Remote.ListPullRequestsForHead(ctx, s.config.Baseline, head)
			if err != nil {
				return
			}
			mu.Lock()
			resolved = append(resolved, found.PullRequests...)
			mu.Unlock()
		}(head)
	}
	wait.Wait()
	return resolved
}

func githubUnavailable(detail, recovery string) Finding {
	message := "Remote delivery status is unavailable, so review, merge, and cleanup phases cannot be confirmed."
	if recovery != "" {
		message += " Recovery: " + recovery + "."
	}
	return Finding{
		Code:     FindingGitHubUnavailable,
		Severity: SeverityError,
		Source:   SourceGitHub,
		Message:  message,
		Detail:   detail,
	}
}

// describeRemoteError returns a sanitized reason and its recovery command. The
// underlying error is never rendered directly: `gh` failures routinely echo
// tokens and request bodies.
func describeRemoteError(err error) (detail, recovery string) {
	var remoteErr *github.Error
	if errors.As(err, &remoteErr) {
		return remoteErr.Detail, remoteErr.Recovery()
	}
	return "the GitHub query failed", "run: gh auth status"
}

// activePlanLookup reads the planning copy inside a feature's own worktree.
func activePlanLookup(now time.Time) func(string, string) (planning.Feature, error) {
	return func(worktreePath, slug string) (planning.Feature, error) {
		return planning.Lookup(filepath.Join(worktreePath, "tasks"), slug, now)
	}
}

// inspectGit fills in local Git facts for every feature that has a checkout,
// with bounded concurrency and a per-checkout timeout.
func (s *Service) inspectGit(ctx context.Context, features []Feature, now time.Time) {
	type job struct{ index int }
	jobs := make(chan job)
	var wait sync.WaitGroup

	workers := gitWorkers
	if len(features) < workers {
		workers = len(features)
	}
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for next := range jobs {
				feature := &features[next.index]
				inspectCtx, cancel := context.WithTimeout(ctx, gitTimeout)
				facts := worktree.InspectFacts(inspectCtx, s.config.Git, feature.Git.WorktreePath, s.config.Baseline)
				cancel()
				applyFacts(feature, facts, now)
			}
		}()
	}
	for index := range features {
		if features[index].Git.WorktreePath == "" {
			features[index].Git.Availability = AvailabilityAbsent
			continue
		}
		jobs <- job{index: index}
	}
	close(jobs)
	wait.Wait()
}

// applyFacts maps the Git package's per-fact availability onto the read model,
// preserving the independence of each fact.
func applyFacts(feature *Feature, facts worktree.Facts, now time.Time) {
	git := &feature.Git
	git.ObservedAt = now
	git.Detail = facts.Detail

	if facts.BranchAvailability == worktree.FactAvailable {
		git.Branch = planning.Sanitize(facts.Branch, 200)
	}
	if facts.HeadAvailability == worktree.FactAvailable {
		git.HeadSHA = planning.Sanitize(facts.Head, 64)
	}
	git.DirtyAvailability = factAvailability(facts.DirtyAvailability)
	if git.DirtyAvailability.OK() {
		git.Dirty = facts.Dirty
	}
	git.DivergenceAvailability = factAvailability(facts.DivergenceAvailability)
	if git.DivergenceAvailability.OK() {
		git.Ahead, git.Behind = facts.Ahead, facts.Behind
	}
	if factAvailability(facts.BaselineStaleAvailability).OK() {
		git.BaselineStale = facts.BaselineStale
	}

	// The checkout itself is available whenever any fact could be read; only a
	// total failure makes the whole Git row unavailable.
	if facts.BranchAvailability == worktree.FactAvailable ||
		facts.HeadAvailability == worktree.FactAvailable ||
		facts.DirtyAvailability == worktree.FactAvailable {
		git.Availability = AvailabilityAvailable
		return
	}
	git.Availability = AvailabilityUnavailable
}

func factAvailability(state worktree.FactAvailability) Availability {
	if state == worktree.FactAvailable {
		return AvailabilityAvailable
	}
	return AvailabilityUnavailable
}

func baselineStale(features []Feature) bool {
	for _, feature := range features {
		if feature.Git.BaselineStale {
			return true
		}
	}
	return false
}

func findingsFor(findings []Finding, slug string) []Finding {
	var matched []Finding
	for _, finding := range findings {
		if finding.Feature == slug {
			matched = append(matched, finding)
		}
	}
	return matched
}

// mergeFindings concatenates finding sets and restores the shared severity
// order. Findings are only ever added; none is dropped or cleared.
func mergeFindings(sets ...[]Finding) []Finding {
	var merged []Finding
	for _, set := range sets {
		merged = append(merged, set...)
	}
	sortFindings(merged)
	return merged
}

// requiredSourcesFresh reports whether every required source was observed
// successfully in this collection.
func requiredSourcesFresh(snapshot Snapshot) bool {
	for _, source := range snapshot.Sources {
		if source.Required && !source.Availability.OK() {
			return false
		}
	}
	return true
}
