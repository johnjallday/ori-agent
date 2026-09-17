package integrationrelease

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

var (
	floorCommit  = strings.Repeat("a", 40)
	commit062    = strings.Repeat("b", 40)
	tagObject062 = strings.Repeat("c", 40)
	commit070    = strings.Repeat("d", 40)
)

func testEntry() reviewedintegration.Entry {
	return reviewedintegration.Entry{
		Key: "example_integration", PluginID: "example-plugin", MinimumVersion: "0.6.1",
		SourceRepository: "https://github.com/example/example-plugin",
		FallbackCommit:   floorCommit, SourceFormat: plugin.FormatClaude,
		ExpectedBlueprintID: "example-song", MinimumBlueprintVersion: 7,
		ExpectedProgramID: "example-program", ExpectedProgramSchema: 2, ExpectedProtocol: 1,
		RequiredHostFeatures: []string{plugin.HostFeatureAssistantProgramV1},
		SupportedPlatforms:   []string{"darwin/arm64"}, ReleaseReady: true,
	}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

type releasesServer struct {
	server  *httptest.Server
	calls   atomic.Int32
	mu      sync.Mutex
	status  int
	body    string
	headers http.Header
}

func newReleasesServer(t *testing.T, body string) *releasesServer {
	t.Helper()
	fake := &releasesServer{status: http.StatusOK, body: body}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

func (fake *releasesServer) serve(w http.ResponseWriter, r *http.Request) {
	fake.calls.Add(1)
	fake.mu.Lock()
	fake.headers = r.Header.Clone()
	status, body := fake.status, fake.body
	fake.mu.Unlock()
	if r.URL.Path != "/repos/example/example-plugin/releases" || r.URL.Query().Get("per_page") != "30" {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (fake *releasesServer) set(status int, body string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.status, fake.body = status, body
}

const annotatedAndLightweightTags = "" +
	floorCommitLine + "\trefs/tags/v0.6.1\n" +
	"cccccccccccccccccccccccccccccccccccccccc\trefs/tags/v0.6.2\n" +
	"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\trefs/tags/v0.6.2^{}\n" +
	"dddddddddddddddddddddddddddddddddddddddd\trefs/tags/0.7.0\n"

const floorCommitLine = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func staticTags(output string, calls *atomic.Int32) TagLister {
	return func(_ context.Context, repository string) ([]byte, error) {
		if calls != nil {
			calls.Add(1)
		}
		if repository != "https://github.com/example/example-plugin" {
			return nil, errors.New("unexpected repository")
		}
		return []byte(output), nil
	}
}

func newTestResolver(fake *releasesServer, tags TagLister, clock *fakeClock) *Resolver {
	return New(fake.server.URL, fake.server.Client(), tags, clock.Now)
}

func TestResolvePicksHighestStableReleaseAtOrAboveTheFloor(t *testing.T) {
	fake := newReleasesServer(t, `[
		{"tag_name":"v0.9.0","draft":true,"prerelease":false},
		{"tag_name":"v0.8.0","draft":false,"prerelease":true},
		{"tag_name":"v0.7.1-rc.1","draft":false,"prerelease":false},
		{"tag_name":"nightly","draft":false,"prerelease":false},
		{"tag_name":"v0.6.2","draft":false,"prerelease":false},
		{"tag_name":"0.7.0","draft":false,"prerelease":false},
		{"tag_name":"v0.6.1","draft":false,"prerelease":false},
		{"tag_name":"v0.5.9","draft":false,"prerelease":false}
	]`)
	clock := &fakeClock{now: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)}
	got := newTestResolver(fake, staticTags(annotatedAndLightweightTags, nil), clock).Resolve(context.Background(), testEntry())
	want := Resolution{
		Version: "0.7.0", Tag: "0.7.0", Commit: commit070,
		Source: "https://github.com/example/example-plugin#sha=" + commit070, CheckedAt: clock.Now(),
	}
	if got != want {
		t.Fatalf("resolution = %#v, want %#v", got, want)
	}
}

func TestResolveUsesPeeledCommitForAnnotatedTag(t *testing.T) {
	fake := newReleasesServer(t, `[{"tag_name":"v0.6.2","draft":false,"prerelease":false}]`)
	clock := &fakeClock{now: time.Now()}
	got := newTestResolver(fake, staticTags(annotatedAndLightweightTags, nil), clock).Resolve(context.Background(), testEntry())
	if got.Version != "0.6.2" || got.Tag != "v0.6.2" || got.Commit != commit062 || got.Commit == tagObject062 || got.Fallback {
		t.Fatalf("annotated tag was not peeled: %#v", got)
	}
}

func TestResolveFallsBackToTheFloor(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		tags   TagLister
	}{
		"server error":         {status: http.StatusForbidden, body: `{"message":"rate limited"}`},
		"malformed json":       {status: http.StatusOK, body: `{"not":"a list"}`},
		"only below the floor": {status: http.StatusOK, body: `[{"tag_name":"v0.6.0","draft":false,"prerelease":false}]`},
		"only non-semver tags": {status: http.StatusOK, body: `[{"tag_name":"latest","draft":false,"prerelease":false}]`},
		"tag not in repository": {status: http.StatusOK, body: `[{"tag_name":"v0.6.9","draft":false,"prerelease":false}]`,
			tags: staticTags(annotatedAndLightweightTags, nil)},
		"malformed commit": {status: http.StatusOK, body: `[{"tag_name":"v0.6.2","draft":false,"prerelease":false}]`,
			tags: staticTags("BBBB\trefs/tags/v0.6.2\n", nil)},
		"ls-remote fails": {status: http.StatusOK, body: `[{"tag_name":"v0.6.2","draft":false,"prerelease":false}]`,
			tags: func(context.Context, string) ([]byte, error) { return nil, errors.New("offline") }},
	}
	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newReleasesServer(t, item.body)
			fake.set(item.status, item.body)
			tags := item.tags
			if tags == nil {
				tags = staticTags(annotatedAndLightweightTags, nil)
			}
			got := newTestResolver(fake, tags, &fakeClock{now: time.Now()}).Resolve(context.Background(), testEntry())
			want := Resolution{
				Version: "0.6.1", Commit: floorCommit, Fallback: true,
				Source: "https://github.com/example/example-plugin#sha=" + floorCommit,
			}
			if got != want {
				t.Fatalf("resolution = %#v, want floor %#v", got, want)
			}
		})
	}
}

