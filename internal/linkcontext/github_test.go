package linkcontext

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseGitHubRepo_EveryRepositoryForm(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/widgets":                    "acme/widgets",
		"http://github.com/acme/widgets":                     "acme/widgets",
		"https://www.github.com/acme/widgets":                "acme/widgets",
		"github.com/acme/widgets":                            "acme/widgets",
		"https://github.com/acme/widgets.git":                "acme/widgets",
		"https://github.com/acme/widgets/":                   "acme/widgets",
		"https://github.com/acme/widgets/tree/main/internal": "acme/widgets",
		"https://github.com/acme/widgets/issues":             "acme/widgets",
		"https://github.com/acme/widgets/pull/12":            "acme/widgets",
		"https://github.com/acme/widgets?tab=readme":         "acme/widgets",
		"https://github.com/acme/widgets#readme":             "acme/widgets",
		"  https://GitHub.com/Acme/Widgets  ":                "Acme/Widgets",
		"git@github.com:acme/widgets.git":                    "acme/widgets",
		"git@github.com:acme/widgets":                        "acme/widgets",
		"https://github.com/acme/my.repo_v2":                 "acme/my.repo_v2",
	}
	for raw, want := range cases {
		owner, name, ok := ParseGitHubRepo(raw)
		if !ok || owner+"/"+name != want {
			t.Errorf("ParseGitHubRepo(%q) = %q/%q %v, want %s", raw, owner, name, ok, want)
		}
	}
}

func TestParseGitHubRepo_NotARepository(t *testing.T) {
	cases := []string{
		"",
		"https://github.com",
		"https://github.com/",
		"https://github.com/acme",
		"https://github.com/acme/",
		"https://gist.github.com/acme/abc123",
		"https://github.com/gist/abc123",
		"https://github.com/orgs/acme/repositories",
		"https://github.acme-enterprise.com/acme/widgets",
		"https://gitlab.com/acme/widgets",
		"https://example.com/github.com/acme/widgets",
		"ftp://github.com/acme/widgets",
		"https://github.com/-acme/widgets",
		"https://github.com/acme/wid gets",
		"git@gitlab.com:acme/widgets.git",
		strings.Repeat("a", MaxURLLength+1),
	}
	for _, raw := range cases {
		if owner, name, ok := ParseGitHubRepo(raw); ok {
			t.Errorf("ParseGitHubRepo(%q) = %q/%q, want not a repository", raw, owner, name)
		}
	}
}

// A fixture GitHub API. The README sentinel and the extra fields stand in for
// everything the resolver must not copy.
func fixtureGitHub(t *testing.T, status int, headers map[string]string, body map[string]any) (*httptest.Server, *http.Request) {
	t.Helper()
	var seen *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		copied := r.Clone(context.Background())
		seen = copied
		for key, value := range headers {
			w.Header().Set(key, value)
		}
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
	t.Cleanup(server.Close)
	return server, seen
}

const readmeSentinel = "README_SENTINEL_NEVER_IN_OBSERVATION"

func repoBody() map[string]any {
	return map[string]any{
		"full_name":         "acme/widgets",
		"description":       "Widgets for  everyone\n\tand\x07 more",
		"language":          "Go",
		"topics":            []string{"cli", "widgets", "", "go"},
		"default_branch":    "main",
		"private":           false,
		"archived":          false,
		"open_issues_count": 12,
		"pushed_at":         "2026-10-15T08:30:00Z",
		"html_url":          "https://github.com/acme/widgets",
		"homepage":          "https://widgets.example",
		"readme":            readmeSentinel,
		"contents":          []string{readmeSentinel},
	}
}

