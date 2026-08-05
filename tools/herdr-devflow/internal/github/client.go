// Package github reads pull-request delivery status through the authenticated
// `gh` CLI.
//
// Everything here is read-only: the package lists pull requests and never
// creates, edits, merges, closes, or comments on one. Errors are sanitized
// before they leave the package, because `gh` failures routinely echo tokens,
// headers, and request bodies that must not reach a terminal, a JSON payload,
// or a Herdr board cell.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	// DefaultTimeout bounds one `gh` invocation.
	DefaultTimeout = 20 * time.Second
	// DefaultCandidateLimit bounds how many pull requests are requested.
	DefaultCandidateLimit = 100
	// MaxOutputBytes bounds how much `gh` output is decoded.
	MaxOutputBytes = 8 << 20
	// maxDetailRunes bounds a sanitized diagnostic.
	maxDetailRunes = 200
)

// CheckState aggregates the required checks for one pull request.
type CheckState string

const (
	ChecksNone        CheckState = "none"
	ChecksPassing     CheckState = "passing"
	ChecksPending     CheckState = "pending"
	ChecksFailing     CheckState = "failing"
	ChecksUnavailable CheckState = "unavailable"
)

// PullRequest is the exact remote delivery evidence for one branch.
type PullRequest struct {
	Number int
	URL    string
	Head   string
	Base   string
	Draft  bool
	// State is the normalized remote state: open, closed, or merged.
	State string
	// Merged is true only for a pull request GitHub reports as merged. A
	// closed-unmerged pull request is emphatically not delivered work.
	Merged    bool
	Checks    CheckState
	UpdatedAt time.Time
	MergedAt  time.Time
}

// Result is one repository-wide query outcome.
type Result struct {
	// PullRequests are every decoded candidate, in the order returned.
	PullRequests []PullRequest
	// ObservedAt is when the query completed.
	ObservedAt time.Time
	// Truncated is true when the candidate limit capped the listing.
	Truncated bool
}

// Runner executes a `gh` command and returns its standard output. Injecting it
// keeps this package testable without a network or an authenticated CLI.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Client queries pull requests and Issues for one repository.
type Client struct {
	run              Runner
	dir              string
	timeout          time.Duration
	candidateLimit   int
	issueLimit       int
	projectItemLimit int
}

// Options configures a Client.
type Options struct {
	// Dir is the repository directory `gh` runs in.
	Dir string
	// Run overrides the real `gh` invocation.
	Run Runner
	// Timeout bounds one invocation; zero uses DefaultTimeout.
	Timeout time.Duration
	// CandidateLimit bounds how many pull requests are requested; zero uses
	// DefaultCandidateLimit.
	CandidateLimit int
	// IssueLimit bounds how many Issues one backlog listing requests and
	// decodes; zero uses DefaultIssueLimit. It is separate from CandidateLimit
	// because the two answer different questions: delivery evidence wants every
	// recent pull request, while a backlog wants a list somebody can read.
	IssueLimit int
	// ProjectItemLimit bounds how many board cards one read requests and
	// decodes; zero uses DefaultProjectItemLimit. A board holds every Issue,
	// groomed or not, so it outgrows a single readable column before the Issue
	// list itself does.
	ProjectItemLimit int
}

// New builds a Client, applying bounded defaults.
func New(options Options) *Client {
	client := &Client{
		run:              options.Run,
		dir:              options.Dir,
		timeout:          options.Timeout,
		candidateLimit:   options.CandidateLimit,
		issueLimit:       options.IssueLimit,
		projectItemLimit: options.ProjectItemLimit,
	}
	if client.run == nil {
		client.run = ExecRunner(options.Dir)
	}
	if client.timeout <= 0 {
		client.timeout = DefaultTimeout
	}
	if client.candidateLimit <= 0 {
		client.candidateLimit = DefaultCandidateLimit
	}
	if client.issueLimit <= 0 {
		client.issueLimit = DefaultIssueLimit
	}
	if client.projectItemLimit <= 0 {
		client.projectItemLimit = DefaultProjectItemLimit
	}
	return client
}

// ExecRunner runs the real `gh` binary in dir with a fixed argument vector.
func ExecRunner(dir string) Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		// #nosec G204 -- args are fixed literals composed by this package; no
		// user input reaches the argument vector.
		command := exec.CommandContext(ctx, "gh", args...)
		command.Dir = dir
		return command.Output()
	}
}

// jsonFields are the exact fields the overview needs. Requesting more would
// pull pull-request bodies and comments into memory for no benefit.
var jsonFields = strings.Join([]string{
	"number", "url", "headRefName", "baseRefName",
	"isDraft", "state", "updatedAt", "mergedAt", "statusCheckRollup",
}, ",")

