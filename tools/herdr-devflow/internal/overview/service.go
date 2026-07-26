package overview

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

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
)

// Config configures one Service. Every collector is injectable so the service
// can be exercised without a real repository, Git, GitHub, or Herdr.
type Config struct {
	// RepoRoot is any checkout of the repository to inventory.
	RepoRoot string
	// Baseline is the integration branch, defaulting to dev.
	Baseline string
	// Git runs read-only Git commands. Defaults to the real Git.
	Git worktree.Runner
	// Now supplies the observation clock. Defaults to time.Now.
	Now func() time.Time
}

// Service collects the shared read-only snapshot. It is the single collector
// behind `wt status`, `wt herd status`, the JSON contract, and the Herdr board.
//
// Collection is local-first: planning, backlog, worktree, and Git evidence are
// gathered without a network. Remote enrichment is layered on top and is
// required before a snapshot may call itself complete.
type Service struct {
	config Config
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
	return &Service{config: config}
}

// Collect gathers one complete observation. It mutates nothing: every call
// here reads files or runs read-only Git plumbing.
//
// A collector failure degrades its own evidence and is recorded as an
// unavailable Source; it never aborts the snapshot, because a partial board
// that says what it does not know is more useful than no board at all.
func (s *Service) Collect(ctx context.Context) (Snapshot, error) {
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

	backlog := planning.ReadBacklog(filepath.Join(planningRoot, "BACKLOG.md"), now)
	snapshot.Sources = append(snapshot.Sources, Source{
		Kind:         SourceBacklog,
		Availability: planAvailability(backlog.State),
		ObservedAt:   now,
		Detail:       backlog.Detail,
	})

	features, findings := BuildInventory(Input{
		DevPlanning:      devPlanning,
		Backlog:          backlog,
		Checkouts:        checkouts,
		LookupActivePlan: activePlanLookup(now),
		ReadPlanProgress: tasklist.ReadPlan,
		Now:              now,
	})

	s.inspectGit(ctx, features, now)
	snapshot.Sources = append(snapshot.Sources, Source{
		Kind: SourceGit, Availability: AvailabilityAvailable, ObservedAt: now,
	})

	// A fresh authenticated GitHub query is required before any snapshot may
	// call itself complete. This slice has no GitHub collector yet, so the
	// source is recorded as unavailable and the snapshot stays incomplete
	// rather than presenting local guesses as settled facts.
	snapshot.Sources = append(snapshot.Sources, Source{
		Kind:         SourceGitHub,
		Availability: AvailabilityUnavailable,
		Required:     true,
		Detail:       "remote delivery status has not been queried",
	})
	snapshot.Findings = append(snapshot.Findings, Finding{
		Code:     FindingGitHubUnavailable,
		Severity: SeverityError,
		Source:   SourceGitHub,
		Message:  "Remote delivery status is unavailable, so review, merge, and cleanup phases cannot be confirmed.",
	})

	options := DeriveOptions{Baseline: s.config.Baseline, RemoteAvailable: false}
	for index := range features {
		features[index].Phase = DerivePhase(features[index], options)
		features[index].Findings = mergeFindings(findingsFor(findings, features[index].Slug), DeriveFindings(features[index], options))
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
	sortFindings(snapshot.Findings)
	snapshot.Features = features
	snapshot.Complete = requiredSourcesFresh(snapshot)
	return snapshot, nil
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
		git.Branch = facts.Branch
	}
	if facts.HeadAvailability == worktree.FactAvailable {
		git.HeadSHA = facts.Head
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
