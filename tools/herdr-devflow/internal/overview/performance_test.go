package overview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// healthyRepo builds a repository with count features: planning artifacts, a
// linked worktree each, a bridge record, a live agent, and a merged pull
// request. It is the shape the five-second budget is written against.
func healthyRepo(t *testing.T, count int) (root string, run worktree.Runner, remote *fakeRemote, agents *fakeAgents, bridge *fakeBridge) {
	t.Helper()
	root = t.TempDir()
	dev := filepath.Join(root, "ori-agent-dev")
	if err := os.MkdirAll(filepath.Join(dev, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dev, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	var (
		listing strings.Builder
		pulls   []github.PullRequest
		live    []herdr.AgentInfo
	)
	listing.WriteString("worktree " + dev + "\nHEAD aaa\nbranch refs/heads/dev\n\n")
	bridgeState := model.BridgeState{Version: 1, Features: map[string]model.FeatureState{}}

	// A realistic plan: 7 milestones and 12 subtasks each.
	var plan strings.Builder
	for milestone := 1; milestone <= 7; milestone++ {
		fmt.Fprintf(&plan, "- [ ] %d.0 Milestone %d\n", milestone, milestone)
		for subtask := 1; subtask <= 12; subtask++ {
			state := " "
			if milestone < 4 {
				state = "x"
			}
			fmt.Fprintf(&plan, "  - [%s] %d.%d Subtask text for milestone %d\n", state, milestone, subtask, milestone)
		}
	}

	for index := range count {
		slug := fmt.Sprintf("feature-%02d", index)
		write := func(name, contents string) {
			if err := os.WriteFile(filepath.Join(dev, "tasks", name), []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		write("prd-"+slug+".md", "# PRD: "+slug+"\n")
		write("tasks-"+slug+".md", plan.String())

		checkout := filepath.Join(root, slug)
		if err := os.MkdirAll(checkout, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: /elsewhere\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&listing, "worktree %s\nHEAD abc%02d\nbranch refs/heads/feature/%s\n\n", checkout, index, slug)

		pulls = append(pulls, pull(200+index, "feature/"+slug, "open"))
		workspace := fmt.Sprintf("ws-%02d", index)
		live = append(live, liveAgent(workspace, workspace+":p1", "term", "ori-"+slug))
		bridgeState.Features[slug] = model.FeatureState{
			Feature:     model.Feature{Name: slug, Branch: "feature/" + slug, Path: checkout},
			WorkspaceID: workspace,
			Agents: map[string]model.RoleAgent{
				"builder": savedRole("builder", workspace, workspace+":p1", "term", "ori-"+slug),
			},
			UpdatedAt: observed.Add(time.Hour),
		}
	}

	output := listing.String()
	run = func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "worktree" && args[1] == "list" {
			return output, nil
		}
		switch strings.Join(args, " ") {
		case "rev-parse --abbrev-ref HEAD":
			return "feature/x", nil
		case "rev-parse HEAD":
			return "abcdef0123456789", nil
		case "status --porcelain":
			return "", nil
		case "rev-list --left-right --count dev...HEAD":
			return "2 5", nil
		case "rev-list --count dev..origin/dev":
			return "0", nil
		}
		return "", nil
	}
	remote = &fakeRemote{result: github.Result{ObservedAt: observed, PullRequests: pulls}}
	agents = &fakeAgents{live: live}
	bridge = &fakeBridge{state: bridgeState}
	return root, run, remote, agents, bridge
}

func TestCollectHealthyRepositoryWithinBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("performance budget is not measured in short mode")
	}
	root, run, remote, agents, bridge := healthyRepo(t, 20)
	service := NewService(Config{
		RepoRoot: root, Git: run, Remote: remote, Agents: agents, Bridge: bridge,
		Now: func() time.Time { return observed },
	})

	start := time.Now()
	snapshot, err := service.Collect(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(snapshot.Features) != 20 {
		t.Fatalf("features = %d, want 20", len(snapshot.Features))
	}
	// The budget is generous on purpose: it exists to catch an accidental
	// per-feature network call or an unbounded fan-out, not to police
	// microseconds.
	if elapsed > 5*time.Second {
		t.Fatalf("collection took %v, want under 5s for a healthy 20-feature repository", elapsed)
	}
	if remote.count() != 1 {
		t.Fatalf("remote queries = %d, want exactly one repository-wide call", remote.count())
	}
	if agents.callCount() != 1 {
		t.Fatalf("herdr calls = %d, want exactly one listing", agents.callCount())
	}
}

func BenchmarkCollectHealthyRepository(b *testing.B) {
	helper := &testing.T{}
	root, run, remote, agents, bridge := healthyRepo(helper, 20)
	service := NewService(Config{
		RepoRoot: root, Git: run, Remote: remote, Agents: agents, Bridge: bridge,
		Now: func() time.Time { return observed },
	})

	b.ResetTimer()
	for b.Loop() {
		if _, err := service.Collect(context.Background()); err != nil {
			b.Fatalf("Collect: %v", err)
		}
	}
}

func TestWatchNeverExceedsTheConfiguredRemoteCadence(t *testing.T) {
	root, run, remote, agents, bridge := healthyRepo(t, 5)
	service := NewService(Config{
		RepoRoot: root, Git: run, Remote: remote, Agents: agents, Bridge: bridge,
		// Deliberately below the floor: the clock must clamp it.
		RemoteRefreshInterval: time.Millisecond,
		Now:                   func() time.Time { return observed },
	})

	ctx, cancel := context.WithCancel(context.Background())
	renders := 0
	if err := service.Watch(ctx, time.Millisecond, func(Snapshot) {
		renders++
		if renders == 25 {
			cancel()
		}
	}); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// 25 local renders, but the remote clock's floor means one query.
	if renders < 25 {
		t.Fatalf("renders = %d, want the local clock to keep ticking", renders)
	}
	if remote.count() != 1 {
		t.Fatalf("remote queries = %d over %d renders, want 1: the cadence floor is the only thing preventing an API storm",
			remote.count(), renders)
	}
}

func TestCollectIsCancelable(t *testing.T) {
	root, _, remote, agents, bridge := healthyRepo(t, 5)
	blocked := make(chan struct{})
	var once sync.Once

	service := NewService(Config{
		RepoRoot: root,
		Git: func(ctx context.Context, _ string, args ...string) (string, error) {
			if len(args) >= 2 && args[0] == "worktree" {
				return "", nil
			}
			once.Do(func() { close(blocked) })
			<-ctx.Done()
			return "", ctx.Err()
		},
		Remote: remote, Agents: agents, Bridge: bridge,
		Now: func() time.Time { return observed },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = service.Collect(ctx)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("collection ignored cancellation")
	}
}

func TestConcurrentCollectionsAreSafe(t *testing.T) {
	// The Herdr board and a CLI invocation can share one service.
	root, run, remote, agents, bridge := healthyRepo(t, 8)
	service := NewService(Config{
		RepoRoot: root, Git: run, Remote: remote, Agents: agents, Bridge: bridge,
		Now: func() time.Time { return observed },
	})

	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := service.Collect(context.Background()); err != nil {
				t.Errorf("Collect: %v", err)
			}
		}()
	}
	wait.Wait()
}