// ListPullRequests performs one fresh authenticated query for every pull
// request targeting base. A single repository-wide call is deliberate: one
// bounded request scales better than one request per feature and cannot storm
// the API when a repository grows.
func (c *Client) ListPullRequests(ctx context.Context, base string) (Result, error) {
	if strings.TrimSpace(base) == "" {
		return Result{}, errors.New("a base branch is required")
	}
	queryCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	output, err := c.run(queryCtx,
		"pr", "list",
		"--state", "all",
		"--base", base,
		"--limit", fmt.Sprint(c.candidateLimit),
		"--json", jsonFields,
	)
	if err != nil {
		return Result{}, classify(queryCtx, err)
	}
	// The raw body is deliberately never included in an error; it can contain
	// anything the remote chose to send.
	result, err := decodeList(output)
	if err != nil {
		return Result{}, err
	}
	result.Truncated = len(result.PullRequests) >= c.candidateLimit
	return result, nil
}

// MaxTargetedLookups bounds how many per-branch queries one collection may
// make when the bulk listing did not cover a feature.
const MaxTargetedLookups = 10

// ListPullRequestsForHead queries the pull requests for one exact head branch.
//
// It exists because the bulk listing is necessarily capped: a repository with
// hundreds of merged pull requests will always have older ones fall outside a
// single page. Rather than raising the cap forever — which just moves the
// problem — a feature the bulk page missed gets one small, targeted query.
func (c *Client) ListPullRequestsForHead(ctx context.Context, base, head string) (Result, error) {
	if strings.TrimSpace(base) == "" || strings.TrimSpace(head) == "" {
		return Result{}, errors.New("a base and head branch are required")
	}
	queryCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	output, err := c.run(queryCtx,
		"pr", "list",
		"--state", "all",
		"--base", base,
		"--head", head,
		"--limit", "10",
		"--json", jsonFields,
	)
	if err != nil {
		return Result{}, classify(queryCtx, err)
	}
	return decodeList(output)
}

func decodeList(output []byte) (Result, error) {
	if len(output) > MaxOutputBytes {
		return Result{}, &Error{Kind: ErrorMalformed, Detail: "the GitHub response was larger than this tool will decode"}
	}
	var decoded []rawPullRequest
	if err := json.Unmarshal(output, &decoded); err != nil {
		return Result{}, &Error{Kind: ErrorMalformed, Detail: "the GitHub response could not be decoded"}
	}
	result := Result{ObservedAt: time.Now()}
	for _, raw := range decoded {
		result.PullRequests = append(result.PullRequests, raw.normalize())
	}
	return result, nil
}

