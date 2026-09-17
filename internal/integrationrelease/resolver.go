// Package integrationrelease resolves the release a reviewed integration
// installs: the latest stable GitHub release of the reviewed repository at or
// above the registry floor. A failed lookup never blocks setup; it returns the
// last known release, then the floor's compiled fallback commit.
package integrationrelease

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

const (
	// DefaultAPIBase is the production releases API.
	DefaultAPIBase = "https://api.github.com"
	// CacheTTL is how long a successful resolution is reused without network I/O.
	CacheTTL = time.Hour
	// FailureRetryInterval spaces lookups while the releases source is failing,
	// so an unreachable API is not retried on every read.
	FailureRetryInterval = 5 * time.Minute
	// LookupBudget bounds one resolution, both network steps together.
	LookupBudget = 10 * time.Second

	releasesPerPage     = 30
	maxReleasesResponse = 4 << 20
	clientTimeout       = 30 * time.Second
)

// Resolution is the release a reviewed integration installs. Source is the
// exact plugin.Manager source, <repository>#sha=<Commit>. Fallback is true when
// the latest release could not be resolved and the compiled floor was used.
type Resolution struct {
	Version   string
	Tag       string
	Commit    string
	Source    string
	CheckedAt time.Time
	Fallback  bool
}

// TagLister returns `git ls-remote --tags` output for a repository.
type TagLister func(ctx context.Context, repository string) ([]byte, error)

// Resolver resolves and caches the latest stable release per registry entry.
// It is safe for concurrent use; concurrent callers for one entry share one
// lookup.
type Resolver struct {
	apiBase  string
	client   *http.Client
	listTags TagLister
	now      func() time.Time
	budget   time.Duration

	mu      sync.Mutex
	entries map[string]*cachedResolution
}

type cachedResolution struct {
	// lookup serializes network lookups for one entry; callers that arrive
	// during a lookup wait for its result instead of starting another.
	lookup    sync.Mutex
	last      *Resolution
	nextCheck time.Time
}

// New builds a resolver. apiBase is the releases API root (DefaultAPIBase in
// production). A nil client, tag lister or clock selects the production
// default.
func New(apiBase string, client *http.Client, listTags TagLister, now func() time.Time) *Resolver {
	if client == nil {
		client = &http.Client{Timeout: clientTimeout}
	}
	if listTags == nil {
		listTags = GitListTags
	}
	if now == nil {
		now = time.Now
	}
	return &Resolver{
		apiBase: strings.TrimSuffix(strings.TrimSpace(apiBase), "/"), client: client,
		listTags: listTags, now: now, budget: LookupBudget,
		entries: make(map[string]*cachedResolution),
	}
}

// GitListTags runs `git ls-remote --tags` against the repository without
// prompting for credentials.
func GitListTags(ctx context.Context, repository string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--tags", "--", repository) // #nosec G204 -- repository is a compiled reviewed registry URL
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.Output()
}

// Floor is the entry's compiled fallback release. A pending entry has no
// source.
func Floor(entry reviewedintegration.Entry) Resolution {
	floor := Resolution{Version: entry.MinimumVersion, Source: entry.FallbackSource(), Fallback: true}
	if floor.Source != "" {
		floor.Commit = entry.FallbackCommit
	}
	return floor
}

// Resolve returns the latest stable release at or above the entry's floor. It
// never returns an error: a failed lookup yields the last successful resolution
// for the entry, or the floor when there is none.
func (r *Resolver) Resolve(ctx context.Context, entry reviewedintegration.Entry) Resolution {
	floor := Floor(entry)
	if r == nil || floor.Source == "" {
		return floor
	}
	owner, repository, ok := githubRepository(entry.SourceRepository)
	if !ok {
		return floor
	}
	cached := r.cacheFor(entry)
	cached.lookup.Lock()
	defer cached.lookup.Unlock()
	now := r.now()
	if now.Before(cached.nextCheck) {
		return cached.current(floor)
	}
	lookupCtx, cancel := context.WithTimeout(ctx, r.budget)
	defer cancel()
	resolution, err := r.lookup(lookupCtx, entry, owner, repository)
	if err != nil {
		if ctx.Err() != nil {
			// The caller gave up; that says nothing about the release source.
			return cached.current(floor)
		}
		// The raw error is never logged: request URLs may carry credentials.
		logger.Warn("Integration release lookup failed; using the last known release or the reviewed minimum", logger.Fields{
			"integration": entry.Key, "stage": lookupStage(err),
		})
		cached.nextCheck = now.Add(FailureRetryInterval)
		return cached.current(floor)
	}
	resolution.CheckedAt = now
	cached.last = &resolution
	cached.nextCheck = now.Add(CacheTTL)
	return resolution
}

