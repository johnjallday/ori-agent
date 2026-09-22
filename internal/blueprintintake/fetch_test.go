package blueprintintake

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestHTTPLinkFetcherRefusesPrivateTestServerBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("private"))
	}))
	defer server.Close()

	_, err := NewHTTPLinkFetcher(server.Client()).Fetch(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "private or local") {
		t.Fatalf("error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("private server received %d request(s)", calls.Load())
	}
}

func TestHTTPLinkFetcherRefusesRedirectToPrivateHost(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://127.0.0.1/private"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})}

	_, err := NewHTTPLinkFetcher(client).Fetch(context.Background(), "https://example.com/start")
	if err == nil || !strings.Contains(err.Error(), "private or local") {
		t.Fatalf("redirect error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("redirect target was requested; calls = %d, want 1", calls.Load())
	}
}

func TestExtractPageTextReturnsTitleAndVisibleText(t *testing.T) {
	title, text := extractPageText([]byte(`<html><head><title>Course page</title><style>hidden</style></head><body><h1>Dates</h1><script>ignore</script><p>Quiz October 1</p></body></html>`), "text/html")
	if title != "Course page" || text != "Course page Dates Quiz October 1" {
		t.Fatalf("title/text = %q / %q", title, text)
	}
}
