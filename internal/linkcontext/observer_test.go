package linkcontext

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)

type stubRepos struct {
	repo Repo
	err  error
	seen []string
}

func (s *stubRepos) Resolve(_ context.Context, owner, name string) (Repo, error) {
	s.seen = append(s.seen, owner+"/"+name)
	if s.err != nil {
		return Repo{}, s.err
	}
	repo := s.repo
	repo.Owner, repo.Name, repo.FullName = owner, name, owner+"/"+name
	return repo, nil
}

type stubPages struct {
	page Page
	err  error
	seen []string
}

func (s *stubPages) Fetch(_ context.Context, target *url.URL) (Page, error) {
	s.seen = append(s.seen, target.String())
	if s.err != nil {
		return Page{}, s.err
	}
	page := s.page
	if page.URL == "" {
		page.URL = target.String()
	}
	return page, nil
}

func testObserver(repos RepoResolver, pages PageFetcher) *Observer {
	return NewObserver(repos, pages).WithClock(func() time.Time { return fixedNow }, func() string { return "link-1" })
}

func TestObserve_ARepositoryGoesToTheResolverAndNeverToAFetcher(t *testing.T) {
	repos := &stubRepos{repo: Repo{Description: "Widgets", Language: "Go", OpenIssues: 3, Reachable: ReachableYes}}
	pages := &stubPages{}
	observation, err := testObserver(repos, pages).Observe(context.Background(), "git@github.com:acme/widgets.git")
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if len(pages.seen) != 0 || strings.Join(repos.seen, ",") != "acme/widgets" {
		t.Fatalf("routing: repos=%v pages=%v", repos.seen, pages.seen)
	}
	if observation.Kind != KindGitHubRepo || observation.URL != "https://github.com/acme/widgets" || observation.Host != "github.com" {
		t.Fatalf("observation: %+v", observation)
	}
	if observation.Title != "acme/widgets" || observation.Description != "Widgets" || !observation.Readable || observation.Repo == nil || observation.Repo.OpenIssues != 3 {
		t.Fatalf("repository facts: %+v %+v", observation, observation.Repo)
	}
	if observation.ID != "link-1" || !observation.FetchedAt.Equal(fixedNow) || observation.DisplayName() != "acme/widgets" {
		t.Fatalf("identity: %+v", observation)
	}
	if err := observation.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestObserve_AnUnreachableRepositoryStillAttachesByName(t *testing.T) {
	repos := &stubRepos{repo: Repo{Reachable: ReachableNo}}
	observation, err := testObserver(repos, nil).Observe(context.Background(), "https://github.com/acme/private-thing/issues/4")
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if observation.Readable || observation.Repo.Reachable != ReachableNo || observation.Repo.FullName != "acme/private-thing" || observation.Description != "" {
		t.Fatalf("unreachable repository: %+v %+v", observation, observation.Repo)
	}
	// No resolver at all: the same, as unknown.
	observation, err = testObserver(nil, nil).Observe(context.Background(), "https://github.com/acme/widgets")
	if err != nil || observation.Repo.Reachable != ReachableUnknown || observation.Repo.FullName != "acme/widgets" {
		t.Fatalf("no resolver: %+v %v", observation, err)
	}
}

func TestObserve_APageGoesToTheFetcherWithACanonicalAddress(t *testing.T) {
	pages := &stubPages{page: Page{Kind: KindPage, Title: "  An\narticle ", Description: "About things", Readable: true}}
	observation, err := testObserver(nil, pages).Observe(context.Background(), "Example.COM/Posts/one?x=1#section")
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if strings.Join(pages.seen, ",") != "https://example.com/Posts/one?x=1" {
		t.Fatalf("fetched address: %v", pages.seen)
	}
	if observation.Kind != KindPage || observation.Host != "example.com" || observation.Title != "An article" || !observation.Readable {
		t.Fatalf("observation: %+v", observation)
	}
	if observation.DisplayName() != "An article" {
		t.Fatalf("display name: %q", observation.DisplayName())
	}
}

func TestObserve_WithoutAFetcherAPageAttachesByAddressOnly(t *testing.T) {
	observation, err := testObserver(nil, nil).Observe(context.Background(), "https://example.com/paper.pdf")
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if observation.Readable || observation.Kind != KindPage || observation.Title != "" || observation.DisplayName() != "example.com" {
		t.Fatalf("address-only observation: %+v", observation)
	}
}

func TestObserve_TheFetchersFinalAddressIsCheckedToo(t *testing.T) {
	pages := &stubPages{page: Page{URL: "http://127.0.0.1:8080/admin", Kind: KindPage, Readable: true}}
	_, err := testObserver(nil, pages).Observe(context.Background(), "https://example.com/redirects")
	if !errors.Is(err, ErrURLUnsafe) {
		t.Fatalf("a final address on a private host must be refused, got %v", err)
	}
	pages = &stubPages{err: errors.New("redirect refused")}
	if _, err := testObserver(nil, pages).Observe(context.Background(), "https://example.com/redirects"); !errors.Is(err, ErrURLUnsafe) {
		t.Fatalf("a fetcher refusal must be an unsafe-address refusal, got %v", err)
	}
}

func TestObserve_RefusesUnsafeAddressesBeforeAnythingIsFetched(t *testing.T) {
	cases := map[string]error{
		"":                                   ErrURLEmpty,
		"   ":                                ErrURLEmpty,
		strings.Repeat("a", MaxURLLength+1):  ErrURLTooLong,
		"ftp://example.com/file":             ErrURLUnsafe,
		"file:///etc/passwd":                 ErrURLUnsafe,
		"javascript:alert(1)":                ErrURLUnsafe,
		"http://localhost/admin":             ErrURLUnsafe,
		"http://127.0.0.1/":                  ErrURLUnsafe,
		"http://10.0.0.1/":                   ErrURLUnsafe,
		"http://192.168.1.1/":                ErrURLUnsafe,
		"http://169.254.169.254/latest/meta": ErrURLUnsafe,
		"http://[::1]/":                      ErrURLUnsafe,
		"https://user:pass@example.com/":     ErrURLUnsafe,
		"https://example.com/doc?token=abc123def456":        ErrURLUnsafe,
		"https://example.com/file?X-Amz-Signature=deadbeef": ErrURLUnsafe,
		"https://example.com:8443/page":                     ErrURLUnsafe,
		"https://intranet.internal/wiki":                    ErrURLUnsafe,
	}
	for raw, want := range cases {
		repos := &stubRepos{}
		pages := &stubPages{page: Page{Kind: KindPage, Readable: true}}
		_, err := testObserver(repos, pages).Observe(context.Background(), raw)
		if !errors.Is(err, want) {
			t.Errorf("Observe(%q) = %v, want %v", raw, err, want)
		}
		if len(repos.seen)+len(pages.seen) != 0 {
			t.Errorf("Observe(%q) fetched something: repos=%v pages=%v", raw, repos.seen, pages.seen)
		}
		if message, ok := Refusal(err); !ok || message == "" {
			t.Errorf("Observe(%q): no user-readable refusal for %v", raw, err)
		}
	}
}

func TestObserve_ARepositoryThroughTheRealResolverNeverCarriesTheReadme(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(repoBody())
	}))
	t.Cleanup(server.Close)
	resolver := NewGitHubResolver(server.Client(), nil).WithBase(server.URL)
	observation, err := testObserver(resolver, nil).Observe(context.Background(), "https://github.com/acme/widgets")
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	data, _ := json.Marshal(observation)
	if strings.Contains(string(data), readmeSentinel) {
		t.Fatalf("the README sentinel reached the observation: %s", data)
	}
	if len(data) > MaxBytes {
		t.Fatalf("observation is %d bytes, cap is %d", len(data), MaxBytes)
	}
}

