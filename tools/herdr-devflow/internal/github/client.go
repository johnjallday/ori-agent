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

// Client queries pull requests for one repository.
type Client struct {
	run            Runner
	dir            string
	timeout        time.Duration
	candidateLimit int
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
}

// New builds a Client, applying bounded defaults.
func New(options Options) *Client {
	client := &Client{
		run:            options.Run,
		dir:            options.Dir,
		timeout:        options.Timeout,
		candidateLimit: options.CandidateLimit,
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
	if len(output) > MaxOutputBytes {
		return Result{}, &Error{Kind: ErrorMalformed, Detail: "the GitHub response was larger than this tool will decode"}
	}

	var decoded []rawPullRequest
	if err := json.Unmarshal(output, &decoded); err != nil {
		// The raw body is deliberately not included; it can contain anything.
		return Result{}, &Error{Kind: ErrorMalformed, Detail: "the GitHub response could not be decoded"}
	}

	result := Result{ObservedAt: time.Now(), Truncated: len(decoded) >= c.candidateLimit}
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

// Recovery is the operator action that most likely fixes this failure.
func (e *Error) Recovery() string {
	switch e.Kind {
	case ErrorMissing:
		return "install the GitHub CLI: https://cli.github.com"
	case ErrorUnauthenticated:
		return "run: gh auth login"
	case ErrorTimeout, ErrorNetwork:
		return "check network access to github.com, then retry"
	default:
		return "run: gh pr list --limit 1"
	}
}

// authMessage matches the phrases `gh` uses for an unusable credential. It is
// matched against stderr only to classify the failure; the stderr text itself
// is discarded.
var authMessage = regexp.MustCompile(`(?i)(auth|credential|login|token|permission|HTTP 401|HTTP 403|not logged)`)

// classify converts a raw exec failure into a sanitized Error. The original
// stderr is examined here and then dropped: it is the single most likely place
// for a token to appear.
func classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return &Error{Kind: ErrorTimeout, Detail: "the GitHub query did not finish before its timeout"}
	}
	if errors.Is(err, exec.ErrNotFound) {
		return &Error{Kind: ErrorMissing, Detail: "the GitHub CLI (gh) is not installed or not on PATH"}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if authMessage.Match(exitErr.Stderr) {
			return &Error{Kind: ErrorUnauthenticated, Detail: "the GitHub CLI is not authenticated for this repository"}
		}
		return &Error{Kind: ErrorNetwork, Detail: "the GitHub query failed"}
	}
	return &Error{Kind: ErrorNetwork, Detail: "the GitHub query could not be run"}
}

// sanitize strips control characters and bounds a remote-supplied value.
// Branch names and URLs come from the network and are rendered in terminals.
func sanitize(value string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	runes := []rune(strings.TrimSpace(cleaned))
	if len(runes) > maxDetailRunes {
		return string(runes[:maxDetailRunes]) + "…"
	}
	return string(runes)
}
