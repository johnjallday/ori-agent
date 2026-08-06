package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/github"
)

// This file owns the `./scripts/devops/issue.sh` command surface: argument parsing,
// human rendering, and the JSON contract below.
//
// The JSON is an Ori-owned envelope, not `gh` output passed through. Whatever
// GitHub adds, renames, or reshapes in its own payloads, a script reading this
// contract keeps working — and the fields it does expose are the ones Ori can
// promise, because Ori decoded and bounded every one of them.
//
// Issues are the record. The ordered view of them lives on a project board,
// read one column at a time under its own separate contract:
// `./scripts/devops/backlog.sh` for what is captured and not yet groomed, and
// `./scripts/devops/ready.sh` for what has been specced and is buildable.

// issueSchemaVersion is the version of every JSON payload `./scripts/devops/issue.sh`
// emits. It stays at 1 across the rename from `backlog.sh`: the shape did not
// change, and raising it would tell every consumer to expect a break that never
// happened.
const issueSchemaVersion = 1

// issueArgs is one parsed invocation.
type issueArgs struct {
	// command is the requested operation. An invocation with no subcommand
	// lists, because listing is what a backlog is for.
	command string
	scope   github.AuthorScope
	json    bool
	// repository is set only when `view` was given a full Issue URL, and is
	// checked against the resolved repository before anything is read.
	repository github.Repository
	number     int
	title      string
	body       string
}

// parseIssueArgs validates an invocation completely before anything runs.
//
// Nothing here contacts GitHub, and that is deliberate: a typo should cost a
// message, not a network round trip against somebody's rate limit — and for
// `add`, a misread invocation would create a real Issue somebody has to go and
// close. The parser therefore decides the whole shape of the command — which
// operation, whose Issues, which output — and refuses anything it does not
// recognize.
func parseIssueArgs(args []string) (issueArgs, error) {
	parsed := issueArgs{scope: github.ScopeMe}
	var positional []string
	all := false
	body := ""
	bodyGiven := false

	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--all":
			all = true
		case argument == "--json":
			parsed.json = true
		case argument == "--body":
			// The next token is the body whatever it looks like: a body may
			// legitimately begin with a dash, and silently treating it as a
			// flag would drop the text somebody wrote.
			if index+1 >= len(args) {
				return issueArgs{}, errors.New("issue: --body requires text")
			}
			index++
			body, bodyGiven = args[index], true
		case strings.HasPrefix(argument, "--body="):
			body, bodyGiven = strings.TrimPrefix(argument, "--body="), true
		case strings.HasPrefix(argument, "-"):
			return issueArgs{}, fmt.Errorf("issue: unknown option %q", argument)
		default:
			positional = append(positional, argument)
		}
	}

	command := "list"
	if len(positional) > 0 {
		command = positional[0]
		positional = positional[1:]
	}

	switch command {
	case "list", "ls":
		parsed.command = "list"
		if len(positional) > 0 {
			return issueArgs{}, fmt.Errorf("issue: list takes no arguments, but got %q", positional[0])
		}
	case "view", "show":
		parsed.command = "view"
		if len(positional) == 0 {
			return issueArgs{}, errors.New("issue: view needs an Issue number, for example: ./scripts/devops/issue.sh view 292")
		}
		if len(positional) > 1 {
			return issueArgs{}, fmt.Errorf("issue: view takes one Issue, but also got %q", positional[1])
		}
		repository, number, err := github.ParseIssueReference(positional[0])
		if err != nil {
			return issueArgs{}, fmt.Errorf("issue: %w", err)
		}
		parsed.repository, parsed.number = repository, number
	case "add", "new":
		parsed.command = "add"
		if len(positional) == 0 {
			return issueArgs{}, errors.New(
				"issue: add needs a quoted title, for example: ./scripts/devops/issue.sh add \"Coordinate based map\"")
		}
		if len(positional) > 1 {
			// Almost always an unquoted title. Creating an Issue named after
			// its first word is not a helpful guess.
			return issueArgs{}, fmt.Errorf(
				"issue: add takes one quoted title, but also got %q — quote the whole title", positional[1])
		}
		title, err := github.ValidateTitle(positional[0])
		if err != nil {
			return issueArgs{}, fmt.Errorf("issue: %w", err)
		}
		parsed.title = title
		if bodyGiven {
			validated, err := github.ValidateBody(body)
			if err != nil {
				return issueArgs{}, fmt.Errorf("issue: %w", err)
			}
			parsed.body = validated
		}
	case "sync":
		return issueArgs{}, errors.New(
			"issue: sync was removed — GitHub Issues are read live on every invocation, so there is nothing to sync")
	case "prune":
		return issueArgs{}, errors.New(
			"issue: prune was removed — close an Issue on GitHub instead; GitHub keeps its own history")
	default:
		return issueArgs{}, fmt.Errorf("issue: unknown subcommand %q", command)
	}

	if bodyGiven && parsed.command != "add" {
		return issueArgs{}, fmt.Errorf("issue: --body applies to add, not %s", parsed.command)
	}
	if all {
		// --all widens whose Issues are listed. It has no meaning for one
		// Issue, and accepting it there would suggest it did something.
		if parsed.command != "list" {
			return issueArgs{}, fmt.Errorf("issue: --all applies to list, not %s", parsed.command)
		}
		parsed.scope = github.ScopeAll
	}
	return parsed, nil
}