func TestGitHubResolver_ReadsTheRepositoryFacts(t *testing.T) {
	server, _ := fixtureGitHub(t, http.StatusOK, nil, repoBody())
	var tokenAsked bool
	resolver := NewGitHubResolver(server.Client(), func(context.Context) (string, bool) { tokenAsked = true; return "", false }).WithBase(server.URL)
	repo, err := resolver.Resolve(context.Background(), "acme", "widgets")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !tokenAsked {
		t.Fatal("the token source was not consulted")
	}
	if repo.Reachable != ReachableYes || repo.FullName != "acme/widgets" || repo.Language != "Go" || repo.DefaultBranch != "main" || repo.OpenIssues != 12 || repo.PushedAt != "2026-10-15" {
		t.Fatalf("repo facts: %+v", repo)
	}
	if repo.Description != "Widgets for everyone and more" {
		t.Fatalf("description was not bounded text: %q", repo.Description)
	}
	if strings.Join(repo.Topics, ",") != "cli,widgets,go" {
		t.Fatalf("topics: %v", repo.Topics)
	}
	data, _ := json.Marshal(repo)
	if strings.Contains(string(data), readmeSentinel) || strings.Contains(string(data), "homepage") {
		t.Fatalf("the observation copied more than the repository facts: %s", data)
	}
}

func TestGitHubResolver_SendsTheStoredTokenAsBearer(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(repoBody())
	}))
	t.Cleanup(server.Close)
	resolver := NewGitHubResolver(server.Client(), func(context.Context) (string, bool) { return " ghp_secret ", true }).WithBase(server.URL)
	if _, err := resolver.Resolve(context.Background(), "acme", "widgets"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if authorization != "Bearer ghp_secret" {
		t.Fatalf("authorization header: %q", authorization)
	}
}

func TestGitHubResolver_NotFoundAndForbiddenAreUnreachable(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		server, _ := fixtureGitHub(t, status, map[string]string{"X-RateLimit-Remaining": "41"}, map[string]any{"message": "Not Found"})
		resolver := NewGitHubResolver(server.Client(), nil).WithBase(server.URL)
		repo, err := resolver.Resolve(context.Background(), "acme", "widgets")
		if err != nil || repo.Reachable != ReachableNo || repo.FullName != "acme/widgets" {
			t.Fatalf("status %d: %+v %v", status, repo, err)
		}
	}
}

func TestGitHubResolver_RateLimitBadTokenAndOutagesAreUnknown(t *testing.T) {
	cases := []struct {
		status  int
		headers map[string]string
	}{
		{http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0"}},
		{http.StatusTooManyRequests, nil},
		{http.StatusForbidden, map[string]string{"Retry-After": "60"}},
		{http.StatusUnauthorized, nil},
		{http.StatusBadGateway, nil},
	}
	for _, tc := range cases {
		server, _ := fixtureGitHub(t, tc.status, tc.headers, map[string]any{"message": "nope"})
		resolver := NewGitHubResolver(server.Client(), nil).WithBase(server.URL)
		repo, err := resolver.Resolve(context.Background(), "acme", "widgets")
		if err != nil || repo.Reachable != ReachableUnknown || repo.FullName != "acme/widgets" {
			t.Fatalf("status %d %v: %+v %v", tc.status, tc.headers, repo, err)
		}
	}
	// A server that is not there at all.
	server, _ := fixtureGitHub(t, http.StatusOK, nil, nil)
	server.Close()
	resolver := NewGitHubResolver(server.Client(), nil).WithBase(server.URL)
	repo, err := resolver.Resolve(context.Background(), "acme", "widgets")
	if err != nil || repo.Reachable != ReachableUnknown {
		t.Fatalf("transport failure: %+v %v", repo, err)
	}
}

func TestGitHubResolver_OversizedOrMalformedBodiesAreUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("x", githubMaxBody+10)))
	}))
	t.Cleanup(server.Close)
	resolver := NewGitHubResolver(server.Client(), nil).WithBase(server.URL)
	repo, err := resolver.Resolve(context.Background(), "acme", "widgets")
	if err != nil || repo.Reachable != ReachableUnknown {
		t.Fatalf("oversized body: %+v %v", repo, err)
	}
}

func TestGitHubResolver_RefusesAMalformedName(t *testing.T) {
	server, _ := fixtureGitHub(t, http.StatusOK, nil, repoBody())
	resolver := NewGitHubResolver(server.Client(), nil).WithBase(server.URL)
	if _, err := resolver.Resolve(context.Background(), "acme/evil", "../widgets"); err == nil {
		t.Fatal("a malformed name must not become a request path")
	}
}