func TestObservationValidate_EnforcesTheCaps(t *testing.T) {
	base := func() Observation {
		return Observation{Version: Version, ID: "id", URL: "https://example.com/a", Host: "example.com", Kind: KindPage, Title: "t", FetchedAt: fixedNow}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("a minimal page observation must validate: %v", err)
	}
	long := base()
	long.Title = strings.Repeat("é", MaxTextRunes+1)
	if err := long.Validate(); err == nil {
		t.Fatal("a title over the rune cap must fail")
	}
	ctrl := base()
	ctrl.Description = "has\x00control"
	if err := ctrl.Validate(); err == nil {
		t.Fatal("control characters must fail")
	}
	kind := base()
	kind.Kind = Kind("readme")
	if err := kind.Validate(); err == nil {
		t.Fatal("an unknown kind must fail")
	}
	repoless := base()
	repoless.Kind = KindGitHubRepo
	if err := repoless.Validate(); err == nil {
		t.Fatal("a repository kind without repo facts must fail")
	}
	topics := base()
	topics.Kind = KindGitHubRepo
	topics.Repo = &Repo{Owner: "a", Name: "b", FullName: "a/b", Reachable: ReachableYes, Topics: make([]string, MaxTopics+1)}
	for i := range topics.Repo.Topics {
		topics.Repo.Topics[i] = "t"
	}
	if err := topics.Validate(); err == nil {
		t.Fatal("more than MaxTopics must fail")
	}
	badDate := base()
	badDate.Kind = KindGitHubRepo
	badDate.Repo = &Repo{Owner: "a", Name: "b", FullName: "a/b", Reachable: ReachableYes, PushedAt: "2026-10-15T08:30:00Z"}
	if err := badDate.Validate(); err == nil {
		t.Fatal("a pushed_at that is not a bare date must fail")
	}
	if Text("  a\tb\n\x07c  ") != "a b c" {
		t.Fatalf("Text: %q", Text("  a\tb\n\x07c  "))
	}
}