// issue runs one `./scripts/devops/issue.sh` invocation.
//
// Exit codes are the contract scripts branch on: 0 when the requested
// operation completed, 1 when GitHub could not answer, and 2 when the
// invocation itself was wrong. An empty backlog is a completed operation.
func (a *App) issue(ctx context.Context, opts options, args []string) int {
	parsed, err := parseIssueArgs(args)
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
		a.writeIssueError(err, opts.json)
		return 1
	}

	switch parsed.command {
	case "view":
		return a.issueView(ctx, client, repository, parsed, opts.json)
	case "add":
		return a.issueAdd(ctx, client, repository, parsed, opts.json)
	default:
		return a.issueList(ctx, client, repository, parsed, opts.json)
	}
}

func (a *App) writeIssueError(err error, asJSON bool) {
	a.writeGitHubError(err, asJSON, issueSchemaVersion, "./scripts/devops/issue.sh")
}

func (a *App) issueList(
	ctx context.Context, client *github.Client, repository github.Repository,
	parsed issueArgs, asJSON bool,
) int {
	list, err := client.ListIssues(ctx, repository, parsed.scope)
	if err != nil {
		a.writeIssueError(err, asJSON)
		return 1
	}
	if asJSON {
		a.writeResult(true, newIssueListPayload(list))
		return 0
	}
	a.renderIssueList(list)
	return 0
}

func (a *App) issueView(
	ctx context.Context, client *github.Client, repository github.Repository,
	parsed issueArgs, asJSON bool,
) int {
	// A URL is accepted as a convenience, not as a way to read another
	// repository: it has to name the one this checkout resolves to.
	if !parsed.repository.Empty() && parsed.repository.Slug() != repository.Slug() {
		a.writeError(fmt.Errorf(
			"issue: that Issue URL is for %s, but this checkout is %s",
			parsed.repository.Slug(), repository.Slug()), asJSON)
		return 2
	}

	detail, err := client.ViewIssue(ctx, repository, parsed.number)
	if err != nil {
		a.writeIssueError(err, asJSON)
		return 1
	}
	if asJSON {
		a.writeResult(true, newIssueDetailPayload(repository, detail))
		return 0
	}
	a.renderIssueDetail(detail)
	return 0
}

func (a *App) issueAdd(
	ctx context.Context, client *github.Client, repository github.Repository,
	parsed issueArgs, asJSON bool,
) int {
	created, err := client.CreateIssue(ctx, repository, parsed.title, parsed.body)
	if err != nil {
		a.writeIssueError(err, asJSON)
		return 1
	}
	if asJSON {
		a.writeResult(true, newIssueCreatedPayload(created))
		return 0
	}
	style := a.listStyle()
	a.out("Created %s#%d%s  %s\n%s%s%s\n",
		style.bold, created.Number, style.reset, created.Title,
		style.dim, created.URL, style.reset)
	return 0
}

// renderIssueList writes one listing for a person to read.
//
// The shape is chosen for the job it does: this is the raw capture list, so it
// stays one line per Issue with the number first — the number is what every
// later step is named after. Bodies are not here; `./scripts/devops/issue.sh view`
// exists for the moment you want one.
func (a *App) renderIssueList(list github.IssueList) {
	a.renderIssueListStyled(list, a.listStyle())
}