func TestResolveUnreachableAPIFallsBackToTheFloor(t *testing.T) {
	resolver := New("http://127.0.0.1:9", nil, staticTags(annotatedAndLightweightTags, nil), time.Now)
	got := resolver.Resolve(context.Background(), testEntry())
	if !got.Fallback || got.Version != "0.6.1" || got.Commit != floorCommit {
		t.Fatalf("unreachable API did not fall back to the floor: %#v", got)
	}
}

func TestResolvePendingEntryNeverTouchesTheNetwork(t *testing.T) {
	fake := newReleasesServer(t, `[{"tag_name":"v0.6.2","draft":false,"prerelease":false}]`)
	var tagCalls atomic.Int32
	entry := testEntry()
	entry.ReleaseReady = false
	got := newTestResolver(fake, staticTags(annotatedAndLightweightTags, &tagCalls), &fakeClock{now: time.Now()}).
		Resolve(context.Background(), entry)
	if got.Source != "" || got.Commit != "" || !got.Fallback || fake.calls.Load() != 0 || tagCalls.Load() != 0 {
		t.Fatalf("pending entry resolved a source: %#v api=%d tags=%d", got, fake.calls.Load(), tagCalls.Load())
	}
}

func TestResolveCachesForAnHourAndRefetchesAfterExpiry(t *testing.T) {
	fake := newReleasesServer(t, `[{"tag_name":"v0.6.2","draft":false,"prerelease":false}]`)
	var tagCalls atomic.Int32
	clock := &fakeClock{now: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)}
	resolver := newTestResolver(fake, staticTags(annotatedAndLightweightTags, &tagCalls), clock)
	first := resolver.Resolve(context.Background(), testEntry())
	clock.Advance(CacheTTL - time.Second)
	fake.set(http.StatusOK, `[{"tag_name":"0.7.0","draft":false,"prerelease":false}]`)
	cached := resolver.Resolve(context.Background(), testEntry())
	if cached != first || fake.calls.Load() != 1 || tagCalls.Load() != 1 {
		t.Fatalf("cache hit made network calls: %#v api=%d tags=%d", cached, fake.calls.Load(), tagCalls.Load())
	}
	clock.Advance(time.Second)
	refreshed := resolver.Resolve(context.Background(), testEntry())
	if refreshed.Version != "0.7.0" || !refreshed.CheckedAt.Equal(clock.Now()) || fake.calls.Load() != 2 {
		t.Fatalf("expired cache did not refetch: %#v api=%d", refreshed, fake.calls.Load())
	}
}

