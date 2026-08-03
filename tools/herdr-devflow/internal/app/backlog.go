package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/config"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/worktree"
)

// This file owns the `wt backlog` command surface: argument parsing, human
// rendering, and the JSON contract below.
//
// The JSON is an Ori-owned envelope, not `gh` output passed through. Whatever
// GitHub adds, renames, or reshapes in its own payloads, a script reading this
// contract keeps working — and the fields it does expose are the ones Ori can
// promise, because Ori decoded and bounded every one of them.

// backlogSchemaVersion is the version of every JSON payload `wt backlog`
// emits. It starts at 1; any incompatible change to the shape below raises it,
// so a consumer can tell which contract it is holding.
const backlogSchemaVersion = 1

// backlogArgs is one parsed invocation.
type backlogArgs struct {
	// command is the requested operation. An invocation with no subcommand
	// lists, because listing is what a backlog is for.
	command string
	scope   github.AuthorScope
	json    bool
}

// parseBacklogArgs validates an invocation completely before anything runs.
//
// Nothing here contacts GitHub, and that is deliberate: a typo should cost a
// message, not a network round trip against somebody's rate limit. The parser
// therefore decides the whole shape of the command — which operation, whose
// Issues, which output — and refuses anything it does not recognize.
func parseBacklogArgs(args []string) (backlogArgs, error) {
	parsed := backlogArgs{scope: github.ScopeMe}
	command := ""
	all := false

	for _, argument := range args {
		switch {
		case argument == "--all":
			all = true
		case argument == "--json":
			parsed.json = true
		case strings.HasPrefix(argument, "-"):
			return backlogArgs{}, fmt.Errorf("backlog: unknown option %q", argument)
		case command == "":
			command = argument
		default:
			// A second positional means the invocation is ambiguous — most
			// often an unquoted title. Guessing which word was meant is how a
			// command creates the wrong thing.
			return backlogArgs{}, fmt.Errorf("backlog: unexpected argument %q", argument)
		}
	}

	if command == "" {
		command = "list"
	}
	switch command {
	case "list", "ls":
		parsed.command = "list"
	case "sync":
		return backlogArgs{}, errors.New(
			"backlog: sync was removed — GitHub Issues are read live on every invocation, so there is nothing to sync")
	case "prune":
		return backlogArgs{}, errors.New(
			"backlog: prune was removed — close an Issue on GitHub instead; GitHub keeps its own history")
	default:
		return backlogArgs{}, fmt.Errorf("backlog: unknown subcommand %q", command)
	}

	if all {
		parsed.scope = github.ScopeAll
	}
	return parsed, nil
}

// backlog runs one `wt backlog` invocation.
//
// Exit codes are the contract scripts branch on: 0 when the requested
// operation completed, 1 when GitHub could not answer, and 2 when the
// invocation itself was wrong. An empty backlog is a completed operation.
func (a *App) backlog(ctx context.Context, opts options, args []string) int {
	parsed, err := parseBacklogArgs(args)
	if err != nil {
		a.writeError(err, opts.json)
		return 2
	}
	if parsed.json {
		opts.json = true
	}

	client, err := a.backlogClient(opts)
	if err != nil {
		a.writeError(err, opts.json)
		return 1
	}
	repository, err := client.ResolveRepository(ctx)
	if err != nil {
		a.writeBacklogError(err, opts.json)
		return 1
	}

	list, err := client.ListIssues(ctx, repository, parsed.scope)
	if err != nil {
		a.writeBacklogError(err, opts.json)
		return 1
	}
	if opts.json {
		a.writeResult(true, newBacklogListPayload(list))
		return 0
	}
	a.renderBacklogList(list)
	return 0
}