// renderIssueListStyled is the renderer with its palette supplied, so the
// colored form can be exercised without a terminal.
func (a *App) renderIssueListStyled(list github.IssueList, style listStyle) {
	scope := "by @me"
	if list.Scope == github.ScopeAll {
		scope = "by all authors"
	}

	a.out("%sOri Issues%s — %s%s%s — %s %s\n",
		style.bold, style.reset,
		style.cyan, list.Repository.Slug(), style.reset,
		countedIssues(len(list.Issues)), scope)

	if len(list.Issues) == 0 {
		// The distinction matters: GitHub answered, and the answer was none.
		// A failed query never reaches this branch — it exits 1 with a reason.
		a.out("\nGitHub returned no open Issues %s. Capture one with: ./scripts/devops/issue.sh add \"<title>\"\n", scope)
		return
	}

	a.outln()
	for _, issue := range list.Issues {
		a.out("%s#%-5d%s %s", style.bold, issue.Number, style.reset, issue.Title)
		if secondary := issueRowDetail(issue, list.ObservedAt); secondary != "" {
			a.out("  %s%s%s", style.dim, secondary, style.reset)
		}
		a.outln()
	}

	if list.Truncated {
		a.out(
			"\n%sMore open Issues matched than this listing reads; showing the first %d.%s\n",
			style.dim, len(list.Issues), style.reset)
	}
}

// renderIssueDetail writes one Issue in full.
//
// It prints the Markdown source as text. It does not render HTML, fetch an
// image, follow a link, or download an attachment — an Issue is something
// somebody else wrote, and reading it must not be an action.
func (a *App) renderIssueDetail(detail github.IssueDetail) {
	style := a.listStyle()
	state := string(detail.State)
	if detail.StateReason != "" {
		state += " (" + detail.StateReason + ")"
	}

	a.out("%s#%d  %s%s\n", style.bold, detail.Number, detail.Title, style.reset)
	a.out("%sstate%s    %s\n", style.dim, style.reset, state)
	a.out("%sauthor%s   %s\n", style.dim, style.reset, orPlaceholder(detail.Author, "unknown"))
	a.out("%slabels%s   %s\n", style.dim, style.reset, orPlaceholder(strings.Join(detail.Labels, ", "), "none"))
	a.out("%screated%s  %s\n", style.dim, style.reset, orPlaceholder(ghTimestamp(detail.CreatedAt), "unknown"))
	a.out("%supdated%s  %s\n", style.dim, style.reset, orPlaceholder(ghTimestamp(detail.UpdatedAt), "unknown"))
	if !detail.ClosedAt.IsZero() {
		a.out("%sclosed%s   %s\n", style.dim, style.reset, ghTimestamp(detail.ClosedAt))
	}
	a.out("%surl%s      %s\n", style.dim, style.reset, detail.URL)

	a.outln()
	if strings.TrimSpace(detail.Body) == "" {
		// Said explicitly, because a blank space below the header reads like
		// the command failed to print something.
		a.out("%s(this Issue has no description)%s\n", style.dim, style.reset)
	} else {
		a.outln(detail.Body)
	}
	a.renderIssueSpec(detail, style)
}

// renderIssueSpec writes the grooming agent's research below the Issue.
//
// Nothing is printed when there is none. A placeholder would put "not groomed
// yet" under every Issue in the backlog, which is noise on the common case and
// tells the reader something the board already says more clearly.
func (a *App) renderIssueSpec(detail github.IssueDetail, style listStyle) {
	if strings.TrimSpace(detail.Spec) == "" {
		return
	}
	a.out("\n%s── Agent spec ──%s\n", style.dim, style.reset)
	if detail.SpecDuplicates {
		// The agent edits its comment in place, so more than one means something
		// upstream misbehaved. Saying so beats silently picking one.
		a.out(
			"%s(several spec comments found; showing the most recently updated)%s\n",
			style.dim, style.reset)
	}
	a.outln()
	a.outln(detail.Spec)
}

// issueDetailPayload is the `./scripts/devops/issue.sh view --json` contract: the same
// core identity as a list item, plus the fields that only exist for one Issue.
type issueDetailPayload struct {
	SchemaVersion int    `json:"schema_version"`
	Repository    string `json:"repository"`
	issuePayload
	State       string `json:"state"`
	StateReason string `json:"state_reason,omitempty"`
	ClosedAt    string `json:"closed_at,omitempty"`
	Body        string `json:"body"`
	// Spec is the grooming agent's research, with its marker line removed, and
	// is omitted entirely when the Issue has not been groomed. Adding an
	// optional field does not break a consumer of the existing shape, so the
	// schema version does not move.
	Spec string `json:"spec,omitempty"`
}

