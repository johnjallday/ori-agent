package publicsearch

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/publicread"
)

type searchReadFunc func(context.Context, publicread.Request) (publicread.Response, error)

func (f searchReadFunc) Read(ctx context.Context, r publicread.Request) (publicread.Response, error) {
	return f(ctx, r)
}
func TestPublicSearchUsesExactExistingProtocolNoRedirectsOrFallback(t *testing.T) {
	for _, fixture := range []struct {
		body   string
		count  int
		failed bool
	}{{`{"RelatedTopics":[],"AbstractURL":""}`, 0, false}, {`{"Heading":"Community","AbstractURL":"https://example.com/docs","AbstractText":"metadata","RelatedTopics":[]}`, 1, false}, {`{}`, 0, true}, {`{"RelatedTopics":null}`, 0, true}, {`{"RelatedTopics":"malformed"}`, 0, true}, {`<html>service failure</html>`, 0, true}} {
		t.Run(fixture.body, func(t *testing.T) {
			calls := 0
			adapter := &PublicWebSearchAdapter{reader: searchReadFunc(func(_ context.Context, request publicread.Request) (publicread.Response, error) {
				calls++
				target, _ := url.Parse(request.URL)
				if target.Scheme != "https" || target.Host != "api.duckduckgo.com" || target.Query().Get("q") != "Telegram community management" || len(target.Query()) != 4 || request.SameOriginRedirects {
					t.Fatal("scope changed", request)
				}
				return publicread.Response{URL: request.URL, Body: []byte(fixture.body), ReadAt: time.Now().UTC(), Hash: strings.Repeat("a", 64)}, nil
			})}
			result, err := adapter.Search(context.Background(), "Telegram community management")
			if (err != nil) != fixture.failed || !fixture.failed && len(result.Response.Results) != fixture.count || calls != 1 {
				t.Fatal(result, err, calls)
			}
		})
	}
}
func TestPublicSearchRejectsCredentialInvalidQueriesAndFailedRead(t *testing.T) {
	calls := 0
	adapter := &PublicWebSearchAdapter{reader: searchReadFunc(func(context.Context, publicread.Request) (publicread.Response, error) {
		calls++
		return publicread.Response{}, errors.New("private diagnostics")
	})}
	for _, query := range []string{"", "--install", "api_key=secret", "Telegram\ncommunity"} {
		if _, err := adapter.Search(context.Background(), query); err == nil {
			t.Fatal("unsafe query accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid input sent")
	}
	if _, err := adapter.Search(context.Background(), "pollen forecast somewhere"); !errors.Is(err, ErrPublicSearch) || calls != 1 {
		t.Fatal("failed read used another source or leaked diagnostics", err, calls)
	}
}
