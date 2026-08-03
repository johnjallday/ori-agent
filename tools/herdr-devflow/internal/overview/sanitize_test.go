package overview

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/model"
)

// hostile values that arrive from files, the network, or a terminal and are
// then printed into a terminal, a JSON payload, or a Herdr board cell.
const (
	escapeSequence = "\x1b[31mRED\x1b[0m"
	nullByte       = "\x00"
	secretToken    = "ghp_LEAKEDTOKENVALUE00000000000000000000"
)

// containsControlCharacters reports whether rendered output carries anything a
// terminal would interpret rather than display.
func containsControlCharacters(value string) bool {
	for _, r := range value {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 32 || r == 127 {
			return true
		}
	}
	return false
}

func hostileRepo(t *testing.T) *Service {
	t.Helper()
	root := t.TempDir()
	dev := filepath.Join(root, "ori-agent-dev")
	if err := os.MkdirAll(filepath.Join(dev, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dev, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A planning artifact whose title and task text carry escape sequences.
	write := func(name, contents string) {
		if err := os.WriteFile(filepath.Join(dev, "tasks", name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("prd-hostile-feature.md", "# PRD: "+escapeSequence+"Title"+nullByte+"\n")
	write("tasks-hostile-feature.md",
		"- [ ] 1.0 Milestone "+escapeSequence+"\n  - [ ] 1.1 Subtask "+escapeSequence+nullByte+"\n")

	// A stray BACKLOG.md is written deliberately: the file this feature deleted
	// may still exist in somebody's checkout, and nothing may read it back in.
	backlog := "# Backlog\n\n## Doing\n- hostile-feature -> PRD at tasks/prd-hostile-feature.md " + escapeSequence + "\n"
	if err := os.WriteFile(filepath.Join(dev, "BACKLOG.md"), []byte(backlog), 0o600); err != nil {
		t.Fatal(err)
	}

	checkout := filepath.Join(root, "hostile-feature")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: /elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	listing := "worktree " + dev + "\nHEAD aaa\nbranch refs/heads/dev\n\n" +
		"worktree " + checkout + "\nHEAD bbb\nbranch refs/heads/feature/hostile-feature\n"
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "worktree" && args[1] == "list" {
			return listing, nil
		}
		if strings.Join(args, " ") == "status --porcelain" {
			// Even Git output is not trusted.
			return " M file" + escapeSequence + "\n", nil
		}
		return "", nil
	}

	// A pull request whose branch and URL were chosen by whoever opened it.
	remote := &fakeRemote{result: github.Result{
		ObservedAt: observed,
		PullRequests: []github.PullRequest{{
			Number: 9, Head: "feature/hostile-feature", Base: "dev", State: "open",
			URL: "https://example.test/" + escapeSequence, Checks: github.ChecksPassing,
		}},
	}}

	// A Herdr agent whose pane name came from a terminal.
	agents := &fakeAgents{live: []herdr.AgentInfo{{
		WorkspaceID: "ws-1", PaneID: "pane-1" + escapeSequence, TerminalID: "term-1",
		Name: "agent" + escapeSequence, Agent: "claude", AgentStatus: model.AgentIdle,
	}}}
	bridge := &fakeBridge{state: model.BridgeState{Version: 1, Features: map[string]model.FeatureState{
		"hostile-feature": {
			Feature:     model.Feature{Name: "hostile-feature", Branch: "feature/hostile-feature", Path: checkout},
			WorkspaceID: "ws-1",
			Agents: map[string]model.RoleAgent{
				"builder": savedRole("builder", "ws-1", "pane-0", "term-1", "saved"+escapeSequence),
			},
			UpdatedAt: observed,
		},
	}}}

	return NewService(Config{
		RepoRoot: root, Git: run, Remote: remote, Agents: agents, Bridge: bridge,
		Now: func() time.Time { return observed },
	})
}

func TestHostileInputNeverReachesRenderedOutput(t *testing.T) {
	snapshot, err := hostileRepo(t).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	row, ok := snapshot.Feature("hostile-feature")
	if !ok {
		t.Fatalf("the hostile feature was dropped entirely: %+v", snapshot.Features)
	}

	surfaces := map[string]func(*strings.Builder) error{
		"compact":  func(out *strings.Builder) error { return RenderCompact(out, snapshot, RenderOptions{NoColor: true}) },
		"expanded": func(out *strings.Builder) error { return RenderExpanded(out, snapshot, RenderOptions{NoColor: true}) },
	}
	for name, render := range surfaces {
		var out strings.Builder
		if err := render(&out); err != nil {
			t.Fatalf("%s render: %v", name, err)
		}
		if containsControlCharacters(out.String()) {
			t.Fatalf("%s surface emitted control characters from hostile input:\n%q", name, out.String())
		}
	}

	var detail strings.Builder
	if err := RenderDetail(&detail, snapshot, row, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderDetail: %v", err)
	}
	if containsControlCharacters(detail.String()) {
		t.Fatalf("detail surface emitted control characters:\n%q", detail.String())
	}
}

func TestHostileInputNeverReachesJSON(t *testing.T) {
	snapshot, err := hostileRepo(t).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Go escapes control characters when encoding, so assert on the decoded
	// values: that is what a consumer actually renders.
	var decoded Snapshot
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	row, _ := decoded.Feature("hostile-feature")

	values := []string{row.Title, row.Plan.Progress.NextActionable.Text}
	for _, agent := range row.Agents {
		values = append(values, agent.Live.Pane, agent.Saved.Session, agent.BindingDetail)
	}
	for _, finding := range row.Findings {
		values = append(values, finding.Message, finding.Detail)
	}
	for _, value := range values {
		if containsControlCharacters(value) {
			t.Fatalf("a JSON field carried control characters: %q", value)
		}
	}
}

func TestRemoteErrorsNeverCarrySecretsIntoAnySurface(t *testing.T) {
	// The one path where a credential could plausibly reach a terminal.
	root, run, _, agents, bridge := healthyRepo(t, 2)
	failing := &fakeRemote{errFrom: 1, err: &github.Error{
		Kind:   github.ErrorUnauthenticated,
		Detail: "the GitHub CLI is not authenticated for this repository",
	}}
	service := NewService(Config{
		RepoRoot: root, Git: run, Remote: failing, Agents: agents, Bridge: bridge,
		Now: func() time.Time { return observed },
	})

	snapshot, err := service.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	var compact, expanded strings.Builder
	if err := RenderCompact(&compact, snapshot, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderCompact: %v", err)
	}
	if err := RenderExpanded(&expanded, snapshot, RenderOptions{NoColor: true}); err != nil {
		t.Fatalf("RenderExpanded: %v", err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for surface, output := range map[string]string{
		"compact": compact.String(), "expanded": expanded.String(), "json": string(encoded),
	} {
		if strings.Contains(output, secretToken) {
			t.Fatalf("%s surface leaked a token", surface)
		}
		for _, forbidden := range []string{"Authorization", "Bearer ", "ghp_"} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("%s surface leaked %q", surface, forbidden)
			}
		}
	}
	// The failure must still be explained and actionable.
	if !strings.Contains(compact.String(), "gh auth login") {
		t.Fatalf("the recovery command was suppressed along with the secret:\n%s", compact.String())
	}
}

// TestWorktreePathStaysBounded covers a displayed value the other sanitize
// fixtures do not: the worktree directory path itself. A control character in
// a checkout path is already rejected earlier, by canonicalPath's own
// validation (see worktree/context.go), so a directory name can never reach
// this field carrying one. Length is a different property with no such
// upstream guarantee — a deeply nested checkout is entirely legal on the
// filesystem — so this checks the read model, terminal, and JSON bound it like
// every other displayed value.
func TestWorktreePathStaysBounded(t *testing.T) {
	root := t.TempDir()
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
	write("prd-long-path.md", "# PRD: Long Path\n")
	write("tasks-long-path.md", "- [ ] 1.0 Live\n  - [ ] 1.1 Next\n")

	// A legal but excessively long directory name, well past NAME_MAX-safe on
	// every platform this runs on, pushing the whole checkout path over 200
	// runes without touching any control character.
	checkout := filepath.Join(root, strings.Repeat("a", 220))
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: /elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	listing := "worktree " + dev + "\nHEAD aaa\nbranch refs/heads/dev\n\n" +
		"worktree " + checkout + "\nHEAD bbb\nbranch refs/heads/feature/long-path\n"
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "worktree" && args[1] == "list" {
			return listing, nil
		}
		return "", nil
	}
	remote := &fakeRemote{result: github.Result{ObservedAt: observed}}

	service := NewService(Config{
		RepoRoot: root, Git: run, Remote: remote,
		Now: func() time.Time { return observed },
	})
	snapshot, err := service.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	row, ok := snapshot.Feature("long-path")
	if !ok {
		t.Fatalf("the long-path feature was dropped entirely: %+v", snapshot.Features)
	}
	if runes := []rune(row.Git.WorktreePath); len(runes) > 201 {
		t.Fatalf("worktree path length = %d, want bounded (the checkout path was %d runes)", len(runes), len([]rune(checkout)))
	}

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	decodedRow, _ := decoded.Feature("long-path")
	if runes := []rune(decodedRow.Git.WorktreePath); len(runes) > 201 {
		t.Fatalf("worktree_path JSON field length = %d, want bounded", len(runes))
	}
}

func TestSanitizedValuesStayBounded(t *testing.T) {
	snapshot, err := hostileRepo(t).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	row, _ := snapshot.Feature("hostile-feature")

	if runes := []rune(row.Title); len(runes) > 200 {
		t.Fatalf("title length = %d, want bounded", len(runes))
	}
	if runes := []rune(row.Plan.Progress.NextActionable.Text); len(runes) > 260 {
		t.Fatalf("task text length = %d, want bounded", len(runes))
	}
}
