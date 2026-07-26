package overview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// fakeRemote is a deterministic RemoteCollector. It counts calls so tests can
// assert how often the network would have been touched.
type fakeRemote struct {
	mu      sync.Mutex
	calls   int
	result  github.Result
	err     error
	errFrom int
}

func (f *fakeRemote) ListPullRequests(context.Context, string) (github.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil && f.calls >= f.errFrom {
		return github.Result{}, f.err
	}
	return f.result, nil
}

func (f *fakeRemote) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// repoFixture lays out a dev checkout with planning artifacts so the service
// can run end to end without touching the real repository.
func repoFixture(t *testing.T) (root string, run worktree.Runner) {
	t.Helper()
	root = t.TempDir()
	dev := filepath.Join(root, "ori-agent-dev")
	if err := os.MkdirAll(filepath.Join(dev, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dev, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, contents string) {
		if err := os.WriteFile(filepath.Join(dev, "tasks", name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("prd-sample-feature.md", "# PRD: Sample Feature\n")
	write("tasks-sample-feature.md", "- [x] 1.0 Done\n  - [x] 1.1 Done\n- [ ] 2.0 Live\n  - [ ] 2.1 Next\n")

	listing := "worktree " + dev + "\nHEAD aaa\nbranch refs/heads/dev\n"
	run = func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "worktree" && args[1] == "list" {
			return listing, nil
		}
		return "", nil
	}
	return root, run
}

func newTestService(t *testing.T, remote RemoteCollector, mutate ...func(*Config)) *Service {
	t.Helper()
	root, run := repoFixture(t)
	config := Config{RepoRoot: root, Git: run, Remote: remote, Now: func() time.Time { return observed }}
	for _, apply := range mutate {
		apply(&config)
	}
	return NewService(config)
}

func TestCollectIsCompleteOnlyWithAFreshRemoteQuery(t *testing.T) {
	remote := &fakeRemote{result: github.Result{ObservedAt: observed}}
	snapshot, err := newTestService(t, remote).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !snapshot.Complete {
		t.Fatal("a snapshot with a successful remote query was reported incomplete")
	}
	if snapshot.GitHubCheckedAt.IsZero() {
		t.Fatal("the remote check time was not recorded")
	}
	for _, feature := range snapshot.Features {
		if !feature.Phase.Confirmed {
			t.Fatalf("feature %q was left unconfirmed despite a fresh query", feature.Slug)
		}
	}
}

func TestCollectWithoutAnyRemoteCollectorIsIncomplete(t *testing.T) {
	snapshot, err := newTestService(t, nil).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snapshot.Complete {
		t.Fatal("a snapshot with no remote query called itself complete")
	}
	source, _ := snapshot.Source(SourceGitHub)
	if !source.Required || source.Availability.OK() {
		t.Fatalf("github source = %+v, want a required, unavailable source", source)
	}
}

func TestCollectRemoteFailureKeepsLocalFactsAndExplainsItself(t *testing.T) {
	remote := &fakeRemote{
		errFrom: 1,
		err:     &github.Error{Kind: github.ErrorUnauthenticated, Detail: "the GitHub CLI is not authenticated for this repository"},
	}
	snapshot, err := newTestService(t, remote).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect returned a hard error instead of degrading: %v", err)
	}
	if snapshot.Complete {
		t.Fatal("a failed remote query produced a complete snapshot")
	}
	// Local evidence must survive: a partial board beats no board.
	found, ok := snapshot.Feature("sample-feature")
	if !ok {
		t.Fatalf("local features were dropped: %+v", snapshot.Features)
	}
	if !found.Plan.Progress.Availability.OK() || found.Plan.Progress.SubtasksTotal != 2 {
		t.Fatalf("local plan evidence was lost: %+v", found.Plan.Progress)
	}
	if found.Remote.Availability != AvailabilityUnavailable {
		t.Fatalf("remote availability = %q, want unavailable", found.Remote.Availability)
	}
	if found.Phase.Confirmed {
		t.Fatal("a phase was confirmed without remote evidence")
	}

	finding, ok := findingFor(snapshot.Findings, FindingGitHubUnavailable)
	if !ok {
		t.Fatalf("findings = %v, want github_unavailable", snapshot.Findings)
	}
	if finding.Severity != SeverityError {
		t.Fatalf("severity = %q, want error", finding.Severity)
	}
	if !strings.Contains(finding.Message, "gh auth login") {
		t.Fatalf("message = %q, want the recovery command stated", finding.Message)
	}
}

func TestCollectRemoteFailureNeverLeaksRawDiagnostics(t *testing.T) {
	secret := "ghp_TOKENVALUE000000000000000000000000000"
	remote := &fakeRemote{errFrom: 1, err: &github.Error{
		Kind:   github.ErrorUnauthenticated,
		Detail: "the GitHub CLI is not authenticated for this repository",
	}}
	snapshot, err := newTestService(t, remote).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, finding := range snapshot.Findings {
		if strings.Contains(finding.Message+finding.Detail, secret) {
			t.Fatalf("a secret reached a finding: %+v", finding)
		}
	}
}

func TestCollectAlwaysQueriesFreshEvenWithinTheRefreshInterval(t *testing.T) {
	// A command a human just typed must not answer from a cached result.
	remote := &fakeRemote{result: github.Result{ObservedAt: observed}}
	service := newTestService(t, remote, func(config *Config) {
		config.RemoteRefreshInterval = time.Hour
	})

	for range 3 {
		if _, err := service.Collect(context.Background()); err != nil {
			t.Fatalf("Collect: %v", err)
		}
	}
	if remote.count() != 3 {
		t.Fatalf("remote calls = %d, want one per explicit collection", remote.count())
	}
}

func TestWatchRateLimitsTheRemoteQuery(t *testing.T) {
	remote := &fakeRemote{result: github.Result{ObservedAt: observed}}
	service := newTestService(t, remote, func(config *Config) {
		config.RemoteRefreshInterval = time.Hour
	})

	ctx, cancel := context.WithCancel(context.Background())
	renders := 0
	err := service.Watch(ctx, time.Millisecond, func(Snapshot) {
		renders++
		if renders == 5 {
			cancel()
		}
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if renders < 5 {
		t.Fatalf("renders = %d, want the local clock to keep ticking", renders)
	}
	if remote.count() != 1 {
		t.Fatalf("remote calls = %d, want exactly one within the refresh interval", remote.count())
	}
}

func TestWatchReusesTheLastGoodRemoteResultAndMarksItStale(t *testing.T) {
	remote := &fakeRemote{
		result:  github.Result{ObservedAt: observed},
		errFrom: 2,
		err:     &github.Error{Kind: github.ErrorNetwork, Detail: "the GitHub query failed"},
	}
	service := newTestService(t, remote, func(config *Config) {
		// Below the floor on purpose: the clock must clamp it, which is what
		// keeps a watched board from hammering the API.
		config.RemoteRefreshInterval = time.Nanosecond
	})

	if _, err := service.Collect(context.Background()); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	// The second query fails, but a good result is already cached.
	snapshot, err := service.collectRateLimited(context.Background())
	if err != nil {
		t.Fatalf("second collection: %v", err)
	}
	source, _ := snapshot.Source(SourceGitHub)
	if source.Availability != AvailabilityStale {
		t.Fatalf("availability = %q, want stale", source.Availability)
	}
	if !snapshot.Stale {
		t.Fatal("the snapshot did not report itself stale")
	}
	if snapshot.Complete {
		t.Fatal("a stale snapshot called itself complete")
	}
	if !strings.Contains(source.Detail, "last successful result") {
		t.Fatalf("detail = %q, want the reuse explained", source.Detail)
	}
}

func TestRemoteClockClampsBelowTheFloor(t *testing.T) {
	clock := newRemoteClock(&fakeRemote{}, time.Second)
	if clock.interval != MinRemoteRefreshInterval {
		t.Fatalf("interval = %v, want it clamped to %v", clock.interval, MinRemoteRefreshInterval)
	}
}

func TestRemoteClockReturnsTheErrorWhenNothingIsCached(t *testing.T) {
	failing := &fakeRemote{errFrom: 1, err: &github.Error{Kind: github.ErrorNetwork, Detail: "the GitHub query failed"}}
	clock := newRemoteClock(failing, time.Hour)

	outcome := clock.get(context.Background(), "dev", observed, true)
	if outcome.err == nil {
		t.Fatal("a first-query failure was reported as success")
	}
	if outcome.stale {
		t.Fatal("a failure with nothing cached was reported as stale data")
	}
}

func TestCollectAttachesRemoteEvidenceToTheRightFeature(t *testing.T) {
	remote := &fakeRemote{result: github.Result{
		ObservedAt:   observed,
		PullRequests: []github.PullRequest{pull(77, "feature/sample-feature", "open")},
	}}
	snapshot, err := newTestService(t, remote).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	found, _ := snapshot.Feature("sample-feature")
	if found.Remote.PullRequest == nil || found.Remote.PullRequest.Number != 77 {
		t.Fatalf("remote = %+v, want pull request 77", found.Remote)
	}
	if found.Phase.Phase != PhaseReview {
		t.Fatalf("phase = %q, want review", found.Phase.Phase)
	}
}

func TestCollectKeepsRemoteFindingsOnTheirFeature(t *testing.T) {
	// An unattributed "checks are failing" in the footer tells a reader
	// nothing about which feature to go and look at.
	remote := &fakeRemote{result: github.Result{
		ObservedAt: observed,
		PullRequests: []github.PullRequest{
			pull(88, "feature/sample-feature", "open", func(p *github.PullRequest) { p.Checks = github.ChecksFailing }),
		},
	}}
	snapshot, err := newTestService(t, remote).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if _, ok := findingFor(snapshot.Findings, FindingChecksFailing); ok {
		t.Fatal("a feature-scoped finding leaked into the repository footer")
	}
	found, _ := snapshot.Feature("sample-feature")
	if _, ok := findingFor(found.Findings, FindingChecksFailing); !ok {
		t.Fatalf("feature findings = %v, want the failing-checks finding", found.Findings)
	}
}