func TestResolveFailureKeepsTheLastResultAndBacksOff(t *testing.T) {
	fake := newReleasesServer(t, `[{"tag_name":"v0.6.2","draft":false,"prerelease":false}]`)
	clock := &fakeClock{now: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)}
	resolver := newTestResolver(fake, staticTags(annotatedAndLightweightTags, nil), clock)
	first := resolver.Resolve(context.Background(), testEntry())
	clock.Advance(CacheTTL)
	fake.set(http.StatusServiceUnavailable, `{}`)
	kept := resolver.Resolve(context.Background(), testEntry())
	if kept != first || kept.Fallback || fake.calls.Load() != 2 {
		t.Fatalf("failure did not keep the last result with its CheckedAt: %#v first=%#v", kept, first)
	}
	clock.Advance(FailureRetryInterval - time.Second)
	if again := resolver.Resolve(context.Background(), testEntry()); again != first || fake.calls.Load() != 2 {
		t.Fatalf("failing source was retried before the backoff: api=%d", fake.calls.Load())
	}
	clock.Advance(time.Second)
	fake.set(http.StatusOK, `[{"tag_name":"0.7.0","draft":false,"prerelease":false}]`)
	if recovered := resolver.Resolve(context.Background(), testEntry()); recovered.Version != "0.7.0" || fake.calls.Load() != 3 {
		t.Fatalf("recovered source was not used: %#v api=%d", recovered, fake.calls.Load())
	}
}

func TestResolveNeverChoosesBelowTheFloorWhenReleasesDisappear(t *testing.T) {
	fake := newReleasesServer(t, `[{"tag_name":"v0.6.2","draft":false,"prerelease":false}]`)
	clock := &fakeClock{now: time.Now()}
	resolver := newTestResolver(fake, staticTags(annotatedAndLightweightTags, nil), clock)
	_ = resolver.Resolve(context.Background(), testEntry())
	clock.Advance(CacheTTL)
	fake.set(http.StatusOK, `[{"tag_name":"v0.5.0","draft":false,"prerelease":false}]`)
	if got := resolver.Resolve(context.Background(), testEntry()); got.Version != "0.6.2" {
		t.Fatalf("resolver moved below the last known release: %#v", got)
	}
	// A different floor is a different cache entry.
	raised := testEntry()
	raised.MinimumVersion = "0.6.3"
	if got := resolver.Resolve(context.Background(), raised); !got.Fallback || got.Version != "0.6.3" {
		t.Fatalf("raised floor reused a resolution below it: %#v", got)
	}
}

func TestResolveSharesOneLookupAcrossConcurrentCallers(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		<-release
		_, _ = w.Write([]byte(`[{"tag_name":"v0.6.2","draft":false,"prerelease":false}]`))
	}))
	t.Cleanup(server.Close)
	resolver := New(server.URL, server.Client(), staticTags(annotatedAndLightweightTags, nil), time.Now)
	results := make(chan Resolution, 8)
	var started sync.WaitGroup
	for range 8 {
		started.Add(1)
		go func() {
			started.Done()
			results <- resolver.Resolve(context.Background(), testEntry())
		}()
	}
	started.Wait()
	time.Sleep(50 * time.Millisecond)
	close(release)
	for range 8 {
		if got := <-results; got.Version != "0.6.2" {
			t.Fatalf("concurrent caller got %#v", got)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent callers made %d lookups, want 1", calls.Load())
	}
}

func TestResolveSlowServerHitsTheBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(server.Close)
	resolver := New(server.URL, server.Client(), staticTags(annotatedAndLightweightTags, nil), time.Now)
	resolver.budget = 100 * time.Millisecond
	started := time.Now()
	got := resolver.Resolve(context.Background(), testEntry())
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("lookup ignored its budget: %s", elapsed)
	}
	if !got.Fallback || got.Version != "0.6.1" {
		t.Fatalf("budget expiry did not fall back to the floor: %#v", got)
	}
}

func TestResolveCanceledCallerDoesNotRecordAFailure(t *testing.T) {
	fake := newReleasesServer(t, `[{"tag_name":"v0.6.2","draft":false,"prerelease":false}]`)
	resolver := newTestResolver(fake, staticTags(annotatedAndLightweightTags, nil), &fakeClock{now: time.Now()})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := resolver.Resolve(canceled, testEntry()); !got.Fallback {
		t.Fatalf("canceled caller resolved a release: %#v", got)
	}
	if got := resolver.Resolve(context.Background(), testEntry()); got.Version != "0.6.2" {
		t.Fatalf("canceled caller suppressed the next lookup: %#v", got)
	}
}

func TestResolveSendsTokenOnlyWhenSetAndOnlyOverHTTPS(t *testing.T) {
	var authorization atomic.Value
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization.Store(r.Header.Get("Authorization"))
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") != "ori-agent" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`[{"tag_name":"v0.6.2","draft":false,"prerelease":false}]`))
	})
	secure := httptest.NewTLSServer(handler)
	t.Cleanup(secure.Close)
	plain := httptest.NewServer(handler)
	t.Cleanup(plain.Close)
	resolve := func(server *httptest.Server) Resolution {
		return New(server.URL, server.Client(), staticTags(annotatedAndLightweightTags, nil), time.Now).
			Resolve(context.Background(), testEntry())
	}

	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	if got := resolve(secure); got.Fallback || authorization.Load() != "" {
		t.Fatalf("unset token sent a header or request failed: %#v auth=%q", got, authorization.Load())
	}
	t.Setenv("GH_TOKEN", "gh-secret")
	if got := resolve(secure); got.Fallback || authorization.Load() != "Bearer gh-secret" {
		t.Fatalf("GH_TOKEN was not sent: %#v auth=%q", got, authorization.Load())
	}
	t.Setenv("GITHUB_TOKEN", "github-secret")
	if got := resolve(secure); got.Fallback || authorization.Load() != "Bearer github-secret" {
		t.Fatalf("GITHUB_TOKEN did not take precedence: %#v auth=%q", got, authorization.Load())
	}
	if got := resolve(plain); got.Fallback || authorization.Load() != "" {
		t.Fatalf("token was sent over plain http: %#v auth=%q", got, authorization.Load())
	}
}

// Candidates exists so a caller can walk down from the newest release when it
// cannot load one, so the order, the floor at the end, and the cap are the
// contract — not just the first entry.
func TestCandidatesAreOrderedNewestFirstAndEndAtTheFloor(t *testing.T) {
	fake := newReleasesServer(t, `[
		{"tag_name":"v0.6.2","draft":false,"prerelease":false},
		{"tag_name":"0.7.0","draft":false,"prerelease":false},
		{"tag_name":"v0.6.1","draft":false,"prerelease":false},
		{"tag_name":"v0.5.9","draft":false,"prerelease":false},
		{"tag_name":"v0.8.0","draft":false,"prerelease":true}
	]`)
	clock := &fakeClock{now: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)}
	entry := testEntry()
	got := newTestResolver(fake, staticTags(annotatedAndLightweightTags, nil), clock).Candidates(context.Background(), entry)

	want := []Resolution{
		{Version: "0.7.0", Tag: "0.7.0", Commit: commit070, Source: entry.PinnedSource(commit070), CheckedAt: clock.Now()},
		{Version: "0.6.2", Tag: "v0.6.2", Commit: commit062, Source: entry.PinnedSource(commit062), CheckedAt: clock.Now()},
		Floor(entry),
	}
	if len(got) != len(want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("candidate %d = %#v, want %#v", index, got[index], want[index])
		}
	}
	// v0.6.1 is the floor's own version: offering it twice would spend an
	// inspection to learn the same thing.
	if got[len(got)-1].Version != entry.MinimumVersion || !got[len(got)-1].Fallback {
		t.Fatalf("the list does not end at the floor: %#v", got)
	}
}

