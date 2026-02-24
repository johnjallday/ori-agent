package chathttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDuckDuckGoWebSearchAdapter_WebSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"Heading":"Go",
			"AbstractText":"Go is a programming language.",
			"AbstractURL":"https://go.dev/",
			"RelatedTopics":[
				{"Text":"Go documentation","FirstURL":"https://go.dev/doc/"},
				{"Text":"Go blog","FirstURL":"https://go.dev/blog/"}
			]
		}`))
	}))
	defer server.Close()

	adapter := NewDuckDuckGoWebSearchAdapter(server.Client())
	adapter.Endpoint = server.URL
	adapter.MaxResults = 3

	resp, err := adapter.WebSearch(context.Background(), WebSearchRequest{Query: "go language"})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp.Source != "duckduckgo.com" {
		t.Fatalf("expected source duckduckgo.com, got %q", resp.Source)
	}
	if len(resp.Results) == 0 {
		t.Fatalf("expected non-empty search results")
	}
	if resp.Results[0].URL != "https://go.dev/" {
		t.Fatalf("unexpected first result url: %q", resp.Results[0].URL)
	}
}

func TestHTTPWebFetchAdapter_WebFetchHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
			<!doctype html>
			<html>
			  <head>
			    <title>Example Page</title>
			    <style>.hidden { display:none; }</style>
			  </head>
			  <body>
			    <h1>Hello World</h1>
			    <p>This is a test page.</p>
			    <script>console.log("ignore me")</script>
			  </body>
			</html>
		`))
	}))
	defer server.Close()

	cfg := DefaultWebFetchAdapterConfig()
	cfg.Safety.BlockPrivateHosts = false
	adapter := NewHTTPWebFetchAdapter(server.Client(), cfg)

	resp, err := adapter.WebFetch(context.Background(), WebFetchRequest{URL: server.URL})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp.Title != "Example Page" {
		t.Fatalf("expected title Example Page, got %q", resp.Title)
	}
	if !strings.Contains(resp.Content, "Hello World") {
		t.Fatalf("expected content to include visible text, got %q", resp.Content)
	}
	if strings.Contains(resp.Content, "ignore me") {
		t.Fatalf("expected script text to be removed, got %q", resp.Content)
	}
	if resp.Source == "" {
		t.Fatalf("expected source hostname")
	}
}

func TestHTTPWebFetchAdapter_BlocksPrivateHosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	cfg := DefaultWebFetchAdapterConfig() // BlockPrivateHosts=true by default.
	adapter := NewHTTPWebFetchAdapter(server.Client(), cfg)

	_, err := adapter.WebFetch(context.Background(), WebFetchRequest{URL: server.URL})
	if err == nil {
		t.Fatalf("expected private-host safety error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "private") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSimpleBrowserAutomationAdapter_Actions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`
				<html><head><title>Home</title></head>
				<body>
					<a id="next" href="/next">Next</a>
					<input id="q" />
				</body></html>
			`))
		case "/next":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`
				<html><head><title>Next</title></head>
				<body>
					<div id="target">Final destination text</div>
				</body></html>
			`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	policy := DefaultBrowserAutomationPolicy()
	policy.BlockPrivateHosts = false
	adapter := NewSimpleBrowserAutomationAdapter(server.Client(), policy)

	openResp, err := adapter.BrowserAction(context.Background(), BrowserRequest{
		Action: "open_url",
		URL:    server.URL + "/",
	})
	if err != nil {
		t.Fatalf("open_url failed: %v", err)
	}
	if !openResp.Success {
		t.Fatalf("expected open_url success")
	}

	clickResp, err := adapter.BrowserAction(context.Background(), BrowserRequest{
		Action:   "click",
		Selector: "#next",
	})
	if err != nil {
		t.Fatalf("click failed: %v", err)
	}
	if !clickResp.Success {
		t.Fatalf("expected click success")
	}

	typeResp, err := adapter.BrowserAction(context.Background(), BrowserRequest{
		Action:   "type",
		Selector: "#q",
		Text:     "ori",
	})
	if err != nil {
		t.Fatalf("type failed: %v", err)
	}
	if !typeResp.Success {
		t.Fatalf("expected type success")
	}

	extractResp, err := adapter.BrowserAction(context.Background(), BrowserRequest{
		Action:   "extract_text",
		Selector: "#target",
	})
	if err != nil {
		t.Fatalf("extract_text failed: %v", err)
	}
	if !extractResp.Success {
		t.Fatalf("expected extract_text success")
	}
	if !strings.Contains(extractResp.Result, "Final destination text") {
		t.Fatalf("unexpected extracted text: %q", extractResp.Result)
	}
}

func TestSimpleBrowserAutomationAdapter_Allowlist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>ok</body></html>"))
	}))
	defer server.Close()

	policy := BrowserAutomationPolicy{
		UserAgent:         utilityDefaultUA,
		MaxResponseBytes:  1 << 20,
		AllowedDomains:    []string{"example.com"},
		BlockPrivateHosts: false,
	}
	adapter := NewSimpleBrowserAutomationAdapter(server.Client(), policy)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := adapter.BrowserAction(ctx, BrowserRequest{
		Action: "open_url",
		URL:    server.URL,
	})
	if err == nil {
		t.Fatalf("expected allowlist rejection")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
