package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
)

// This file owns the `./scripts/devops/backlog.sh` command surface: the Backlog
// column of the project board linked to this repository.
//
// Backlog means captured and not yet groomed — where GitHub's auto-add workflow
// puts every new Issue. It is the column you read to see what is waiting, not
// what is buildable; that is `./scripts/devops/ready.sh`. The raw Issue capture
// list behind both is `./scripts/devops/issue.sh`.

// backlogSchemaVersion versions the JSON payload this command emits.
//
// It is 2 because version 1 of this same command emitted the Ready column. The
// shape did not change but its meaning did, and a consumer that branched on
// version 1 would otherwise be handed a different column without being told.
// The Ready column now has its own command and its own version 1.
const backlogSchemaVersion = 2

// parseBacklogArgs validates an invocation completely before anything runs.
//
// The Issue subcommands this command used to answer are rejected by name rather
// than falling through to "unknown subcommand". `backlog.sh add` was a real
// command once; someone typing it today deserves to be told where it went, not
// that it never existed.
func parseBacklogArgs(args []string) (boardArgs, error) {
	parsed := boardArgs{}
	var positional []string

	for _, argument := range args {
		switch {
		case argument == "--json":
			parsed.json = true
		case argument == "--all":
			return boardArgs{}, errors.New(
				"backlog: --all applies to Issues, not the board — the board already holds " +
					"only what belongs on it; for every open Issue use: ./scripts/devops/issue.sh --all")
		case strings.HasPrefix(argument, "-"):
			return boardArgs{}, fmt.Errorf("backlog: unknown option %q", argument)
		default:
			positional = append(positional, argument)
		}
	}

	if len(positional) == 0 {
		return parsed, nil
	}
	switch positional[0] {
	case "ready":
		// The column this command used to read. Named explicitly because
		// someone typing it is remembering the old behaviour, and the honest
		// answer is that it moved rather than that it is not a subcommand.
		return boardArgs{}, errors.New(
			"backlog: the Ready column moved to its own command: ./scripts/devops/ready.sh")
	case "list", "ls":
		// Deliberately not accepted as a synonym for the default. `list` used to
		// mean "list Issues", and silently redefining it to mean "list the
		// board" would answer a different question than the one that was asked.
		return boardArgs{}, errors.New(
			"backlog: list moved — ./scripts/devops/backlog.sh reads the project board's Backlog column, " +
				"and Issues are listed by: ./scripts/devops/issue.sh")
	case "view", "show":
		return boardArgs{}, errors.New(
			"backlog: view moved to ./scripts/devops/issue.sh view <number|url>")
	case "add", "new":
		return boardArgs{}, errors.New(
			"backlog: add moved to ./scripts/devops/issue.sh add \"<title>\"")
	default:
		return boardArgs{}, fmt.Errorf("backlog: unknown subcommand %q", positional[0])
	}
}

// backlogColumn describes the Backlog column to the shared board runner.
var backlogColumn = boardColumn{
	name:          "backlog",
	script:        "./scripts/devops/backlog.sh",
	status:        github.StatusBacklog,
	schemaVersion: backlogSchemaVersion,
	selectItems:   github.ProjectBoard.Backlog,
	counted:       countedBacklog,
	emptyMessage: "The Backlog column is empty. Capture something with: " +
		"./scripts/devops/issue.sh add \"<title>\"",
}

// backlog runs one `./scripts/devops/backlog.sh` invocation.
func (a *App) backlog(ctx context.Context, opts options, args []string) int {
	parsed, err := parseBacklogArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	return a.runBoardColumn(ctx, opts, backlogColumn, parsed)
}

// countedBacklog names the column in the count, because "4" alone beside a
// board title does not say which of its columns was counted.
func countedBacklog(count int) string {
	if count == 1 {
		return "1 in Backlog"
	}
	return fmt.Sprintf("%d in Backlog", count)
}