func TestCandidatesNeverExceedTheCap(t *testing.T) {
	var releases, tags strings.Builder
	releases.WriteString("[")
	tags.WriteString(floorCommitLine + "\trefs/tags/v0.6.1\n")
	for index := 0; index < 12; index++ {
		version := "0.7." + strconv.Itoa(index)
		if index > 0 {
			releases.WriteString(",")
		}
		releases.WriteString(`{"tag_name":"v` + version + `","draft":false,"prerelease":false}`)
		tags.WriteString(strings.Repeat(string(rune('a'+index%6)), 40) + "\trefs/tags/v" + version + "\n")
	}
	releases.WriteString("]")

	fake := newReleasesServer(t, releases.String())
	got := newTestResolver(fake, staticTags(tags.String(), nil), &fakeClock{now: time.Now()}).
		Candidates(context.Background(), testEntry())
	if len(got) != MaxCandidates {
		t.Fatalf("candidates = %d, want the cap of %d", len(got), MaxCandidates)
	}
	if !got[len(got)-1].Fallback {
		t.Fatalf("the capped list does not end at the floor: %#v", got)
	}
	if got[0].Version != "0.7.11" {
		t.Fatalf("the capped list dropped the newest release: %#v", got[0])
	}
}

// A failed lookup answers with the floor alone, exactly as Resolve does.
func TestCandidatesFallBackToTheFloorAlone(t *testing.T) {
	fake := newReleasesServer(t, `{"not":"a list"}`)
	got := newTestResolver(fake, staticTags(annotatedAndLightweightTags, nil), &fakeClock{now: time.Now()}).
		Candidates(context.Background(), testEntry())
	if len(got) != 1 || got[0] != Floor(testEntry()) {
		t.Fatalf("candidates = %#v, want the floor alone", got)
	}
}

// One lookup serves both entry points, so a caller that reads the list pays no
// more network than one that wants only the newest.
func TestCandidatesAndResolveShareOneLookup(t *testing.T) {
	fake := newReleasesServer(t, `[{"tag_name":"0.7.0","draft":false,"prerelease":false},{"tag_name":"v0.6.2","draft":false,"prerelease":false}]`)
	var tagCalls atomic.Int32
	clock := &fakeClock{now: time.Now()}
	resolver := newTestResolver(fake, staticTags(annotatedAndLightweightTags, &tagCalls), clock)

	candidates := resolver.Candidates(context.Background(), testEntry())
	newest := resolver.Resolve(context.Background(), testEntry())
	if fake.calls.Load() != 1 || tagCalls.Load() != 1 {
		t.Fatalf("two entry points made %d api and %d tag calls", fake.calls.Load(), tagCalls.Load())
	}
	if newest != candidates[0] {
		t.Fatalf("Resolve = %#v, want the newest candidate %#v", newest, candidates[0])
	}
}

func TestGitHubRepositoryAcceptsOnlyOwnerAndRepository(t *testing.T) {
	for source, want := range map[string]bool{
		"https://github.com/example/example-plugin":        true,
		"https://github.com/example/example-plugin.git":    true,
		"https://github.com/example":                       false,
		"https://github.com/example/example-plugin/nested": false,
		"https://github.com/../example-plugin":             false,
		"https://example.invalid/example/example-plugin":   false,
		"https://github.com/example/example plugin":        false,
	} {
		if _, _, ok := githubRepository(source); ok != want {
			t.Errorf("githubRepository(%q) ok = %v, want %v", source, ok, want)
		}
	}
}
