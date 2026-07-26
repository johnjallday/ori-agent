package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectLinkedGitWorktreeProvesPathBranchAndRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	runGit(t, "", "init", "-b", "dev", repo)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "-c", "user.name=Ori Test", "-c", "user.email=ori@example.test", "commit", "-m", "fixture")
	feature := filepath.Join(filepath.Dir(repo), "feature")
	runGit(t, repo, "worktree", "add", "-b", "feature/bridge", feature)

	paths, err := Resolve(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := InspectLinkedGitWorktree(context.Background(), feature, "feature/bridge", paths.GitCommonDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "feature/bridge" || got.CommonDir != paths.GitCommonDir || got.SourcePath != paths.RepoRoot {
		t.Fatalf("InspectLinkedGitWorktree() = %#v, want branch/common dir/source checkout", got)
	}
	if _, err := InspectLinkedGitWorktree(context.Background(), feature, "feature/other", paths.GitCommonDir); err == nil {
		t.Fatal("InspectLinkedGitWorktree accepted the wrong branch")
	}
	if _, err := InspectLinkedGitWorktree(context.Background(), repo, "dev", paths.GitCommonDir); err == nil {
		t.Fatal("InspectLinkedGitWorktree accepted the repository source checkout")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	command := exec.Command("git", args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

// scriptedRunner answers a fixed set of Git invocations and records the exact
// argument vectors it was given.
type scriptedRunner struct {
	responses map[string]string
	failures  map[string]error
	calls     [][]string
}

func (r *scriptedRunner) run(_ context.Context, _ string, args ...string) (string, error) {
	r.calls = append(r.calls, args)
	key := strings.Join(args, " ")
	if err, ok := r.failures[key]; ok {
		return "", err
	}
	if value, ok := r.responses[key]; ok {
		return value, nil
	}
	return "", fmt.Errorf("unexpected git invocation %q", key)
}

func healthyRunner() *scriptedRunner {
	return &scriptedRunner{responses: map[string]string{
		"rev-parse --abbrev-ref HEAD":              "feature/downloads-janitor",
		"rev-parse HEAD":                           "0123456789abcdef",
		"status --porcelain":                       " M internal/foo.go\n?? scratch.txt\n",
		"rev-list --left-right --count dev...HEAD": "4 6",
		"rev-list --count dev..origin/dev":         "0",
	}, failures: map[string]error{}}
}

func TestInspectFactsReadsCompleteLocalEvidence(t *testing.T) {
	runner := healthyRunner()
	facts := InspectFacts(context.Background(), runner.run, t.TempDir(), "dev")

	if facts.Branch != "feature/downloads-janitor" || facts.BranchAvailability != FactAvailable {
		t.Fatalf("branch = %q/%q", facts.Branch, facts.BranchAvailability)
	}
	if facts.Head != "0123456789abcdef" || facts.HeadAvailability != FactAvailable {
		t.Fatalf("head = %q/%q", facts.Head, facts.HeadAvailability)
	}
	if !facts.Dirty || facts.DirtyAvailability != FactAvailable {
		t.Fatalf("dirty = %v/%q, want a dirty worktree", facts.Dirty, facts.DirtyAvailability)
	}
	// left-right prints behind then ahead: 4 commits only on dev, 6 only here.
	if facts.Behind != 4 || facts.Ahead != 6 {
		t.Fatalf("divergence = +%d/-%d, want ahead 6 behind 4", facts.Ahead, facts.Behind)
	}
	if facts.BaselineStale {
		t.Fatal("baseline reported stale despite matching its remote")
	}
	if facts.Detail != "" {
		t.Fatalf("detail = %q, want empty when nothing degraded", facts.Detail)
	}
}

func TestInspectFactsCleanWorktree(t *testing.T) {
	runner := healthyRunner()
	runner.responses["status --porcelain"] = "\n"

	facts := InspectFacts(context.Background(), runner.run, t.TempDir(), "dev")
	if facts.Dirty {
		t.Fatal("clean worktree reported dirty")
	}
	if facts.DirtyAvailability != FactAvailable {
		t.Fatalf("dirty availability = %q, want available", facts.DirtyAvailability)
	}
}

func TestInspectFactsDetectsStaleBaseline(t *testing.T) {
	runner := healthyRunner()
	runner.responses["rev-list --count dev..origin/dev"] = "12"

	facts := InspectFacts(context.Background(), runner.run, t.TempDir(), "dev")
	if !facts.BaselineStale || facts.BaselineStaleAvailability != FactAvailable {
		t.Fatalf("baseline stale = %v/%q, want a stale local baseline", facts.BaselineStale, facts.BaselineStaleAvailability)
	}
}

func TestInspectFactsDegradesEachFactIndependently(t *testing.T) {
	runner := healthyRunner()
	runner.failures["rev-list --left-right --count dev...HEAD"] = errors.New("no merge base")
	runner.failures["rev-list --count dev..origin/dev"] = errors.New("no upstream")

	facts := InspectFacts(context.Background(), runner.run, t.TempDir(), "dev")
	if facts.Branch == "" || facts.BranchAvailability != FactAvailable {
		t.Fatal("a failed divergence count discarded the successfully read branch")
	}
	if facts.DirtyAvailability != FactAvailable {
		t.Fatal("a failed divergence count discarded the working-tree state")
	}
	if facts.DivergenceAvailability != FactUnavailable || facts.BaselineStaleAvailability != FactUnavailable {
		t.Fatal("failed counts were reported as available")
	}
	if facts.Ahead != 0 || facts.Behind != 0 {
		t.Fatalf("unavailable divergence produced counts +%d/-%d", facts.Ahead, facts.Behind)
	}
	if !strings.Contains(facts.Detail, "divergence versus dev") {
		t.Fatalf("detail = %q, want an explanation of the degraded fact", facts.Detail)
	}
}

func TestInspectFactsDetachedHeadIsNotABranch(t *testing.T) {
	runner := healthyRunner()
	runner.responses["rev-parse --abbrev-ref HEAD"] = "HEAD"

	facts := InspectFacts(context.Background(), runner.run, t.TempDir(), "dev")
	if facts.Branch != "" || facts.BranchAvailability != FactUnavailable {
		t.Fatalf("detached HEAD reported branch %q/%q", facts.Branch, facts.BranchAvailability)
	}
	if !strings.Contains(facts.Detail, "detached") {
		t.Fatalf("detail = %q, want the detached state explained", facts.Detail)
	}
}

func TestInspectFactsRejectsMalformedCounts(t *testing.T) {
	runner := healthyRunner()
	runner.responses["rev-list --left-right --count dev...HEAD"] = "not a count"

	facts := InspectFacts(context.Background(), runner.run, t.TempDir(), "dev")
	if facts.DivergenceAvailability != FactUnavailable {
		t.Fatal("malformed counts were accepted as available")
	}
}

func TestInspectFactsUsesFixedArgumentVectors(t *testing.T) {
	runner := healthyRunner()
	InspectFacts(context.Background(), runner.run, t.TempDir(), "dev")

	if len(runner.calls) != 5 {
		t.Fatalf("git invocations = %d, want 5", len(runner.calls))
	}
	for _, call := range runner.calls {
		for _, arg := range call {
			if strings.ContainsAny(arg, ";|&$`\n") {
				t.Fatalf("argument %q could be interpreted by a shell", arg)
			}
		}
	}
}

func TestInspectFactsHonoursANonDefaultBaseline(t *testing.T) {
	runner := &scriptedRunner{responses: map[string]string{
		"rev-parse --abbrev-ref HEAD":               "feature/x",
		"rev-parse HEAD":                            "abc",
		"status --porcelain":                        "",
		"rev-list --left-right --count main...HEAD": "1 2",
		"rev-list --count main..origin/main":        "0",
	}, failures: map[string]error{}}

	facts := InspectFacts(context.Background(), runner.run, t.TempDir(), "main")
	if facts.DivergenceAvailability != FactAvailable || facts.Ahead != 2 || facts.Behind != 1 {
		t.Fatalf("facts = %+v, want counts against main", facts)
	}
}
