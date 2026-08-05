package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
)

// This file owns the `./scripts/backlog.sh` command surface: the Ready column
// of the project board linked to this repository, in the order it was ranked.
//
// Ready means buildable — an Issue the grooming agent has researched and
// written a spec comment on. It does not mean approved: choosing what to build
// stays with the person reading the column, which is why this command only
// reads. The raw capture list behind it is `./scripts/issue.sh`.

// backlogSchemaVersion versions the JSON payload `./scripts/backlog.sh` emits.
// It is numbered independently of the Issue contract because these are two
// different shapes from two different sources, and a consumer must never have
// to guess which one it is holding.
const backlogSchemaVersion = 1

// backlogArgs is one parsed invocation.
type backlogArgs struct {
	json bool
}

// parseBacklogArgs validates an invocation completely before anything runs.
//
// The Issue subcommands this command used to answer are rejected by name rather
// than falling through to "unknown subcommand". `backlog.sh add` was a real
// command last week; someone typing it today deserves to be told where it went,
// not that it never existed.
func parseBacklogArgs(args []string) (backlogArgs, error) {
	parsed := backlogArgs{}
	var positional []string

	for _, argument := range args {
		switch {
		case argument == "--json":
			parsed.json = true
		case argument == "--all":
			return backlogArgs{}, errors.New(
				"backlog: --all applies to Issues, not the board — the board already holds " +
					"only what belongs on it; for every open Issue use: ./scripts/issue.sh --all")
		case strings.HasPrefix(argument, "-"):
			return backlogArgs{}, fmt.Errorf("backlog: unknown option %q", argument)
		default:
			positional = append(positional, argument)
		}
	}

	if len(positional) == 0 {
		return parsed, nil
	}
	switch positional[0] {
	case "list", "ls":
		// Deliberately not accepted as a synonym for the default. `list` used to
		// mean "list Issues", and silently redefining it to mean "list the
		// board" would answer a different question than the one that was asked.
		return backlogArgs{}, errors.New(
			"backlog: list moved — ./scripts/backlog.sh now reads the project board's Ready column, " +
				"and Issues are listed by: ./scripts/issue.sh")
	case "view", "show":
		return backlogArgs{}, errors.New(
			"backlog: view moved to ./scripts/issue.sh view <number|url>")
	case "add", "new":
		return backlogArgs{}, errors.New(
			"backlog: add moved to ./scripts/issue.sh add \"<title>\"")
	default:
		return backlogArgs{}, fmt.Errorf("backlog: unknown subcommand %q", positional[0])
	}
}

// backlog runs one `./scripts/backlog.sh` invocation.
//
// Exit codes are the contract scripts branch on: 0 when the board was read, 1
// when GitHub could not answer, and 2 when the invocation itself was wrong. An
// empty Ready column is a completed operation.
func (a *App) backlog(ctx context.Context, opts options, args []string) int {
	parsed, err := parseBacklogArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	if parsed.json {
		opts.json = true
	}

	client, err := a.githubClient(opts)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	repository, err := client.ResolveRepository(ctx)
	if err != nil {
		a.writeBacklogError(err, opts.json)
		return 1
	}
	project, err := client.ResolveLinkedProject(ctx, repository)
	if err != nil {
		a.writeBacklogError(err, opts.json)
		return 1
	}
	board, err := client.ListProjectItems(ctx, repository, project)
	if err != nil {
		a.writeBacklogError(err, opts.json)
		return 1
	}

	if opts.json {
		a.writeResult(true, newBacklogPayload(board))
		return 0
	}
	a.renderBacklog(board, a.listStyle())
	return 0
}

func (a *App) writeBacklogError(err error, asJSON bool) {
	a.writeGitHubError(err, asJSON, backlogSchemaVersion, "./scripts/backlog.sh")
}

