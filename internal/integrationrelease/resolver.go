// Package integrationrelease resolves the release a reviewed integration
// installs: the stable GitHub releases of the reviewed repository at or above
// the registry floor, newest first. Resolve answers with the newest of them;
// Candidates hands over the ordered list, for a caller that must check whether
// this build can actually load a release before settling on it. A failed lookup
// never blocks setup; it yields the last known releases, then the floor's
// compiled fallback commit.
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
	"sort"
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
	// MaxCandidates bounds how many releases one read may be offered, the floor
	// included. A caller that walks the list inspecting each one would otherwise
	// turn a single repository with a long release history into many fetches.
	MaxCandidates = 5

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
	lookup sync.Mutex
	// last is the ordered candidate list from the newest successful lookup,
	// newest release first, every entry at or above the floor. Empty means no
	// lookup has succeeded and the floor is all there is.
	last      []Resolution
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
	resolved := r.resolved(ctx, entry, floor)
	if len(resolved) == 0 {
		return floor
	}
	return resolved[0]
}

// Candidates returns the releases a caller may try, newest first. Every entry
// is at or above the entry's floor, and the list always ends with the floor
// source, so a caller that rejects everything newer still has the one release
// this build was reviewed against. When the published floor release was found,
// that final candidate retains its checked-release evidence rather than being
// mislabeled as a fallback.
//
// It exists because "the newest release" and "the newest release this build can
// load" are not the same thing. A release may require a host feature, protocol,
// platform, or blueprint version this build does not have; the caller is the
// one that can tell, so the resolver hands it the ordered list rather than a
// single answer it would have to refuse.
//
// The list never exceeds MaxCandidates, and it is never empty: a pending entry
// yields its (sourceless) floor, which the caller refuses as it always has.
func (r *Resolver) Candidates(ctx context.Context, entry reviewedintegration.Entry) []Resolution {
	floor := Floor(entry)
	resolved := r.resolved(ctx, entry, floor)
	candidates := make([]Resolution, 0, MaxCandidates)
	floorCandidate := floor
	for _, candidate := range resolved {
		if candidate.Source == floor.Source {
			// Keep the live release evidence, but put the floor last so callers
			// can continue trying newer compatible releases first.
			floorCandidate = candidate
			continue
		}
		if len(candidates)+1 < MaxCandidates {
			candidates = append(candidates, candidate)
		}
	}
	return append(candidates, floorCandidate)
}

// resolved returns the cached or freshly looked-up candidate list, newest
// first. An empty result means the floor is all there is.
func (r *Resolver) resolved(ctx context.Context, entry reviewedintegration.Entry, floor Resolution) []Resolution {
	if r == nil || floor.Source == "" {
		return nil
	}
	owner, repository, ok := githubRepository(entry.SourceRepository)
	if !ok {
		return nil
	}
	cached := r.cacheFor(entry)
	cached.lookup.Lock()
	defer cached.lookup.Unlock()
	now := r.now()
	if now.Before(cached.nextCheck) {
		return cached.last
	}
	lookupCtx, cancel := context.WithTimeout(ctx, r.budget)
	defer cancel()
	resolutions, err := r.lookup(lookupCtx, entry, owner, repository)
	if err != nil {
		if ctx.Err() != nil {
			// The caller gave up; that says nothing about the release source.
			return cached.last
		}
		// The raw error is never logged: request URLs may carry credentials.
		logger.Warn("Integration release lookup failed; using the last known release or the reviewed minimum", logger.Fields{
			"integration": entry.Key, "stage": lookupStage(err),
		})
		cached.nextCheck = now.Add(FailureRetryInterval)
		return cached.last
	}
	for index := range resolutions {
		resolutions[index].CheckedAt = now
	}
	cached.last = resolutions
	cached.nextCheck = now.Add(CacheTTL)
	return resolutions
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

func (r *Resolver) lookup(ctx context.Context, entry reviewedintegration.Entry, owner, repository string) ([]Resolution, error) {
	releases, err := r.releases(ctx, owner, repository)
	if err != nil {
		return nil, err
	}
	ordered := stableReleasesAtOrAboveFloor(releases, entry.MinimumVersion)
	if len(ordered) == 0 {
		return nil, stageError{"no_candidate"}
	}
	// The commits always come from the reviewed repository itself, so a
	// development API override can only choose among official tags. One listing
	// covers every candidate.
	output, err := r.listTags(ctx, entry.SourceRepository)
	if err != nil {
		return nil, stageError{"tag_lookup"}
	}
	resolutions := make([]Resolution, 0, MaxCandidates)
	for _, candidate := range ordered {
		commit, ok := commitForTag(output, candidate.tag)
		if !ok {
			if len(resolutions) == 0 {
				// The newest release names a tag this repository does not have,
				// which says the two sources disagree. Reporting that as a
				// lookup failure keeps the last known release rather than
				// quietly answering with an older one.
				return nil, stageError{"tag_commit"}
			}
			continue
		}
		resolutions = append(resolutions, Resolution{
			Version: candidate.version, Tag: candidate.tag, Commit: commit, Source: entry.PinnedSource(commit),
		})
		if len(resolutions) == MaxCandidates {
			break
		}
	}
	return resolutions, nil
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

// releaseCandidate is one stable release of the reviewed repository, before its
// commit is known.
type releaseCandidate struct {
	version string
	tag     string
}

// stableReleasesAtOrAboveFloor orders the installable releases highest first.
// Drafts, prereleases (by flag or by a semantic-version prerelease suffix) and
// tags that are not registry versions after removing a leading "v" are skipped,
// as are duplicate versions: two tags for one version would otherwise offer the
// same release twice.
func stableReleasesAtOrAboveFloor(releases []githubRelease, minimum string) []releaseCandidate {
	candidates := make([]releaseCandidate, 0, len(releases))
	seen := make(map[string]struct{}, len(releases))
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}
		version := strings.TrimPrefix(release.TagName, "v")
		if !reviewedintegration.StableVersion(version) || !reviewedintegration.AtLeast(version, minimum) {
			continue
		}
		if _, duplicate := seen[version]; duplicate {
			continue
		}
		seen[version] = struct{}{}
		candidates = append(candidates, releaseCandidate{version: version, tag: release.TagName})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		order, _ := reviewedintegration.CompareVersions(candidates[i].version, candidates[j].version)
		return order > 0
	})
	return candidates
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