func newIssueDetailPayload(repository github.Repository, detail github.IssueDetail) issueDetailPayload {
	labels := detail.Labels
	if labels == nil {
		labels = []string{}
	}
	return issueDetailPayload{
		SchemaVersion: issueSchemaVersion,
		Repository:    repository.Slug(),
		issuePayload: issuePayload{
			Number:    detail.Number,
			Title:     detail.Title,
			Author:    detail.Author,
			Labels:    labels,
			URL:       detail.URL,
			CreatedAt: ghTimestamp(detail.CreatedAt),
			UpdatedAt: ghTimestamp(detail.UpdatedAt),
		},
		State:       string(detail.State),
		StateReason: detail.StateReason,
		ClosedAt:    ghTimestamp(detail.ClosedAt),
		Body:        detail.Body,
		Spec:        detail.Spec,
	}
}

// issueCreatedPayload is the `./scripts/devops/issue.sh add --json` contract. It names
// what was created and where to find it, and nothing else: this command sets no
// metadata, so there is none to report.
type issueCreatedPayload struct {
	SchemaVersion int    `json:"schema_version"`
	Repository    string `json:"repository"`
	Number        int    `json:"number"`
	Title         string `json:"title"`
	URL           string `json:"url"`
	State         string `json:"state"`
}

func newIssueCreatedPayload(created github.CreatedIssue) issueCreatedPayload {
	return issueCreatedPayload{
		SchemaVersion: issueSchemaVersion,
		Repository:    created.Repository.Slug(),
		Number:        created.Number,
		Title:         created.Title,
		URL:           created.URL,
		State:         string(created.State),
	}
}

// issueRowDetail is the bounded secondary text on a row: labels, then how long
// ago the Issue moved. Both come from the listing that was already fetched, so
// a row never costs an extra request.
func issueRowDetail(issue github.Issue, observedAt time.Time) string {
	var parts []string
	if len(issue.Labels) > 0 {
		parts = append(parts, strings.Join(issue.Labels, ", "))
	}
	if age := relativeAge(issue.UpdatedAt, observedAt); age != "" {
		parts = append(parts, age)
	}
	return strings.Join(parts, " · ")
}

// countedIssues keeps the header grammatical for the one-Issue backlog, which
// is a state a new repository spends real time in.
func countedIssues(count int) string {
	if count == 1 {
		return "1 open Issue"
	}
	return fmt.Sprintf("%d open Issues", count)
}

// issueListPayload is the `./scripts/devops/issue.sh --json` contract.
//
// The five facts around `issues` exist so a consumer never has to guess what it
// is holding: which repository was read, whose Issues were selected, which
// lifecycle state was asked for, when the read happened, and whether the result
// is the whole matching set.
type issueListPayload struct {
	SchemaVersion int    `json:"schema_version"`
	Repository    string `json:"repository"`
	AuthorScope   string `json:"author_scope"`
	State         string `json:"state"`
	// Complete is false only when Truncated cut the listing short. A failed
	// query is reported as an error and never as an incomplete list.
	Complete   bool           `json:"complete"`
	Truncated  bool           `json:"truncated"`
	ObservedAt string         `json:"observed_at"`
	Issues     []issuePayload `json:"issues"`
}

// issuePayload is one listed Issue. It carries no body: a backlog collection
// stays bounded, and the body is available through `./scripts/devops/issue.sh view`.
type issuePayload struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	Author    string   `json:"author"`
	Labels    []string `json:"labels"`
	URL       string   `json:"url"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

// newIssueListPayload converts one decoded listing into the wire contract.
//
// Both slices are always non-nil, so an Issue with no labels encodes as `[]`
// and an empty backlog as `"issues": []`. A consumer that iterates a JSON array
// must not have to special-case `null` for the two most ordinary states this
// command has.
func newIssueListPayload(list github.IssueList) issueListPayload {
	payload := issueListPayload{
		SchemaVersion: issueSchemaVersion,
		Repository:    list.Repository.Slug(),
		AuthorScope:   list.Scope.String(),
		State:         string(list.State),
		Complete:      list.Complete,
		Truncated:     list.Truncated,
		ObservedAt:    ghTimestamp(list.ObservedAt),
		Issues:        make([]issuePayload, 0, len(list.Issues)),
	}
	for _, issue := range list.Issues {
		labels := issue.Labels
		if labels == nil {
			labels = []string{}
		}
		payload.Issues = append(payload.Issues, issuePayload{
			Number:    issue.Number,
			Title:     issue.Title,
			Author:    issue.Author,
			Labels:    labels,
			URL:       issue.URL,
			CreatedAt: ghTimestamp(issue.CreatedAt),
			UpdatedAt: ghTimestamp(issue.UpdatedAt),
		})
	}
	return payload
}
