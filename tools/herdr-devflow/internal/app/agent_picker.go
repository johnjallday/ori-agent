package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
)

// goAgent provides the interactive counterpart to the live roster. It focuses
// the exact Herdr agent selected by the operator and does not require that agent
// to have a saved Ori bridge role.
func (a *App) goAgent(ctx context.Context, opts options, args []string) int {
	if len(args) != 0 {
		a.writeError(fmt.Errorf("go takes no arguments: wt herd go"), opts.json)
		return 2
	}
	if opts.json {
		a.writeError(fmt.Errorf("go is interactive and cannot be combined with --json"), true)
		return 2
	}
	if !a.isInteractive() {
		a.writeError(fmt.Errorf("wt herd go requires an interactive terminal; run it directly or from the wt REPL"), false)
		return 1
	}

	runtime, err := a.load(opts)
	if err != nil {
		a.writeError(stageConfigError(err), false)
		return 1
	}
	roster, err := collectLiveAgentRoster(ctx, runtime.herdr, nil)
	if err != nil {
		a.writeError(err, false)
		return 1
	}
	if len(roster.Agents) == 0 {
		if err := renderLiveAgentRoster(a.stdout, roster); err != nil {
			a.writeError(err, false)
			return 1
		}
		return 0
	}
	if err := renderAgentPicker(a.stdout, roster); err != nil {
		a.writeError(err, false)
		return 1
	}

	reader := bufio.NewReader(a.stdin)
	for {
		fmt.Fprint(a.stdout, "Choice: ")
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			a.writeError(fmt.Errorf("read agent choice: %w", readErr), false)
			return 1
		}
		choice := strings.TrimSpace(line)
		if choice == "" || strings.EqualFold(choice, "q") {
			return 0
		}
		index, parseErr := strconv.Atoi(choice)
		if parseErr != nil || index < 1 || index > len(roster.Agents) {
			fmt.Fprintf(a.stderr, "Invalid choice %q; enter 1-%d or q.\n", choice, len(roster.Agents))
			if errors.Is(readErr, io.EOF) {
				return 1
			}
			continue
		}

		selected := roster.Agents[index-1]
		if selected.Target == "" {
			a.writeError(fmt.Errorf("selected agent has no focusable Herdr target"), false)
			return 1
		}
		if err := runtime.herdr.FocusAgent(ctx, selected.Target); err != nil {
			a.writeError(err, false)
			return 1
		}
		fmt.Fprintf(a.stdout, "Focused %s (%s) in %s.\n",
			selected.Agent, selected.Kind, liveAgentWorktreeLabel(selected.Worktree))
		return 0
	}
}

func renderAgentPicker(out io.Writer, roster liveAgentRoster) error {
	if _, err := fmt.Fprintln(out, "Select an open agent:"); err != nil {
		return err
	}
	table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "  #\tAGENT\tKIND\tSTATUS\tWORKTREE"); err != nil {
		return err
	}
	for index, agent := range roster.Agents {
		if _, err := fmt.Fprintf(table, "  %d)\t%s\t%s\t%s\t%s\n",
			index+1, agent.Agent, agent.Kind, agent.Status, liveAgentWorktreeLabel(agent.Worktree)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(table, "  q)\tQuit"); err != nil {
		return err
	}
	return table.Flush()
}
