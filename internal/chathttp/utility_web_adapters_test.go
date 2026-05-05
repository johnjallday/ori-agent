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

func TestDuckDuckGoWebSearchAdapter_PollenComFallback(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`{"Heading":"","AbstractText":"","RelatedTopics":[]}`))
		case "/api/LocationSearch":
			if got := r.Header.Get("Referer"); got != server.URL+"/" {
				t.Fatalf("expected pollen location referer, got %q", got)
			}
			if got := r.URL.Query().Get("q"); got != "New York" {
				t.Fatalf("expected New York location query, got %q", got)
			}
			_, _ = w.Write([]byte(`{"Locations":[{"id":"10001","value":"NEW YORK, NY"}]}`))
		case "/api/forecast/current/pollen/10001":
			if got := r.Header.Get("Referer"); got != server.URL+"/forecast/current/pollen/10001" {
				t.Fatalf("expected pollen forecast referer, got %q", got)
			}
			_, _ = w.Write([]byte(`{
				"Type":"pollen",
				"ForecastDate":"2026-05-04T00:00:00-04:00",
				"Location":{
					"ZIP":"10001",
					"City":"NEW YORK",
					"State":"NY",
					"DisplayLocation":"New York, NY",
					"periods":[
						{"Type":"Today","Index":10.9,"Triggers":[{"Name":"Oak","Genus":"Quercus","PlantType":"Tree"}]}
					]
				}
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	oldBase := pollenComBaseURL
	pollenComBaseURL = server.URL
	defer func() { pollenComBaseURL = oldBase }()

	adapter := NewDuckDuckGoWebSearchAdapter(server.Client())
	adapter.Endpoint = server.URL

	resp, err := adapter.WebSearch(context.Background(), WebSearchRequest{Query: "NYC pollen count today"})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp.Source != "pollen.com" {
		t.Fatalf("expected pollen.com source, got %q", resp.Source)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 fallback result, got %#v", resp.Results)
	}
	if resp.Results[0].URL != server.URL+"/forecast/current/pollen/10001" {
		t.Fatalf("unexpected fallback URL: %q", resp.Results[0].URL)
	}
	if !strings.Contains(resp.Results[0].Snippet, "10.9") || !strings.Contains(resp.Results[0].Snippet, "Oak") {
		t.Fatalf("expected pollen forecast snippet, got %q", resp.Results[0].Snippet)
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

func TestHTTPWebFetchAdapter_EnrichesPollenComForecastPage(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/forecast/current/pollen/10001":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><head><title>Current Pollen Allergy Forecast for New York, NY (10001)</title></head><body><h1>Current Allergy Report</h1></body></html>`))
		case "/api/forecast/current/pollen/10001":
			if got := r.Header.Get("Referer"); got != server.URL+"/forecast/current/pollen/10001" {
				t.Fatalf("expected pollen forecast referer, got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"Type":"pollen",
				"ForecastDate":"2026-05-04T00:00:00-04:00",
				"Location":{
					"ZIP":"10001",
					"City":"NEW YORK",
					"State":"NY",
					"DisplayLocation":"New York, NY",
					"periods":[
						{"Type":"Yesterday","Index":11.0,"Triggers":[{"Name":"Oak","Genus":"Quercus","PlantType":"Tree"}]},
						{"Type":"Today","Index":10.9,"Triggers":[{"Name":"Oak","Genus":"Quercus","PlantType":"Tree"},{"Name":"Birch","Genus":"Betula","PlantType":"Tree"}]},
						{"Type":"Tomorrow","Index":11.2,"Triggers":[{"Name":"Maple","Genus":"Acer","PlantType":"Tree"}]}
					]
				}
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	oldBase := pollenComBaseURL
	pollenComBaseURL = server.URL
	defer func() { pollenComBaseURL = oldBase }()

	cfg := DefaultWebFetchAdapterConfig()
	cfg.Safety.BlockPrivateHosts = false
	adapter := NewHTTPWebFetchAdapter(server.Client(), cfg)

	resp, err := adapter.WebFetch(context.Background(), WebFetchRequest{URL: server.URL + "/forecast/current/pollen/10001"})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	for _, want := range []string{
		"Pollen.com structured forecast",
		"Forecast date: 2026-05-04T00:00:00-04:00",
		"Location: New York, NY (10001)",
		"Today: 10.9 (high)",
		"Oak (Quercus), Birch (Betula)",
		"API source: " + server.URL + "/api/forecast/current/pollen/10001",
	} {
		if !strings.Contains(resp.Content, want) {
			t.Fatalf("expected enriched content to contain %q, got %q", want, resp.Content)
		}
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
