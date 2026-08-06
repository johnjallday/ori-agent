package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
)

// This file owns the `./scripts/devops/ready.sh` command surface: the Ready
// column of the project board linked to this repository, in the order it was
// ranked.
//
// Ready means buildable — an Issue the grooming agent has researched and
// written a spec comment on. It does not mean approved: choosing what to build
// stays with the person reading the column, which is why this command only
// reads. What is waiting to be groomed is `./scripts/devops/backlog.sh`.

// readySchemaVersion versions the JSON payload this command emits.
//
// It starts at 1 as a new command, and its shape is what `backlog.sh --json`
// version 1 used to emit — this command inherited that column, so a consumer
// moving over keeps the contract it already had.
const readySchemaVersion = 1

// parseReadyArgs validates an invocation completely before anything runs.
func parseReadyArgs(args []string) (boardArgs, error) {
	parsed := boardArgs{}
	var positional []string

	for _, argument := range args {
		switch {
		case argument == "--json":
			parsed.json = true
		case argument == "--all":
			return boardArgs{}, errors.New(
				"ready: --all applies to Issues, not the board — for every open Issue use: " +
					"./scripts/devops/issue.sh --all")
		case strings.HasPrefix(argument, "-"):
			return boardArgs{}, fmt.Errorf("ready: unknown option %q", argument)
		default:
			positional = append(positional, argument)
		}
	}

	if len(positional) == 0 {
		return parsed, nil
	}
	switch positional[0] {
	case "backlog":
		return boardArgs{}, errors.New(
			"ready: the Backlog column has its own command: ./scripts/devops/backlog.sh")
	case "list", "ls":
		return boardArgs{}, errors.New(
			"ready: this command already lists the Ready column; Issues are listed by: " +
				"./scripts/devops/issue.sh")
	case "view", "show":
		return boardArgs{}, errors.New(
			"ready: view moved to ./scripts/devops/issue.sh view <number|url>")
	default:
		return boardArgs{}, fmt.Errorf("ready: unknown subcommand %q", positional[0])
	}
}

// readyColumn describes the Ready column to the shared board runner.
var readyColumn = boardColumn{
	name:          "ready",
	script:        "./scripts/devops/ready.sh",
	status:        github.StatusReady,
	schemaVersion: readySchemaVersion,
	selectItems:   github.ProjectBoard.Ready,
	counted:       countedReady,
	emptyMessage: "The board has nothing in Ready yet. Captured Issues waiting to be groomed: " +
		"./scripts/devops/backlog.sh",
}

// ready runs one `./scripts/devops/ready.sh` invocation.
func (a *App) ready(ctx context.Context, opts options, args []string) int {
	parsed, err := parseReadyArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	return a.runBoardColumn(ctx, opts, readyColumn, parsed)
}

func countedReady(count int) string {
	if count == 1 {
		return "1 ready"
	}
	return fmt.Sprintf("%d ready", count)
}
