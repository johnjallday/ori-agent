package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// This file holds the repository's product backlog model. GitHub Issues are the
// backlog now, so these types are the one shape every backlog surface reads:
// the human list, the JSON contract, and later the feature-discovery source.
//
// They are deliberately narrow. An Issue arrives from the network and is
// rendered into a terminal, so a value only exists here if a backlog surface
// needs it, and every remote string is bounded and sanitized before it becomes
// one. Bodies are absent by design: a listing that printed them would flood the
// screen and multiply the decoded response size for no gain, so the body lives
// only on the detail type behind `wt backlog view`.

const (
	// DefaultIssueLimit bounds how many Issues one listing requests, decodes,
	// and renders. It is a real ceiling, not a page size: reaching it is
	// reported as truncation rather than silently dropping the remainder.
	DefaultIssueLimit = 100
	// MaxLabelsPerIssue bounds how many labels one row carries. Labels are
	// secondary text on a line meant to stay scannable, and a repository is free
	// to attach far more of them than a terminal row can hold.
	MaxLabelsPerIssue = 10
	// maxTitleRunes bounds one rendered title. Titles are remote text with no
	// length guarantee; a bound keeps one Issue from owning the whole screen.
	maxTitleRunes = 200
	// maxAuthorRunes bounds a rendered author. GitHub logins are far shorter
	// than this, so the bound only ever catches something that is not one.
	maxAuthorRunes = 64
	// maxLabelRunes bounds one label. Labels are secondary text on a row that
	// has to stay scannable.
	maxLabelRunes = 50
	// maxURLRunes bounds a rendered URL.
	maxURLRunes = 300
	// maxRepositoryOutputBytes bounds the repository-resolution response. It
	// carries two short names, so anything larger is not the answer to this
	// question and is refused before it is decoded.
	maxRepositoryOutputBytes = 64 << 10
)

// IssueState is the lifecycle state GitHub reports for an Issue.
type IssueState string

const (
	StateOpen   IssueState = "open"
	StateClosed IssueState = "closed"
)

// AuthorScope selects whose Issues a listing returns.
//
// The default is deliberately narrow. This backlog is the repository owner's
// working list, and an outside contributor's Issue is triage, not a plan for
// today — so the broader scope has to be asked for explicitly.
type AuthorScope string

const (
	// ScopeMe returns only Issues authored by the identity `gh` is
	// authenticated as. It is the default listing scope.
	ScopeMe AuthorScope = "me"
	// ScopeAll removes the author restriction and nothing else: the listing
	// stays open Issues in the current repository.
	ScopeAll AuthorScope = "all"
)

// Valid reports whether the scope is one this package can query.
func (s AuthorScope) Valid() bool {
	return s == ScopeMe || s == ScopeAll
}

func (s AuthorScope) String() string { return string(s) }

// Repository names one GitHub repository as `gh` resolved it for the current
// checkout. It exists so a backlog operation can prove which repository it
// acted on rather than assuming a hard-coded default.
type Repository struct {
	Owner string
	Name  string
}

// Slug is the canonical `owner/name` form used in output and in `--repo`
// arguments.
func (r Repository) Slug() string {
	if r.Empty() {
		return ""
	}
	return r.Owner + "/" + r.Name
}

// Empty reports whether the repository was never resolved. An empty repository
// is never queried: guessing one would silently read somebody else's backlog.
func (r Repository) Empty() bool {
	return strings.TrimSpace(r.Owner) == "" || strings.TrimSpace(r.Name) == ""
}

// ParseRepository reads an `owner/name` slug into a Repository.
//
// It is strict on purpose. The slug decides which repository is read from and
// written to, so anything ambiguous — extra segments, empty halves, a URL, a
// value carrying control characters — is rejected rather than normalized into
// something that happens to resolve.
func ParseRepository(slug string) (Repository, error) {
	cleaned := sanitize(slug)
	owner, name, found := strings.Cut(cleaned, "/")
	if !found {
		return Repository{}, fmt.Errorf("a repository must be written as owner/name")
	}
	owner = strings.TrimSpace(owner)
	name = strings.TrimSpace(name)
	if owner == "" || name == "" || strings.Contains(name, "/") {
		return Repository{}, fmt.Errorf("a repository must be written as owner/name")
	}
	if !repositorySegment(owner) || !repositorySegment(name) {
		return Repository{}, fmt.Errorf("a repository name may use letters, digits, dot, underscore, and hyphen")
	}
	return Repository{Owner: owner, Name: name}, nil
}

