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
)

// The fixture servers listen on loopback, which the production transport
// refuses to dial. Tests give the reader the fixture's own client; the
// address rules that refuse loopback are tested through the observer.

const pageBodySentinel = "PAGE_BODY_SENTINEL_NEVER_READ"

func fixturePages(t *testing.T, handler http.HandlerFunc) (*HTTPPages, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewHTTPPages(server.Client()), server
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed
}

func TestHTTPPages_ReadsTheHeadAndNeverTheBody(t *testing.T) {
	pages, server := fixturePages(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head>
<title>  Attention &amp; Transformers
</title>
<meta name="description" content="A paper about   attention.">
<meta property="og:description" content="ignored when a description exists">
<script>var leak = "` + pageBodySentinel + `";</script>
</head><body><h1>` + pageBodySentinel + `</h1><p>` + pageBodySentinel + `</p>
<title>` + pageBodySentinel + `</title>
<meta name="description" content="` + pageBodySentinel + `"></body></html>`))
	})
	page, err := pages.Fetch(context.Background(), mustURL(t, server.URL+"/papers/attention"))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if page.Kind != KindPage || !page.Readable || page.Title != "Attention & Transformers" || page.Description != "A paper about attention." {
		t.Fatalf("page: %+v", page)
	}
	data, _ := json.Marshal(page)
	if strings.Contains(string(data), pageBodySentinel) {
		t.Fatalf("the body reached the page record: %s", data)
	}
}

func TestHTTPPages_FallsBackToOpenGraph(t *testing.T) {
	pages, server := fixturePages(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><meta property="og:title" content="OG Title"><meta name="twitter:description" content="From twitter"></head><body>x</body></html>`))
	})
	page, err := pages.Fetch(context.Background(), mustURL(t, server.URL))
	if err != nil || page.Title != "OG Title" || page.Description != "From twitter" || !page.Readable {
		t.Fatalf("open graph fallback: %+v %v", page, err)
	}
}

func TestHTTPPages_APageThatSaysNothingIsUnreadable(t *testing.T) {
	pages, server := fixturePages(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head></head><body><div id="app"></div><script src="/app.js"></script></body></html>`))
	})
	page, err := pages.Fetch(context.Background(), mustURL(t, server.URL+"/spa"))
	if err != nil || page.Readable || page.Kind != KindPage || page.Title != "" {
		t.Fatalf("an empty head: %+v %v", page, err)
	}
}

func TestHTTPPages_ADocumentIsKnownByItsAddressAndNeverRead(t *testing.T) {
	var bodyRead bool
	pages, server := fixturePages(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7 " + pageBodySentinel))
		bodyRead = true
	})
	page, err := pages.Fetch(context.Background(), mustURL(t, server.URL+"/papers/Attention%20Is%20All%20You%20Need.pdf"))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if page.Kind != KindDocument || page.Readable || page.Title != "Attention Is All You Need.pdf" {
		t.Fatalf("document: %+v", page)
	}
	if !bodyRead {
		t.Fatal("fixture did not answer")
	}
	data, _ := json.Marshal(page)
	if strings.Contains(string(data), pageBodySentinel) {
		t.Fatalf("the document's bytes reached the record: %s", data)
	}
}

func TestHTTPPages_MarkdownTitleAndPlainText(t *testing.T) {
	pages, server := fixturePages(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".md") {
			w.Header().Set("Content-Type", "text/markdown")
			_, _ = w.Write([]byte("\n\n# Project Notes\n\n" + pageBodySentinel))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(pageBodySentinel))
	})
	page, err := pages.Fetch(context.Background(), mustURL(t, server.URL+"/notes.md"))
	if err != nil || page.Title != "Project Notes" || !page.Readable || page.Kind != KindPage {
		t.Fatalf("markdown: %+v %v", page, err)
	}
	page, err = pages.Fetch(context.Background(), mustURL(t, server.URL+"/notes.txt"))
	if err != nil || page.Readable || page.Title != "" || strings.Contains(page.Description, pageBodySentinel) {
		t.Fatalf("plain text: %+v %v", page, err)
	}
}

func TestHTTPPages_ErrorsAndOtherTypesAttachByAddress(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"not found": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"server error": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
		"image": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{0x89, 'P', 'N', 'G'})
		},
		"no content type": func(w http.ResponseWriter, r *http.Request) {
			w.Header()["Content-Type"] = nil
			_, _ = w.Write([]byte{0, 1, 2})
		},
	}
	for name, handler := range cases {
		pages, server := fixturePages(t, handler)
		page, err := pages.Fetch(context.Background(), mustURL(t, server.URL+"/x"))
		if err != nil || page.Readable || page.Kind != KindPage || page.Title != "" {
			t.Fatalf("%s: %+v %v", name, page, err)
		}
	}
	// A closed server: unreachable, still attached.
	pages, server := fixturePages(t, func(http.ResponseWriter, *http.Request) {})
	server.Close()
	page, err := pages.Fetch(context.Background(), mustURL(t, server.URL+"/paper.pdf"))
	if err != nil || page.Readable || page.Kind != KindDocument {
		t.Fatalf("unreachable document address: %+v %v", page, err)
	}
}

func TestHTTPPages_RedirectsAreCheckedEveryHop(t *testing.T) {
	// The fixture is on loopback, which publicread.ValidateURL refuses, so
	// any redirect the reader is asked to follow is refused here: that is
	// the rule under test (every hop is checked, not just the first).
	pages, server := fixturePages(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/next", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Behind a redirect</title></head></html>`))
	})
	_, err := pages.Fetch(context.Background(), mustURL(t, server.URL+"/start"))
	if !errors.Is(err, ErrPageRedirect) {
		t.Fatalf("a hop that fails the address rules is refused, got %v", err)
	}
}

