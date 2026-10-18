package linkcontext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/urlsafety"
)

// GitHubAPIBase is the REST API the resolver reads. It is a fixed constant,
// never user-supplied, so calls to it need no SSRF validation of their own.
const GitHubAPIBase = "https://api.github.com"

const (
	githubHost        = "github.com"
	githubTimeout     = 10 * time.Second
	githubMaxBody     = 64 << 10
	githubUserAgent   = "ori-agent/link-context"
	githubAcceptValue = "application/vnd.github+json"
)

// A repository segment as GitHub accepts it: letters, digits, dot, dash and
// underscore. A name may end in ".git" when written as a clone URL.
var repoSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ParseGitHubRepo reduces a repository address to its owner and name. It
// accepts https://github.com/owner/name with or without "www.", with or
// without ".git", with any trailing path (/tree/main/x, /issues, /pull/12), and
// the git@github.com:owner/name.git form. Anything that is not exactly a
// repository (an owner page, a gist, another host) reports ok == false.
func ParseGitHubRepo(raw string) (owner, name string, ok bool) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" || len(candidate) > MaxURLLength {
		return "", "", false
	}
	if rest, found := strings.CutPrefix(candidate, "git@github.com:"); found {
		return repoParts(rest)
	}
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", "", false
	}
	if scheme := strings.ToLower(parsed.Scheme); scheme != "http" && scheme != "https" {
		return "", "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != githubHost && host != "www."+githubHost {
		return "", "", false
	}
	return repoParts(strings.TrimPrefix(parsed.EscapedPath(), "/"))
}

// repoParts takes "owner/name[.git][/anything]" and validates both segments.
func repoParts(path string) (owner, name string, ok bool) {
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 {
		return "", "", false
	}
	owner, name = strings.TrimSpace(parts[0]), strings.TrimSuffix(strings.TrimSpace(parts[1]), ".git")
	if !ValidRepoName(owner, name) {
		return "", "", false
	}
	return owner, name, true
}

// ValidRepoName checks an owner and a name each on its own, so a slash in
// either can never turn into an extra path segment.
func ValidRepoName(owner, name string) bool {
	if owner == "" || name == "" || !repoSegment.MatchString(owner) || !repoSegment.MatchString(name) {
		return false
	}
	return !strings.EqualFold(owner, "gist") && !strings.EqualFold(owner, "orgs") && !strings.EqualFold(owner, "settings")
}

// RepoURL is the canonical address of a repository.
func RepoURL(owner, name string) string {
	return "https://" + githubHost + "/" + owner + "/" + name
}

// RepoResolver asks GitHub about one repository. It never fails for a
// repository that merely cannot be seen: that is Reachable "no" or "unknown".
type RepoResolver interface {
	Resolve(ctx context.Context, owner, name string) (Repo, error)
}

// TokenSource hands the resolver the stored GitHub token when there is one
// the user can use right now (connected, vault unlocked). ok == false means
// the call goes out anonymously.
type TokenSource func(ctx context.Context) (token string, ok bool)

// GitHubResolver reads GET /repos/{owner}/{name}: the repository's own
// description and facts, never its contents.
type GitHubResolver struct {
	client *http.Client
	token  TokenSource
	base   string
}

// NewGitHubResolver builds a resolver on a safe transport. A nil client gets
// one with the package timeout; a nil token source means always anonymous.
func NewGitHubResolver(client *http.Client, token TokenSource) *GitHubResolver {
	if client == nil {
		client = &http.Client{Timeout: githubTimeout, Transport: urlsafety.NewSafeTransport()}
	}
	return &GitHubResolver{client: client, token: token, base: GitHubAPIBase}
}

// WithBase points the resolver at another API base. Tests use it for a fixture
// server; production never calls it.
func (r *GitHubResolver) WithBase(base string) *GitHubResolver {
	r.base = strings.TrimRight(base, "/")
	return r
}

type githubRepoResponse struct {
	FullName      string   `json:"full_name"`
	Description   *string  `json:"description"`
	Language      *string  `json:"language"`
	Topics        []string `json:"topics"`
	DefaultBranch string   `json:"default_branch"`
	Private       bool     `json:"private"`
	Archived      bool     `json:"archived"`
	OpenIssues    int      `json:"open_issues_count"`
	PushedAt      string   `json:"pushed_at"`
}

// Resolve returns what GitHub says. A 404 or a plain 403 is a repository the
// caller cannot see (Reachable "no"); a rate limit, a bad token, a transport
// failure, or an unreadable body is "unknown". The owner and name are kept in
// every case so the link still attaches.
func (r *GitHubResolver) Resolve(ctx context.Context, owner, name string) (Repo, error) {
	repo := Repo{Owner: owner, Name: name, FullName: owner + "/" + name, Reachable: ReachableUnknown}
	if !ValidRepoName(owner, name) {
		return repo, fmt.Errorf("%w: repository name", ErrInvalid)
	}
	if r == nil || r.client == nil {
		return repo, nil
	}
	endpoint := r.base + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return repo, nil
	}
	req.Header.Set("Accept", githubAcceptValue)
	req.Header.Set("User-Agent", githubUserAgent)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if r.token != nil {
		if token, ok := r.token(ctx); ok && strings.TrimSpace(token) != "" {
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		}
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return repo, nil
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound:
		repo.Reachable = ReachableNo
		return repo, nil
	case resp.StatusCode == http.StatusForbidden && !rateLimited(resp):
		repo.Reachable = ReachableNo
		return repo, nil
	default:
		// 401 (a bad token), 429 or 403 with the limit spent, 5xx: not an
		// answer about the repository.
		return repo, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, githubMaxBody+1))
	if err != nil || len(body) > githubMaxBody {
		return repo, nil
	}
	var decoded githubRepoResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return repo, nil
	}
	repo.Reachable = ReachableYes
	if decoded.Description != nil {
		repo.Description = Text(*decoded.Description)
	}
	if decoded.Language != nil {
		repo.Language = Text(*decoded.Language)
	}
	repo.DefaultBranch = Text(decoded.DefaultBranch)
	repo.Private = decoded.Private
	repo.Archived = decoded.Archived
	if decoded.OpenIssues > 0 {
		repo.OpenIssues = decoded.OpenIssues
	}
	for _, topic := range decoded.Topics {
		topic = Text(topic)
		if topic == "" || utf8RuneCount(topic) > MaxTopicLen {
			continue
		}
		repo.Topics = append(repo.Topics, topic)
		if len(repo.Topics) == MaxTopics {
			break
		}
	}
	if pushed, err := time.Parse(time.RFC3339, decoded.PushedAt); err == nil {
		repo.PushedAt = pushed.UTC().Format("2006-01-02")
	}
	return repo, nil
}

func rateLimited(resp *http.Response) bool {
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if remaining, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("X-RateLimit-Remaining"))); err == nil && remaining == 0 {
		return true
	}
	return resp.Header.Get("Retry-After") != ""
}

// ErrNoRepoResolver marks an observer built without GitHub access; a
// repository link then attaches with its name only.
var ErrNoRepoResolver = errors.New("link context: no repository resolver")