// backlogClient builds the GitHub client for the checkout `wt backlog` was run
// from.
//
// The bridge configuration is read for its bounds when it is present, but a
// missing or unreadable one is not fatal here. Listing a repository's Issues
// has nothing to do with Herdr, and refusing to show the backlog because an
// unrelated file is malformed would be an odd way to find that out.
func (a *App) backlogClient(opts options) (*github.Client, error) {
	lookup := a.withOverrides(opts)
	repoRoot := opts.repoRoot
	if repoRoot == "" {
		if value, ok := lookup(worktree.RepoOverrideEnv); ok && strings.TrimSpace(value) != "" {
			repoRoot = value
		}
	}
	if repoRoot == "" {
		cwd, err := a.getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve working directory: %w", err)
		}
		// Any linked worktree resolves here, and all of them share one
		// repository, so the backlog is the same from every checkout.
		repoRoot, err = worktree.FindRepoRoot(cwd)
		if err != nil {
			return nil, fmt.Errorf("wt backlog must run inside a Git checkout: %w", err)
		}
	}

	settings := config.Default()
	if paths, err := worktree.Resolve(repoRoot, lookup); err == nil {
		if loaded, err := config.Load(paths.ConfigPath, lookup); err == nil {
			settings = loaded
		}
		repoRoot = paths.RepoRoot
	}
	return github.New(github.Options{
		Dir: repoRoot,
		// Run is nil in production, which selects the real `gh` binary.
		Run:        a.githubRunner,
		Timeout:    settings.GitHubTimeout(),
		IssueLimit: github.DefaultIssueLimit,
	}), nil
}

// renderBacklogList writes one listing for a person to read.
//
// The shape is chosen for the job it does: this is the list somebody scans to
// pick today's work, so it stays one line per Issue with the number first —
// the number is what every later step is named after. Bodies are not here;
// `wt backlog view` exists for the moment you want one.
func (a *App) renderBacklogList(list github.IssueList) {
	a.renderBacklogListStyled(list, a.backlogStyle())
}

// renderBacklogListStyled is the renderer with its palette supplied, so the
// colored form can be exercised without a terminal.
func (a *App) renderBacklogListStyled(list github.IssueList, style backlogStyle) {
	scope := "by @me"
	if list.Scope == github.ScopeAll {
		scope = "by all authors"
	}

	fmt.Fprintf(a.stdout, "%sOri backlog%s — %s%s%s — %s %s\n",
		style.bold, style.reset,
		style.cyan, list.Repository.Slug(), style.reset,
		countedIssues(len(list.Issues)), scope)

	if len(list.Issues) == 0 {
		// The distinction matters: GitHub answered, and the answer was none.
		// A failed query never reaches this branch — it exits 1 with a reason.
		fmt.Fprintf(a.stdout, "\nGitHub returned no open Issues %s. Capture one with: wt backlog add \"<title>\"\n", scope)
		return
	}

	fmt.Fprintln(a.stdout)
	for _, issue := range list.Issues {
		fmt.Fprintf(a.stdout, "%s#%-5d%s %s", style.bold, issue.Number, style.reset, issue.Title)
		if secondary := backlogRowDetail(issue, list.ObservedAt); secondary != "" {
			fmt.Fprintf(a.stdout, "  %s%s%s", style.dim, secondary, style.reset)
		}
		fmt.Fprintln(a.stdout)
	}

	if list.Truncated {
		fmt.Fprintf(a.stdout,
			"\n%sMore open Issues matched than this listing reads; showing the first %d.%s\n",
			style.dim, len(list.Issues), style.reset)
	}
}

// backlogRowDetail is the bounded secondary text on a row: labels, then how
// long ago the Issue moved. Both come from the listing that was already
// fetched, so a row never costs an extra request.
func backlogRowDetail(issue github.Issue, observedAt time.Time) string {
	var parts []string
	if len(issue.Labels) > 0 {
		parts = append(parts, strings.Join(issue.Labels, ", "))
	}
	if age := relativeAge(issue.UpdatedAt, observedAt); age != "" {
		parts = append(parts, age)
	}
	return strings.Join(parts, " · ")
}

// relativeAge says how long ago something happened in the coarsest useful
// unit. A backlog is read to judge staleness, and "3 days ago" answers that
// faster than a timestamp does.
func relativeAge(moment, now time.Time) string {
	if moment.IsZero() || now.IsZero() || moment.After(now) {
		return ""
	}
	elapsed := now.Sub(moment)
	switch {
	case elapsed < time.Hour:
		return "updated just now"
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("updated %dh ago", int(elapsed.Hours()))
	case elapsed < 30*24*time.Hour:
		return fmt.Sprintf("updated %dd ago", int(elapsed.Hours()/24))
	default:
		return "updated " + moment.UTC().Format("2006-01-02")
	}
}

// countedIssues keeps the header grammatical for the one-Issue backlog, which
// is a state a new repository spends real time in.
func countedIssues(count int) string {
	if count == 1 {
		return "1 open Issue"
	}
	return fmt.Sprintf("%d open Issues", count)
}