func TestHTTPPages_FollowsAPublicRedirectAndReportsTheFinalAddress(t *testing.T) {
	// A transport that answers for public-looking hosts from in-process
	// handlers, so the hop rules run on public addresses.
	transport := &rewriteTransport{routes: map[string]http.HandlerFunc{
		"start.example.org": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://www.example.org/landing", http.StatusMovedPermanently)
		},
		"www.example.org": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><title>Arrived</title></head></html>`))
		},
	}}
	pages := NewHTTPPages(&http.Client{Transport: transport})
	page, err := pages.Fetch(context.Background(), mustURL(t, "http://start.example.org/"))
	if err != nil || page.URL != "https://www.example.org/landing" || page.Title != "Arrived" || !page.Readable {
		t.Fatalf("cross-origin public redirect: %+v %v", page, err)
	}
	if transport.seenHeaders["www.example.org"].Get("Cookie") != "" || transport.seenHeaders["www.example.org"].Get("User-Agent") != pageUserAgent {
		t.Fatalf("headers on the hop: %v", transport.seenHeaders["www.example.org"])
	}

	// Four hops is one too many.
	loop := &rewriteTransport{routes: map[string]http.HandlerFunc{
		"loop.example.org": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://loop.example.org/again", http.StatusFound)
		},
	}}
	if _, err := NewHTTPPages(&http.Client{Transport: loop}).Fetch(context.Background(), mustURL(t, "https://loop.example.org/")); !errors.Is(err, ErrPageRedirect) {
		t.Fatalf("a redirect loop is refused, got %v", err)
	}
	// A hop to an address with a token in it is refused.
	token := &rewriteTransport{routes: map[string]http.HandlerFunc{
		"tok.example.org": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://tok.example.org/x?access_token=abc123", http.StatusFound)
		},
	}}
	if _, err := NewHTTPPages(&http.Client{Transport: token}).Fetch(context.Background(), mustURL(t, "https://tok.example.org/")); !errors.Is(err, ErrPageRedirect) {
		t.Fatalf("a hop carrying a token is refused, got %v", err)
	}
}

// rewriteTransport serves requests for named hosts from in-process handlers,
// so redirect rules run against public-looking addresses with no network.
type rewriteTransport struct {
	routes      map[string]http.HandlerFunc
	seenHeaders map[string]http.Header
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	handler, ok := t.routes[req.URL.Hostname()]
	if !ok {
		return nil, errors.New("no route for " + req.URL.Host)
	}
	if t.seenHeaders == nil {
		t.seenHeaders = map[string]http.Header{}
	}
	t.seenHeaders[req.URL.Hostname()] = req.Header.Clone()
	recorder := httptest.NewRecorder()
	handler(recorder, req)
	resp := recorder.Result()
	resp.Request = req
	return resp, nil
}

func TestObserve_ThroughTheRealReaderKeepsOnlyTitleAndDescription(t *testing.T) {
	transport := &rewriteTransport{routes: map[string]http.HandlerFunc{
		"news.example.org": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><title>Morning Briefing</title><meta name="description" content="Daily news"></head><body>` + pageBodySentinel + `</body></html>`))
		},
	}}
	observation, err := testObserver(nil, NewHTTPPages(&http.Client{Transport: transport})).Observe(context.Background(), "https://news.example.org/today")
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if observation.Title != "Morning Briefing" || observation.Description != "Daily news" || !observation.Readable || observation.Host != "news.example.org" {
		t.Fatalf("observation: %+v", observation)
	}
	data, _ := json.Marshal(observation)
	if strings.Contains(string(data), pageBodySentinel) {
		t.Fatalf("the body reached the observation: %s", data)
	}
}