// repositorySegment reports whether one half of a slug uses only the character
// set GitHub allows for owners and repository names.
func repositorySegment(value string) bool {
	if value == "" || len(value) > 100 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// ResolveRepository asks the authenticated CLI which GitHub repository the
// client's checkout belongs to.
//
// Resolution is a separate, explicit step rather than an assumption, and that
// is the point of it. A backlog command runs from the source checkout, from
// `dev`, and from any feature worktree; all of them share one repository, and
// `gh` derives it from the checkout's own remotes, so every one of them selects
// the same backlog. Nothing here consults a configured default repository: when
// this call cannot name a repository, the operation stops instead of quietly
// reading somebody else's Issues.
func (c *Client) ResolveRepository(ctx context.Context) (Repository, error) {
	queryCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	output, err := c.run(queryCtx, "repo", "view", "--json", "owner,name")
	if err != nil {
		return Repository{}, classifyRepository(queryCtx, err)
	}
	return decodeRepository(output)
}

func decodeRepository(output []byte) (Repository, error) {
	if len(output) > maxRepositoryOutputBytes {
		return Repository{}, &Error{
			Kind:   ErrorMalformed,
			Detail: "the GitHub repository response was larger than this tool will decode",
		}
	}
	var decoded struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	// The raw body never reaches an error: it is remote text of unknown shape.
	if err := json.Unmarshal(output, &decoded); err != nil {
		return Repository{}, &Error{
			Kind:   ErrorMalformed,
			Detail: "the GitHub repository response could not be decoded",
		}
	}
	repository, err := ParseRepository(decoded.Owner.Login + "/" + decoded.Name)
	if err != nil {
		return Repository{}, &Error{
			Kind:   ErrorRepository,
			Detail: "GitHub did not name a repository for this checkout",
		}
	}
	return repository, nil
}

// classifyRepository keeps the shared failure classes but states the
// repository-resolution case in its own words: the common cause is a directory
// that is not a GitHub-backed checkout, and "the query failed" would send the
// reader looking for a network problem they do not have.
func classifyRepository(ctx context.Context, err error) error {
	classified := classify(ctx, err)
	var remoteErr *Error
	if !errors.As(classified, &remoteErr) {
		return classified
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return classified
	}
	// A generic failure and a 404 mean the same thing for this question: this
	// checkout does not name a repository this account can read. Every other
	// class — no credential, no permission, rate limited, offline — already
	// says something more specific and is left alone.
	unexplained := remoteErr.Kind == ErrorNetwork && !networkMessage.Match(exitErr.Stderr)
	if unexplained || remoteErr.Kind == ErrorNotFound {
		return &Error{
			Kind:   ErrorRepository,
			Detail: "this directory does not resolve to a GitHub repository you can read",
		}
	}
	return classified
}

// Issue is one backlog row: the smallest description of an Issue that supports
// choosing what to work on. It carries no body — see the file comment.
type Issue struct {
	Number int
	Title  string
	Author string
	// Labels are bounded and sanitized; an Issue with none is normal and is not
	// filtered out of the backlog.
	Labels    []string
	URL       string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// issueListFields are the exact fields one backlog row needs. `body` is
// deliberately absent: requesting it would pull every Issue's full description
// into memory to render a list that never prints one.
var issueListFields = strings.Join([]string{
	"number", "title", "author", "labels", "url", "createdAt", "updatedAt",
}, ",")

// ListIssues performs one fresh query for the repository's open Issues.
//
// Three properties are structural rather than filtered after the fact. The
// repository is always named explicitly, so no configured default repository
// can answer instead. The command is `issue list`, which returns Issues only —
// GitHub's search surfaces represent Issues and pull requests together, and a
// pull request in a product backlog is noise at best. And the only difference
// between the two scopes is the author restriction: an all-author listing is
// still this repository's open Issues, never a wider search.
//
// There is no cache. A backlog that answered from a snapshot would eventually
// show work that is already closed, which is worse than showing nothing.
func (c *Client) ListIssues(ctx context.Context, repository Repository, scope AuthorScope) (IssueList, error) {
	if repository.Empty() {
		return IssueList{}, errors.New("a resolved repository is required")
	}
	if !scope.Valid() {
		return IssueList{}, fmt.Errorf("unsupported author scope %q", scope)
	}
	queryCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	args := []string{
		"issue", "list",
		"--repo", repository.Slug(),
		"--state", string(StateOpen),
		"--limit", fmt.Sprint(c.issueLimit),
		"--json", issueListFields,
	}
	if scope == ScopeMe {
		// `@me` is resolved by the authenticated CLI, so the listing follows
		// whoever is logged in rather than a username baked into Ori.
		args = append(args, "--author", "@me")
	}

	output, err := c.run(queryCtx, args...)
	if err != nil {
		return IssueList{}, classify(queryCtx, err)
	}
	issues, capped, err := decodeIssueList(output, c.issueLimit)
	if err != nil {
		return IssueList{}, err
	}

	list := IssueList{
		Repository: repository,
		Scope:      scope,
		State:      StateOpen,
		Issues:     issues,
		ObservedAt: time.Now(),
	}
	// Reaching the bound means GitHub may hold more matching Issues than were
	// read. The listing says so rather than presenting a capped page as the
	// whole backlog.
	list.Truncated = capped
	list.Complete = !capped
	return list, nil
}

// decodeIssueList turns one response into bounded, normalized Issues and
// reports whether the bound cut the listing short.
//
// The limit is applied here as well as in the request. `--limit` is what the
// remote was asked for; this is what the process will hold and render no matter
// what the remote actually sent.
func decodeIssueList(output []byte, limit int) ([]Issue, bool, error) {
	if len(output) > MaxOutputBytes {
		return nil, false, &Error{Kind: ErrorMalformed, Detail: "the GitHub response was larger than this tool will decode"}
	}
	var decoded []rawIssue
	if err := json.Unmarshal(output, &decoded); err != nil {
		return nil, false, &Error{Kind: ErrorMalformed, Detail: "the GitHub response could not be decoded"}
	}
	capped := len(decoded) >= limit
	if len(decoded) > limit {
		decoded = decoded[:limit]
	}
	issues := make([]Issue, 0, len(decoded))
	for _, raw := range decoded {
		issue, ok := raw.normalize()
		if !ok {
			continue
		}
		issues = append(issues, issue)
	}
	sortIssues(issues)
	return issues, capped, nil
}

// sortIssues puts the most recently updated Issue first, breaking ties by
// descending Issue number.
//
// The ordering is imposed here rather than taken from the response because the
// backlog is read to choose what to do next: the thing touched most recently is
// the thing most likely to be on the reader's mind. The tie-breaker exists so
// the answer is the same every time — two Issues updated in the same second
// would otherwise swap places between runs, and a list that reorders itself is
// one nobody can scan by position.
func sortIssues(issues []Issue) {
	sort.SliceStable(issues, func(left, right int) bool {
		if !issues[left].UpdatedAt.Equal(issues[right].UpdatedAt) {
			return issues[left].UpdatedAt.After(issues[right].UpdatedAt)
		}
		return issues[left].Number > issues[right].Number
	})
}

// rawIssue is the decoded `gh` payload. It exists so the exported model never
// has to change shape when GitHub adds a field.
type rawIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	URL       string `json:"url"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// normalize bounds and sanitizes one remote Issue, and reports false for a row
// that must not reach a backlog at all.
//
// Two rows are dropped rather than rendered. One without a positive number is
// not an Issue anyone can open, view, or refer to — there is nothing useful to
// show for it. One whose URL names a pull request is not backlog work: `issue
// list` does not return them, but the model is what every surface reads, so the
// exclusion is enforced where it cannot be bypassed rather than assumed of the
// command that happened to produce the payload.
func (r rawIssue) normalize() (Issue, bool) {
	if r.Number <= 0 {
		return Issue{}, false
	}
	url := boundedText(r.URL, maxURLRunes)
	if isPullRequestURL(url) {
		return Issue{}, false
	}
	issue := Issue{
		Number:    r.Number,
		Title:     boundedText(r.Title, maxTitleRunes),
		Author:    boundedText(r.Author.Login, maxAuthorRunes),
		URL:       url,
		Labels:    make([]string, 0, len(r.Labels)),
		CreatedAt: parseTimestamp(r.CreatedAt),
		UpdatedAt: parseTimestamp(r.UpdatedAt),
	}
	for _, label := range r.Labels {
		// The bound is on what a row renders, not on what the repository is
		// allowed to have: an Issue with forty labels still lists, with the
		// first few shown.
		if len(issue.Labels) >= MaxLabelsPerIssue {
			break
		}
		name := boundedText(label.Name, maxLabelRunes)
		if name == "" {
			continue
		}
		issue.Labels = append(issue.Labels, name)
	}
	return issue, true
}

// isPullRequestURL reports whether a canonical GitHub URL points at a pull
// request. It matches the path segment, so a repository or Issue title
// containing the word "pull" is unaffected.
func isPullRequestURL(url string) bool {
	return strings.Contains(strings.ToLower(url), "/pull/")
}

// parseTimestamp reads one RFC 3339 time, or reports the zero time when the
// remote sent something else. A missing timestamp is stated as missing; it is
// never replaced with "now", which would make a stale Issue look fresh and
// reorder the list.
func parseTimestamp(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// IssueList is the outcome of one fresh listing query.
//
// Every field is evidence about the query itself, because the caller has no
// other way to tell a real empty backlog from a query that quietly returned
// less than the repository holds.
type IssueList struct {
	// Repository is the repository actually queried, not the one requested.
	Repository Repository
	Scope      AuthorScope
	// State is the lifecycle filter that produced this list. V1 lists open
	// Issues only.
	State  IssueState
	Issues []Issue
	// ObservedAt is when the query completed. Listings are never cached, so
	// this is always the moment the data was read from GitHub.
	ObservedAt time.Time
	// Complete is true when every matching Issue was observed. It is false only
	// when the result bound cut the listing short; a failed query is an error,
	// never an incomplete list.
	Complete bool
	// Truncated is true when the result bound capped the listing.
	Truncated bool
}