func (r *Resolver) cacheFor(entry reviewedintegration.Entry) *cachedResolution {
	// A registry change (repository, floor or fallback) is a different key, so
	// a resolution never outlives the floor it was chosen against.
	key := strings.Join([]string{entry.Key, entry.SourceRepository, entry.MinimumVersion, entry.FallbackCommit}, "\n")
	r.mu.Lock()
	defer r.mu.Unlock()
	cached, ok := r.entries[key]
	if !ok {
		cached = &cachedResolution{}
		r.entries[key] = cached
	}
	return cached
}

func (cached *cachedResolution) current(floor Resolution) Resolution {
	if cached.last != nil {
		return *cached.last
	}
	return floor
}

type stageError struct{ stage string }

func (err stageError) Error() string { return "integration release lookup failed at " + err.stage }

func lookupStage(err error) string {
	var staged stageError
	if errors.As(err, &staged) {
		return staged.stage
	}
	return "unknown"
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

func (r *Resolver) lookup(ctx context.Context, entry reviewedintegration.Entry, owner, repository string) (Resolution, error) {
	releases, err := r.releases(ctx, owner, repository)
	if err != nil {
		return Resolution{}, err
	}
	version, tag, ok := latestStableRelease(releases, entry.MinimumVersion)
	if !ok {
		return Resolution{}, stageError{"no_candidate"}
	}
	// The commit always comes from the reviewed repository itself, so a
	// development API override can only choose among official tags.
	output, err := r.listTags(ctx, entry.SourceRepository)
	if err != nil {
		return Resolution{}, stageError{"tag_lookup"}
	}
	commit, ok := commitForTag(output, tag)
	if !ok {
		return Resolution{}, stageError{"tag_commit"}
	}
	return Resolution{Version: version, Tag: tag, Commit: commit, Source: entry.PinnedSource(commit)}, nil
}

func (r *Resolver) releases(ctx context.Context, owner, repository string) ([]githubRelease, error) {
	endpoint := r.apiBase + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) +
		"/releases?per_page=" + strconv.Itoa(releasesPerPage)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, stageError{"releases_request"}
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "ori-agent")
	// A token is sent only over https, never to a plain-http development API.
	if token := githubToken(); token != "" && request.URL.Scheme == "https" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := r.client.Do(request)
	if err != nil {
		return nil, stageError{"releases_request"}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxReleasesResponse))
		return nil, stageError{"releases_status"}
	}
	var releases []githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, maxReleasesResponse)).Decode(&releases); err != nil {
		return nil, stageError{"releases_decode"}
	}
	return releases, nil
}

// latestStableRelease picks the highest stable release at or above the floor.
// Drafts, prereleases (by flag or by a semantic-version prerelease suffix) and
// tags that are not registry versions after removing a leading "v" are skipped.
func latestStableRelease(releases []githubRelease, minimum string) (version, tag string, ok bool) {
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}
		candidate := strings.TrimPrefix(release.TagName, "v")
		if !reviewedintegration.StableVersion(candidate) || !reviewedintegration.AtLeast(candidate, minimum) {
			continue
		}
		if ok {
			if order, _ := reviewedintegration.CompareVersions(candidate, version); order <= 0 {
				continue
			}
		}
		version, tag, ok = candidate, release.TagName, true
	}
	return version, tag, ok
}

// commitForTag reads one tag's commit from ls-remote output. An annotated tag's
// peeled line names the commit; a lightweight tag's plain line already does.
func commitForTag(output []byte, tag string) (string, bool) {
	var plain, peeled string
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[1] {
		case "refs/tags/" + tag + "^{}":
			peeled = fields[0]
		case "refs/tags/" + tag:
			plain = fields[0]
		}
	}
	commit := peeled
	if commit == "" {
		commit = plain
	}
	return commit, reviewedintegration.ValidCommit(commit)
}

var repositorySegment = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

// githubRepository derives owner and repository from a reviewed
// https://github.com/<owner>/<repository> URL.
func githubRepository(source string) (owner, repository string, ok bool) {
	path, found := strings.CutPrefix(source, "https://github.com/")
	if !found {
		return "", "", false
	}
	parts := strings.Split(strings.TrimSuffix(path, ".git"), "/")
	if len(parts) != 2 || !repositorySegment.MatchString(parts[0]) || !repositorySegment.MatchString(parts[1]) ||
		strings.Trim(parts[0], ".") == "" || strings.Trim(parts[1], ".") == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func githubToken() string {
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		return token
	}
	return strings.TrimSpace(os.Getenv("GH_TOKEN"))
}
