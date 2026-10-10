package skills

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/publicread"
)

type publicReadFunc func(context.Context, publicread.Request) (publicread.Response, error)

func (f publicReadFunc) Read(ctx context.Context, request publicread.Request) (publicread.Response, error) {
	return f(ctx, request)
}

func TestPublicMarketplaceSearchUsesExactlyTheExistingMinimalProtocolWithoutNode(t *testing.T) {
	t.Setenv("PATH", "/no-runtime-on-path")
	t.Setenv("SKILLS_API_URL", "https://private-catalog.invalid")
	t.Setenv("OPENAI_API_KEY", "PRIVATE_CREDENTIAL_SENTINEL")
	query := "  Telegram community management  "
	calls := 0
	searcher := &PublicMarketplaceSearcher{reader: publicReadFunc(func(_ context.Context, request publicread.Request) (publicread.Response, error) {
		calls++
		parsed, err := url.Parse(request.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "skills.sh" || parsed.Path != "/api/search" || parsed.Query().Get("q") != query || parsed.Query().Get("limit") != "8" || len(parsed.Query()) != 2 || request.SameOriginRedirects {
			t.Fatalf("outbound protocol broadened: %+v", request)
		}
		return publicread.Response{URL: request.URL, ContentType: "application/json", Body: []byte(`{"skills":[{"id":"alice/community/telegram","source":"alice/community","name":"telegram","installs":12}]}`), ReadAt: time.Now().UTC(), Hash: strings.Repeat("a", 64), ETag: `"v1"`}, nil
	})}
	result := searcher.Search(context.Background(), query, 8)
	if calls != 1 || result.State != "available" || len(result.Results) != 1 || result.RuntimeVersion != "" || result.SourceHash != strings.Repeat("a", 64) || result.Results[0].URL != "https://skills.sh/alice/community/telegram" {
		t.Fatalf("result: %+v", result)
	}
}

func TestPublicMarketplaceSearchDistinguishesVerifiedEmptyMalformedAndServiceFailure(t *testing.T) {
	for _, test := range []struct{ body, state string }{
		{`{"skills":[]}`, "empty"},
		{`{"skills":[],"error":"PRIVATE_SERVER_SENTINEL"}`, "unavailable"},
		{`{"skills":null}`, "malformed_output"}, {`{}`, "malformed_output"}, {`[]`, "malformed_output"},
		{`{"skills":"not an array"}`, "malformed_output"}, {`{"skills":[]} trailing`, "malformed_output"},
		{`{"skills":[{"name":"injected\nalice/community@telegram","source":"bad"}]}`, "malformed_output"},
		{`{"skills":[{"source":"alice/community","name":"telegram --install"}]}`, "malformed_output"},
	} {
		searcher := &PublicMarketplaceSearcher{reader: publicReadFunc(func(_ context.Context, request publicread.Request) (publicread.Response, error) {
			return publicread.Response{URL: request.URL, Body: []byte(test.body), ReadAt: time.Now().UTC()}, nil
		})}
		if result := searcher.Search(context.Background(), "telegram", 8); result.State != test.state || len(result.Results) != 0 {
			t.Fatalf("%s: %+v", test.body, result)
		}
	}
	searcher := &PublicMarketplaceSearcher{reader: publicReadFunc(func(context.Context, publicread.Request) (publicread.Response, error) {
		return publicread.Response{}, errors.New("PRIVATE_DNS_SENTINEL")
	})}
	if got := searcher.Search(context.Background(), "telegram", 8); got.State != "unavailable" || !got.ObservedAt.IsZero() || strings.Contains(got.Reason, "PRIVATE_") {
		t.Fatalf("failed read: %+v", got)
	}
}

func TestPublicMarketplaceSearchBoundsCandidatesAndDoesNotGuessUnknownListingURLs(t *testing.T) {
	body := `{"skills":[`
	for i := 0; i < 12; i++ {
		if i > 0 {
			body += ","
		}
		body += fmt.Sprintf(`{"id":"unknown/slug","source":"alice/community","name":"skill%d"}`, i)
	}
	body += `]}`
	searcher := &PublicMarketplaceSearcher{reader: publicReadFunc(func(_ context.Context, request publicread.Request) (publicread.Response, error) {
		return publicread.Response{URL: request.URL, Body: []byte(body), ReadAt: time.Now().UTC()}, nil
	})}
	got := searcher.Search(context.Background(), "telegram", 999)
	if got.State != "partial" || !got.Truncated || len(got.Results) != 8 {
		t.Fatalf("bounded results: %+v", got)
	}
	for _, match := range got.Results {
		if match.URL != "" {
			t.Fatalf("invented listing destination: %+v", match)
		}
	}
	calls := 0
	searcher.reader = publicReadFunc(func(context.Context, publicread.Request) (publicread.Response, error) {
		calls++
		t.Fatal("invalid query reached source")
		return publicread.Response{}, nil
	})
	for _, query := range []string{"", "telegram --install", "api_key=PRIVATE_SENTINEL", strings.Repeat("界", 256)} {
		if got := searcher.Search(context.Background(), query, 8); got.State != "invalid_query" || calls != 0 {
			t.Fatalf("query %q: %+v", query, got)
		}
	}
}
