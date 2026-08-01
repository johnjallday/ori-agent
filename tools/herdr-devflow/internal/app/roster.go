package app

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/herdr"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// liveAgentRoster is deliberately smaller than the repository overview. It is
// the answer to one operational question: which coding agents does Herdr report
// as open right now?
type liveAgentRoster struct {
	Agents []liveAgentRow `json:"agents"`
}

type liveAgentRow struct {
	Agent    string `json:"agent"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Worktree string `json:"worktree"`
	Target   string `json:"-"`
}

type liveAgentFilter func(herdr.AgentInfo) bool

func collectLiveAgentRoster(ctx context.Context, client *herdr.Client, include liveAgentFilter) (liveAgentRoster, error) {
	agents, err := client.AgentListInfo(ctx)
	if err != nil {
		return liveAgentRoster{}, err
	}

	roster := liveAgentRoster{Agents: make([]liveAgentRow, 0, len(agents))}
	for _, agent := range agents {
		if include != nil && !include(agent) {
			continue
		}
		worktreePath := liveAgentWorktree(agent)
		kind := cleanRosterField(agent.Agent, 32)
		if kind == "" && agent.AgentSession != nil {
			kind = cleanRosterField(agent.AgentSession.Agent, 32)
		}
		if kind == "" {
			kind = "unknown"
		}

		name := cleanRosterField(agent.Name, 80)
		if name == "" {
			name = kind
			if label := liveAgentWorktreeLabel(worktreePath); worktreePath != "" && label != "" {
				name += "@" + label
			}
		}

		status := cleanRosterField(string(agent.AgentStatus), 32)
		if status == "" {
			status = "unknown"
		}

		roster.Agents = append(roster.Agents, liveAgentRow{
			Agent:    name,
			Kind:     kind,
			Status:   status,
			Worktree: cleanRosterField(worktreePath, 512),
			Target:   liveAgentTarget(agent),
		})
	}

	sort.SliceStable(roster.Agents, func(left, right int) bool {
		if roster.Agents[left].Worktree != roster.Agents[right].Worktree {
			return roster.Agents[left].Worktree < roster.Agents[right].Worktree
		}
		if roster.Agents[left].Kind != roster.Agents[right].Kind {
			return roster.Agents[left].Kind < roster.Agents[right].Kind
		}
		return roster.Agents[left].Agent < roster.Agents[right].Agent
	})
	return roster, nil
}

func renderLiveAgentRoster(out io.Writer, roster liveAgentRoster) error {
	if len(roster.Agents) == 0 {
		_, err := fmt.Fprintln(out, "No open agents.")
		return err
	}
	if _, err := fmt.Fprintf(out, "Open agents: %d\n", len(roster.Agents)); err != nil {
		return err
	}

	table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "AGENT\tKIND\tSTATUS\tWORKTREE"); err != nil {
		return err
	}
	for _, agent := range roster.Agents {
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\n",
			agent.Agent, agent.Kind, agent.Status, liveAgentWorktreeLabel(agent.Worktree)); err != nil {
			return err
		}
	}
	return table.Flush()
}

func statusAgentFilter(ctx context.Context, runtime runtimeContext, parsed statusArgs) (liveAgentFilter, error) {
	root := ""
	switch {
	case parsed.current:
		root = runtime.paths.RepoRoot
	case parsed.context.worktree != "":
		root = parsed.context.worktree
	case parsed.context.feature != "":
		if !worktree.ValidSlug(parsed.context.feature) {
			return nil, fmt.Errorf("--feature must be a canonical feature slug")
		}
		inventory, err := worktree.ListCheckouts(ctx, runtime.paths.RepoRoot, "dev", nil, time.Now())
		if err != nil {
			return nil, fmt.Errorf("resolve feature worktree: %w", err)
		}
		checkouts, ok := inventory.Feature(parsed.context.feature)
		if !ok || len(checkouts) == 0 {
			return nil, fmt.Errorf("no worktree found for feature %q", parsed.context.feature)
		}
		if len(checkouts) != 1 {
			return nil, fmt.Errorf("feature %q resolves to multiple worktrees", parsed.context.feature)
		}
		root = checkouts[0].Path
	}
	if strings.TrimSpace(root) == "" {
		return nil, nil
	}
	return func(agent herdr.AgentInfo) bool {
		return worktree.Contains(root, liveAgentWorktree(agent))
	}, nil
}

func liveAgentTarget(agent herdr.AgentInfo) string {
	for _, target := range []string{agent.Name, agent.PaneID, agent.TerminalID} {
		if strings.TrimSpace(target) != "" {
			return strings.TrimSpace(target)
		}
	}
	return ""
}

func liveAgentWorktree(agent herdr.AgentInfo) string {
	if strings.TrimSpace(agent.Cwd) != "" {
		return filepath.Clean(agent.Cwd)
	}
	if strings.TrimSpace(agent.ForegroundCwd) != "" {
		return filepath.Clean(agent.ForegroundCwd)
	}
	return ""
}

func liveAgentWorktreeLabel(path string) string {
	if strings.TrimSpace(path) == "" {
		return "—"
	}
	label := filepath.Base(filepath.Clean(path))
	if label == "." || label == string(filepath.Separator) {
		return cleanRosterField(path, 80)
	}
	return cleanRosterField(label, 80)
}

func cleanRosterField(value string, limit int) string {
	value = strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return -1
		}
		return char
	}, value)
	value = strings.TrimSpace(value)
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

func (a *App) writeLiveAgentRoster(roster liveAgentRoster, jsonOutput bool) error {
	if jsonOutput {
		a.writeResult(true, roster)
		return nil
	}
	return renderLiveAgentRoster(a.stdout, roster)
}

func (a *App) watchLiveAgentRoster(
	ctx context.Context,
	client *herdr.Client,
	include liveAgentFilter,
	interval time.Duration,
	noColor bool,
) int {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	rendered := false
	for {
		roster, err := collectLiveAgentRoster(ctx, client, include)
		if err != nil {
			a.writeError(err, false)
			return 1
		}
		if rendered && a.statusColorEnabled(noColor) {
			fmt.Fprint(a.stdout, "\x1b[2J\x1b[H")
		}
		rendered = true
		if err := a.writeLiveAgentRoster(roster, false); err != nil {
			a.writeError(err, false)
			return 1
		}

		select {
		case <-ctx.Done():
			return 0
		case <-ticker.C:
		}
	}
}
