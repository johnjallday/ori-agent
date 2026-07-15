package mailbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
	gmailapi "google.golang.org/api/gmail/v1"
)

type staticResolver struct{ err error }

func (s staticResolver) TokenSource(ctx context.Context, accountID string) (oauth2.TokenSource, error) {
	if s.err != nil {
		return nil, s.err
	}
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"}), nil
}

func newTestGmail(t *testing.T, handler http.HandlerFunc) *GmailProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	g := NewGmailProvider(staticResolver{})
	g.endpointOverride = srv.URL + "/"
	return g
}

func b64(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

const testAccountEmail = "me@example.com"

func testAccount() Account {
	return Account{ID: "acct-1", Provider: "gmail", EmailAddress: testAccountEmail}
}

func TestGmailSearchThreadsProjectsAndExcludes(t *testing.T) {
	var capturedQuery string
	g := newTestGmail(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(r.URL.Path, "/threads/") {
			// List call — capture the q param.
			capturedQuery = r.URL.Query().Get("q")
			_ = json.NewEncoder(w).Encode(gmailapi.ListThreadsResponse{
				Threads: []*gmailapi.Thread{{Id: "t1"}},
			})
			return
		}
		// Metadata Get for t1: one inbound unread message.
		_ = json.NewEncoder(w).Encode(gmailapi.Thread{
			Id: "t1",
			Messages: []*gmailapi.Message{{
				Id: "m1", ThreadId: "t1", LabelIds: []string{"UNREAD", "INBOX"}, InternalDate: 1_700_000_000_000,
				Snippet: "hi there",
				Payload: &gmailapi.MessagePart{Headers: []*gmailapi.MessagePartHeader{
					{Name: "Subject", Value: "Need your review"},
					{Name: "From", Value: "Dana <dana@partner.com>"},
					{Name: "To", Value: testAccountEmail},
				}},
			}},
		})
	})

	page, err := g.SearchThreads(context.Background(), testAccount(), Query{})
	if err != nil {
		t.Fatalf("SearchThreads: %v", err)
	}
	if len(page.Threads) != 1 {
		t.Fatalf("expected 1 thread, got %d", len(page.Threads))
	}
	th := page.Threads[0]
	if th.Subject != "Need your review" {
		t.Fatalf("subject = %q", th.Subject)
	}
	if !th.WaitingOnUser {
		t.Fatal("an unread inbound thread should be waiting on the user")
	}
	// The query must unconditionally exclude spam/trash/drafts and bound lookback.
	for _, want := range []string{"-in:spam", "-in:trash", "-in:drafts", "newer_than:"} {
		if !strings.Contains(capturedQuery, want) {
			t.Errorf("query %q missing %q", capturedQuery, want)
		}
	}
}

func TestGmailGetThreadSanitizesBody(t *testing.T) {
	g := newTestGmail(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gmailapi.Thread{
			Id: "t1",
			Messages: []*gmailapi.Message{{
				Id: "m1", ThreadId: "t1", InternalDate: 1_700_000_000_000,
				Payload: &gmailapi.MessagePart{
					MimeType: "text/html",
					Headers:  []*gmailapi.MessagePartHeader{{Name: "From", Value: "dana@partner.com"}},
					Body:     &gmailapi.MessagePartBody{Data: b64(`<p>Hello <script>alert(1)</script>world</p>`)},
				},
			}},
		})
	})

	th, err := g.GetThread(context.Background(), testAccount(), "t1")
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	snip := th.Messages[0].Snippet
	if strings.Contains(snip, "<") || strings.Contains(snip, "alert") {
		t.Fatalf("body not sanitized: %q", snip)
	}
	if !strings.Contains(snip, "Hello") || !strings.Contains(snip, "world") {
		t.Fatalf("expected visible text, got %q", snip)
	}
}

func TestGmailErrorClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header map[string]string
		want   error
	}{
		{"expired", http.StatusUnauthorized, nil, ErrExpired},
		{"not found", http.StatusNotFound, nil, ErrNotFound},
		{"rate limited", http.StatusTooManyRequests, map[string]string{"Retry-After": "42"}, ErrRateLimited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newTestGmail(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.header {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": tc.status, "message": "x"}})
			})
			_, err := g.GetThread(context.Background(), testAccount(), "t1")
			if !errors.Is(err, tc.want) {
				t.Fatalf("status %d: expected %v, got %v", tc.status, tc.want, err)
			}
			if tc.status == http.StatusTooManyRequests {
				var rl *RateLimitError
				if errors.As(err, &rl) && rl.RetryAfter.Seconds() != 42 {
					t.Fatalf("expected 42s retry-after, got %v", rl.RetryAfter)
				}
			}
		})
	}
}

func TestGmailDisconnectedResolver(t *testing.T) {
	g := NewGmailProvider(staticResolver{err: ErrDisconnected})
	if _, err := g.SearchThreads(context.Background(), testAccount(), Query{}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("expected ErrDisconnected, got %v", err)
	}
}

func TestGmailEmptyInboxIsHealthy(t *testing.T) {
	g := newTestGmail(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gmailapi.ListThreadsResponse{})
	})
	page, err := g.SearchThreads(context.Background(), testAccount(), Query{})
	if err != nil {
		t.Fatalf("empty inbox must not error: %v", err)
	}
	if len(page.Threads) != 0 {
		t.Fatalf("expected empty page, got %d", len(page.Threads))
	}
}
