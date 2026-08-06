package app

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
)

// This file owns what `./scripts/devops/backlog.sh` and
// `./scripts/devops/ready.sh` have in common: one bounded read of the project
// board linked to this repository, rendered as a single status column.
//
// The two commands differ only in which column they name and what they say when
// it is empty. Everything else — the GitHub round trip, the JSON contract, the
// ranking, the truncation notice — is identical, and lives here so the two
// columns can never drift into answering the same question two different ways.
//
// Both read and neither writes. Ranking is the grooming agent's job and
// lifecycle is GitHub's; a read command that quietly moved a card would make
// both untrustworthy.

// boardColumn describes one command's view of the board.
//
// It exists so adding a third column later is a value, not another copy of the
// rendering code.
type boardColumn struct {
	// name is the subcommand as typed, used to prefix argument errors.
	name string
	// script is the entrypoint a person actually runs, named in error messages
	// so the fix is a command they can paste rather than a subcommand they have
	// to work out how to reach.
	script string
	// status is the board column this command reads.
	status string
	// schemaVersion versions this command's JSON payload independently, because
	// the two commands are separate contracts that may move apart.
	schemaVersion int
	// selectItems pulls this command's column out of one board read.
	selectItems func(github.ProjectBoard) []github.ProjectItem
	// counted renders the header count in this column's own words.
	counted func(int) string
	// emptyMessage is said when the column is empty. It is phrased as an answer
	// rather than a failure: GitHub was reached and the board was read.
	emptyMessage string
}

// runBoardColumn runs one board-reading command end to end.
//
// Exit codes are the contract scripts branch on: 0 when the board was read, 1
// when GitHub could not answer, and 2 when the invocation itself was wrong. An
// empty column is a completed operation, not a failure.
func (a *App) runBoardColumn(
	ctx context.Context, opts options, column boardColumn, parsed boardArgs,
) int {
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
		a.writeBoardError(err, opts.json, column)
		return 1
	}
	project, err := client.ResolveLinkedProject(ctx, repository)
	if err != nil {
		a.writeBoardError(err, opts.json, column)
		return 1
	}
	board, err := client.ListProjectItems(ctx, repository, project)
	if err != nil {
		a.writeBoardError(err, opts.json, column)
		return 1
	}

	if opts.json {
		a.writeResult(true, newBoardPayload(board, column))
		return 0
	}
	a.renderBoardColumn(board, column, a.listStyle())
	return 0
}

func (a *App) writeBoardError(err error, asJSON bool, column boardColumn) {
	a.writeGitHubError(err, asJSON, column.schemaVersion, column.script)
}

// boardArgs is one parsed invocation of either board command.
type boardArgs struct {
	json bool
}

// renderBoardColumn writes one column for a person to read.
//
// One line per card with the rank first, because the rank is the whole point:
// this is read to answer "what do I do now", and the answer is the top row.
// The justification sits under it dimmed, so the column of titles stays
// scannable while the reasoning is still there for the one you stop on.
func (a *App) renderBoardColumn(
	board github.ProjectBoard, column boardColumn, style listStyle,
) {
	items := column.selectItems(board)

	a.out("%sOri %s%s — %s%s%s — %s%s%s — %s\n",
		style.bold, column.name, style.reset,
		style.cyan, board.Repository.Slug(), style.reset,
		style.cyan, board.Project.Title, style.reset,
		column.counted(len(items)))

	if len(items) == 0 {
		a.out("\n%s\n", column.emptyMessage)
		return
	}

	a.outln()
	for _, item := range items {
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

// boardPayload is the `--json` contract shared by both board commands.
//
// The facts around `items` exist so a consumer never has to guess what it is
// holding: which repository was read, which board answered, which column of it
// this is, when the read happened, and whether the result is the whole column.
type boardPayload struct {
	SchemaVersion int            `json:"schema_version"`
	Repository    string         `json:"repository"`
	Project       projectPayload `json:"project"`
	// Column names the board column these items came from. It is carried
	// explicitly because the two commands emit the same shape from the same
	// board, and the payload alone would otherwise not say which one it is.
	Column string `json:"column"`
	// Complete is false only when Truncated cut the listing short. A failed
	// query is reported as an error and never as an incomplete board.
	Complete   bool               `json:"complete"`
	Truncated  bool               `json:"truncated"`
	ObservedAt string             `json:"observed_at"`
	Items      []boardItemPayload `json:"items"`
}

type projectPayload struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

// boardItemPayload is one card.
//
// Rank, Number, Size, Why and URL are pointers or omitted strings rather than
// zero values, because 0 is a rank somebody could have chosen and an Issue
// numbered 0 does not exist. An absent value is encoded as absent.
type boardItemPayload struct {
	Rank    *int   `json:"rank"`
	Number  *int   `json:"number"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Size    string `json:"size,omitempty"`
	Why     string `json:"why,omitempty"`
	URL     string `json:"url,omitempty"`
	IsDraft bool   `json:"is_draft"`
}

// newBoardPayload converts one board read into the wire contract.
//
// The slice is always non-nil, so an empty column encodes as `"items": []`. A
// consumer that iterates a JSON array must not have to special-case `null` for
// the most ordinary state these commands have.
func newBoardPayload(board github.ProjectBoard, column boardColumn) boardPayload {
	items := column.selectItems(board)
	payload := boardPayload{
		SchemaVersion: column.schemaVersion,
		Repository:    board.Repository.Slug(),
		Project: projectPayload{
			Number: board.Project.Number,
			Title:  board.Project.Title,
			URL:    board.Project.URL,
		},
		Column:     column.status,
		Complete:   board.Complete,
		Truncated:  board.Truncated,
		ObservedAt: ghTimestamp(board.ObservedAt),
		Items:      make([]boardItemPayload, 0, len(items)),
	}
	for _, item := range items {
		encoded := boardItemPayload{
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