// renderBacklog writes the Ready column for a person to read.
//
// One line per card with the rank first, because the rank is the whole point:
// this is read to answer "what do I do now", and the answer is the top row.
// The justification sits under it dimmed, so the column of titles stays
// scannable while the reasoning is still there for the one you stop on.
func (a *App) renderBacklog(board github.ProjectBoard, style listStyle) {
	ready := board.Ready()

	a.out("%sOri backlog%s — %s%s%s — %s%s%s — %s\n",
		style.bold, style.reset,
		style.cyan, board.Repository.Slug(), style.reset,
		style.cyan, board.Project.Title, style.reset,
		countedReady(len(ready)))

	if len(ready) == 0 {
		// Said as an answer, not as a failure: GitHub was reached, the board was
		// read, and nothing on it is groomed yet. A failed read never arrives
		// here — it exits 1 with a reason.
		a.out(
			"\nThe board has nothing in Ready yet. Captured Issues waiting to be groomed: ./scripts/issue.sh\n")
		return
	}

	a.outln()
	for _, item := range ready {
		a.out("%s%-3s%s %s%s%s %s",
			style.bold, rankLabel(item), style.reset,
			style.bold, itemLabel(item), style.reset,
			item.Title)
		if item.Size != "" {
			a.out("  %s[%s]%s", style.dim, item.Size, style.reset)
		}
		a.outln()
		if item.Why != "" {
			a.out("    %s%s%s\n", style.dim, item.Why, style.reset)
		}
	}

	if board.Truncated {
		a.out(
			"\n%sMore cards are on this board than this listing reads; showing the first %d.%s\n",
			style.dim, len(board.Items), style.reset)
	}
}

// rankLabel renders an unranked card as `-` rather than as 0, which reads like
// a rank somebody chose.
func rankLabel(item github.ProjectItem) string {
	if !item.HasRank {
		return "-"
	}
	return fmt.Sprintf("%d", item.Rank)
}

// itemLabel distinguishes a proposal from an Issue at a glance. A draft card is
// the grooming agent's own suggestion — often one spanning several Issues — and
// showing it with a number it does not have would invite someone to go looking
// for an Issue that was never opened.
func itemLabel(item github.ProjectItem) string {
	if item.IsDraft {
		return "[draft]"
	}
	return fmt.Sprintf("#%-5d", item.Number)
}

func countedReady(count int) string {
	if count == 1 {
		return "1 ready"
	}
	return fmt.Sprintf("%d ready", count)
}

// backlogPayload is the `./scripts/backlog.sh --json` contract.
//
// The facts around `items` exist so a consumer never has to guess what it is
// holding: which repository was read, which board answered, when the read
// happened, and whether the result is the whole Ready column.
type backlogPayload struct {
	SchemaVersion int            `json:"schema_version"`
	Repository    string         `json:"repository"`
	Project       projectPayload `json:"project"`
	// Complete is false only when Truncated cut the listing short. A failed
	// query is reported as an error and never as an incomplete board.
	Complete   bool                 `json:"complete"`
	Truncated  bool                 `json:"truncated"`
	ObservedAt string               `json:"observed_at"`
	Items      []backlogItemPayload `json:"items"`
}

type projectPayload struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

// backlogItemPayload is one Ready card.
//
// Rank, Number, Size, Why and URL are pointers or omitted strings rather than
// zero values, because 0 is a rank somebody could have chosen and an Issue
// numbered 0 does not exist. An absent value is encoded as absent.
type backlogItemPayload struct {
	Rank    *int   `json:"rank"`
	Number  *int   `json:"number"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Size    string `json:"size,omitempty"`
	Why     string `json:"why,omitempty"`
	URL     string `json:"url,omitempty"`
	IsDraft bool   `json:"is_draft"`
}

// newBacklogPayload converts one board read into the wire contract.
//
// The slice is always non-nil, so a board with nothing groomed encodes as
// `"items": []`. A consumer that iterates a JSON array must not have to
// special-case `null` for the most ordinary state this command has.
func newBacklogPayload(board github.ProjectBoard) backlogPayload {
	ready := board.Ready()
	payload := backlogPayload{
		SchemaVersion: backlogSchemaVersion,
		Repository:    board.Repository.Slug(),
		Project: projectPayload{
			Number: board.Project.Number,
			Title:  board.Project.Title,
			URL:    board.Project.URL,
		},
		Complete:   board.Complete,
		Truncated:  board.Truncated,
		ObservedAt: ghTimestamp(board.ObservedAt),
		Items:      make([]backlogItemPayload, 0, len(ready)),
	}
	for _, item := range ready {
		encoded := backlogItemPayload{
			Title:   item.Title,
			Status:  item.Status,
			Size:    item.Size,
			Why:     item.Why,
			URL:     item.URL,
			IsDraft: item.IsDraft,
		}
		if item.HasRank {
			rank := item.Rank
			encoded.Rank = &rank
		}
		if !item.IsDraft {
			number := item.Number
			encoded.Number = &number
		}
		payload.Items = append(payload.Items, encoded)
	}
	return payload
}