type rawPullRequest struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	HeadRefName string `json:"headRefName"`
	BaseRefName string `json:"baseRefName"`
	IsDraft     bool   `json:"isDraft"`
	State       string `json:"state"`
	UpdatedAt   string `json:"updatedAt"`
	MergedAt    string `json:"mergedAt"`
	Checks      []struct {
		TypeName   string `json:"__typename"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		State      string `json:"state"`
		Conclusion string `json:"conclusion"`
	} `json:"statusCheckRollup"`
}

func (r rawPullRequest) normalize() PullRequest {
	pull := PullRequest{
		Number: r.Number,
		URL:    sanitize(r.URL),
		Head:   sanitize(r.HeadRefName),
		Base:   sanitize(r.BaseRefName),
		Draft:  r.IsDraft,
		State:  strings.ToLower(sanitize(r.State)),
		Checks: aggregate(r),
	}
	pull.Merged = pull.State == "merged"
	if parsed, err := time.Parse(time.RFC3339, r.UpdatedAt); err == nil {
		pull.UpdatedAt = parsed
	}
	if parsed, err := time.Parse(time.RFC3339, r.MergedAt); err == nil {
		pull.MergedAt = parsed
	}
	return pull
}

// aggregate reduces the check rollup to one state. Any failure dominates, then
// anything still running, then success. An empty rollup means no checks ran,
// which is different from checks whose state could not be read.
func aggregate(raw rawPullRequest) CheckState {
	if len(raw.Checks) == 0 {
		return ChecksNone
	}
	pending := false
	passing := 0
	for _, check := range raw.Checks {
		// CheckRun reports status/conclusion; StatusContext reports state.
		status := strings.ToUpper(check.Status)
		conclusion := strings.ToUpper(check.Conclusion)
		state := strings.ToUpper(check.State)

		switch {
		case conclusion == "FAILURE" || conclusion == "TIMED_OUT" || conclusion == "STARTUP_FAILURE" || state == "FAILURE" || state == "ERROR":
			return ChecksFailing
		case conclusion == "CANCELLED":
			return ChecksFailing
		case status != "" && status != "COMPLETED":
			pending = true
		case state == "PENDING" || state == "EXPECTED":
			pending = true
		case conclusion == "SUCCESS" || state == "SUCCESS":
			passing++
		case conclusion == "SKIPPED" || conclusion == "NEUTRAL":
			// Skipped and neutral checks neither pass nor block.
			continue
		default:
			pending = true
		}
	}
	switch {
	case pending:
		return ChecksPending
	case passing > 0:
		return ChecksPassing
	default:
		return ChecksNone
	}
}

// ErrorKind classifies a failed query so callers can give actionable advice
// without inspecting sanitized prose.
type ErrorKind string

const (
	ErrorMissing         ErrorKind = "gh_missing"
	ErrorUnauthenticated ErrorKind = "gh_unauthenticated"
	ErrorTimeout         ErrorKind = "gh_timeout"
	ErrorNetwork         ErrorKind = "gh_network"
	ErrorMalformed       ErrorKind = "gh_malformed"
	// ErrorRepository means no GitHub repository could be resolved for the
	// current checkout, or the resolved one cannot be read. It is separate from
	// a network failure because the fix is different: check where you are and
	// what you have access to, not whether github.com is reachable.
	ErrorRepository ErrorKind = "gh_repository"
	// ErrorForbidden means the credential is valid but is not allowed to do
	// this. Reporting it as "unauthenticated" would send someone to log in
	// again, which they already did.
	ErrorForbidden ErrorKind = "gh_forbidden"
	// ErrorRateLimit means GitHub is refusing further requests for now. It is
	// the one failure where waiting genuinely is the fix.
	ErrorRateLimit ErrorKind = "gh_rate_limit"
	// ErrorNotFound means the repository or Issue named does not exist, or is
	// invisible to this credential — GitHub does not distinguish the two.
	ErrorNotFound ErrorKind = "gh_not_found"
	// ErrorProjectScope means the credential is valid but carries none of the
	// scopes ProjectsV2 requires. It is separate from ErrorForbidden because the
	// fix is a token edit, not a permission grant, and separate from
	// ErrorUnauthenticated because logging in again changes nothing.
	ErrorProjectScope ErrorKind = "gh_project_scope"
	// ErrorProjectMissing means no project board is linked to the repository, so
	// there is no backlog to read.
	ErrorProjectMissing ErrorKind = "gh_project_missing"
	// ErrorProjectAmbiguous means several boards are linked and none can be
	// chosen without guessing. Guessing here reads the wrong backlog silently,
	// which is worse than refusing.
	ErrorProjectAmbiguous ErrorKind = "gh_project_ambiguous"
)

// Error is a sanitized query failure. It never carries raw `gh` output.
type Error struct {
	Kind   ErrorKind
	Detail string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Kind) + ": " + e.Detail
}

// Recovery is the operator action that most likely fixes this failure. Each
// one is a command or a decision the reader can act on immediately; "try again
// later" is only offered where waiting really is the fix.
func (e *Error) Recovery() string {
	switch e.Kind {
	case ErrorMissing:
		return "install the GitHub CLI: https://cli.github.com"
	case ErrorUnauthenticated:
		return "run: gh auth login"
	case ErrorForbidden:
		return "check that this account may act on the repository: gh auth status"
	case ErrorRateLimit:
		return "wait for the GitHub rate limit to reset, then retry; check it with: gh api rate_limit"
	case ErrorNotFound:
		return "check the repository and Issue number, then retry"
	case ErrorProjectScope:
		// gh's own advice here is `gh auth refresh -s ...`, which cannot work
		// when the credential comes from the GITHUB_TOKEN environment variable:
		// there is no stored OAuth token for it to refresh, and it refuses
		// outright. Naming the token page instead is the difference between a
		// fix and a loop.
		return "add the project scopes to the token at https://github.com/settings/tokens " +
			"(read:project, read:org, read:discussion); if gh suggests `gh auth refresh`, " +
			"that cannot work while GITHUB_TOKEN is set — edit the token itself, its value does not change"
	case ErrorProjectMissing:
		return "link the board to this repository: gh project link <number> --owner <login> --repo <owner>/<name>"
	case ErrorProjectAmbiguous:
		return "unlink every board but the one that is the backlog: gh project unlink <number> --owner <login> --repo <owner>/<name>"
	case ErrorTimeout, ErrorNetwork:
		return "check network access to github.com, then retry"
	case ErrorRepository:
		return "run this from a checkout of a GitHub repository you can read; verify with: gh repo view"
	default:
		return "run: gh pr list --limit 1"
	}
}

// These patterns are matched against stderr only to choose a failure class.
// The stderr text itself is then discarded: it is the single most likely place
// for a token, an authorization header, or a request body to appear.
//
// Order matters where the phrases overlap. A rate-limit refusal is also an HTTP
// 403, and reporting it as an authorization problem would send someone to check
// permissions that are fine.
var (
	// Checked before authMessage, and the order is load-bearing: gh phrases the
	// scope refusal as "your authentication token is missing required scopes",
	// which authMessage matches. Classified that way it would send someone to
	// `gh auth login`, which succeeds and changes nothing, because the token is
	// valid — it simply lacks the project scopes.
	projectScopeMessage = regexp.MustCompile(
		`(?i)(missing required scopes|read:project|read:org|read:discussion)`)
	authMessage = regexp.MustCompile(
		`(?i)(gh auth login|not logged in|bad credentials|requires authentication|` +
			`authentication token|missing required token|HTTP 401|401 Unauthorized)`)
	rateLimitMessage = regexp.MustCompile(
		`(?i)(rate limit|rate-limit|abuse detection|secondary limit|too many requests|HTTP 429)`)
	forbiddenMessage = regexp.MustCompile(
		`(?i)(HTTP 403|403 Forbidden|forbidden|resource not accessible|` +
			`must have (admin|push|write)|SAML enforcement|SSO)`)
	notFoundMessage = regexp.MustCompile(
		`(?i)(HTTP 404|404 Not Found|could not resolve to|no issues found|not found)`)
	networkMessage = regexp.MustCompile(
		`(?i)(dial tcp|no such host|connection refused|connection reset|` +
			`network is unreachable|i/o timeout|failed to verify certificate|` +
			`x509|TLS|proxy|EOF|HTTP 5\d\d|server error)`)
)

// classify converts a raw exec failure into a sanitized Error.
func classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return &Error{Kind: ErrorTimeout, Detail: "the GitHub query did not finish before its timeout"}
	}
	if errors.Is(err, exec.ErrNotFound) {
		return &Error{Kind: ErrorMissing, Detail: "the GitHub CLI (gh) is not installed or not on PATH"}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return classifyStderr(exitErr.Stderr)
	}
	return &Error{Kind: ErrorNetwork, Detail: "the GitHub query could not be run"}
}

// classifyStderr chooses a failure class from `gh` diagnostics. Every returned
// detail is written here, from the class alone; none of it is copied out of the
// text being matched.
func classifyStderr(stderr []byte) *Error {
	switch {
	case projectScopeMessage.Match(stderr):
		return &Error{
			Kind:   ErrorProjectScope,
			Detail: "this GitHub token does not carry the scopes ProjectsV2 reads require",
		}
	case authMessage.Match(stderr):
		return &Error{Kind: ErrorUnauthenticated, Detail: "the GitHub CLI is not authenticated for this repository"}
	case rateLimitMessage.Match(stderr):
		return &Error{Kind: ErrorRateLimit, Detail: "GitHub is rate limiting this account right now"}
	case forbiddenMessage.Match(stderr):
		return &Error{Kind: ErrorForbidden, Detail: "this GitHub account is not allowed to perform that operation"}
	case notFoundMessage.Match(stderr):
		return &Error{Kind: ErrorNotFound, Detail: "GitHub has no such repository or Issue for this account"}
	case networkMessage.Match(stderr):
		return &Error{Kind: ErrorNetwork, Detail: "the GitHub query could not reach github.com"}
	default:
		return &Error{Kind: ErrorNetwork, Detail: "the GitHub query failed"}
	}
}

// sanitize strips control characters and bounds a remote-supplied value.
// Branch names, Issue titles, and URLs come from the network and are rendered
// in terminals.
func sanitize(value string) string {
	return boundedText(value, maxDetailRunes)
}

// boundedText is sanitize with an explicit bound, for remote fields whose
// readable length differs from a diagnostic's — an Issue title is given more
// room than a label, and both are given less than a body.
//
// Two classes of character are removed. ASCII and C1 controls carry terminal
// escape sequences, which is how remote text repaints a screen it was only
// supposed to appear on. Bidirectional overrides and line separators are
// removed for a quieter reason: they reorder or split a line without being
// visible, so a title can be made to read as something it is not.
func boundedText(value string, limit int) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r < 32, r == 127:
			return -1
		case r >= 0x80 && r <= 0x9f:
			return -1
		case r == 0x200e, r == 0x200f:
			return -1
		case r >= 0x202a && r <= 0x202e:
			return -1
		case r >= 0x2066 && r <= 0x2069:
			return -1
		case r == 0x2028, r == 0x2029:
			return -1
		default:
			return r
		}
	}, value)
	runes := []rune(strings.TrimSpace(cleaned))
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return string(runes)
}