// backlogStyle holds the escape sequences for one invocation, or empty strings
// when the destination is not a terminal.
//
// Piped and redirected output must stay plain: this listing is read by `grep`,
// by `less`, and by whatever a script does with it, and an escape sequence
// none of them asked for is corruption of the data, not decoration.
type backlogStyle struct {
	bold  string
	dim   string
	cyan  string
	reset string
}

func (a *App) backlogStyle() backlogStyle {
	if !a.statusColorEnabled(false) {
		return backlogStyle{}
	}
	return backlogStyle{
		bold:  "\x1b[1m",
		dim:   "\x1b[2m",
		cyan:  "\x1b[36m",
		reset: "\x1b[0m",
	}
}

// writeBacklogError reports a GitHub failure with the one action most likely to
// fix it. The classified error already carries nothing but text this repository
// wrote, so it is safe to print as-is.
func (a *App) writeBacklogError(err error, asJSON bool) {
	var remoteErr *github.Error
	if !errors.As(err, &remoteErr) {
		a.writeError(err, asJSON)
		return
	}
	if asJSON {
		a.writeResult(true, map[string]any{
			"schema_version": backlogSchemaVersion,
			"error": map[string]string{
				"code":     string(remoteErr.Kind),
				"message":  remoteErr.Detail,
				"recovery": remoteErr.Recovery(),
			},
		})
		return
	}
	fmt.Fprintf(a.stderr, "wt backlog: %s\n", remoteErr.Detail)
	if recovery := remoteErr.Recovery(); recovery != "" {
		fmt.Fprintf(a.stderr, "Recovery: %s\n", recovery)
	}
}

// backlogListPayload is the `wt backlog --json` contract.
//
// The five facts around `issues` exist so a consumer never has to guess what it
// is holding: which repository was read, whose Issues were selected, which
// lifecycle state was asked for, when the read happened, and whether the result
// is the whole matching set.
type backlogListPayload struct {
	SchemaVersion int    `json:"schema_version"`
	Repository    string `json:"repository"`
	AuthorScope   string `json:"author_scope"`
	State         string `json:"state"`
	// Complete is false only when Truncated cut the listing short. A failed
	// query is reported as an error and never as an incomplete list.
	Complete   bool                  `json:"complete"`
	Truncated  bool                  `json:"truncated"`
	ObservedAt string                `json:"observed_at"`
	Issues     []backlogIssuePayload `json:"issues"`
}

// backlogIssuePayload is one listed Issue. It carries no body: a backlog
// collection stays bounded, and the body is available through `wt backlog
// view`.
type backlogIssuePayload struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	Author    string   `json:"author"`
	Labels    []string `json:"labels"`
	URL       string   `json:"url"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

// newBacklogListPayload converts one decoded listing into the wire contract.
//
// Both slices are always non-nil, so an Issue with no labels encodes as `[]`
// and an empty backlog as `"issues": []`. A consumer that iterates a JSON array
// must not have to special-case `null` for the two most ordinary states this
// command has.
func newBacklogListPayload(list github.IssueList) backlogListPayload {
	payload := backlogListPayload{
		SchemaVersion: backlogSchemaVersion,
		Repository:    list.Repository.Slug(),
		AuthorScope:   list.Scope.String(),
		State:         string(list.State),
		Complete:      list.Complete,
		Truncated:     list.Truncated,
		ObservedAt:    backlogTimestamp(list.ObservedAt),
		Issues:        make([]backlogIssuePayload, 0, len(list.Issues)),
	}
	for _, issue := range list.Issues {
		labels := issue.Labels
		if labels == nil {
			labels = []string{}
		}
		payload.Issues = append(payload.Issues, backlogIssuePayload{
			Number:    issue.Number,
			Title:     issue.Title,
			Author:    issue.Author,
			Labels:    labels,
			URL:       issue.URL,
			CreatedAt: backlogTimestamp(issue.CreatedAt),
			UpdatedAt: backlogTimestamp(issue.UpdatedAt),
		})
	}
	return payload
}

// backlogTimestamp renders one time as UTC RFC 3339, or as an empty string when
// the remote did not supply it. An absent timestamp is stated as absent rather
// than encoded as the zero year, which reads like a real date from 1 CE.
func backlogTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
