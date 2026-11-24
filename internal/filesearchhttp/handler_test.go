package filesearchhttp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/filesearch"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// A full Files place, as the page sends it, is bigger than the usual body limit
// because the scope travels as an escaped JSON string. It must still be kept,
// and read back whole; one past what the service allows is still refused.
func TestFilesPlaceFullSizeRoundTrip(t *testing.T) {
	db, e := database.Open(t.Context(), &database.Config{InMemory: true})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = db.Close() }()
	s, e := filesearch.New(t.Context(), filesearch.Options{DB: db.DB, Policy: func() filesearch.Policy { return filesearch.Policy{} }, Pick: func(context.Context) (string, bool, error) { return "", false, nil }})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	h := Handler{Service: s, Users: userprofile.LocalUserProvider{}}
	const tab = "1822561b-104d-4d57-b079-33dded692620"
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", Prefix+"files-place", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:12345"
		r.Host = "localhost:8765"
		r.Header.Set("Origin", "http://localhost:8765")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	quotes := strings.Repeat(`\"`, 640) // 640 quote marks (the most a scope may be), each escaped inside the body
	full := `{"tab":"` + tab + `","place":{"term":"` + strings.Repeat("€", 128) + `","scope":"` + quotes +
		`","selected":"` + strings.Repeat(`\"`, 128) + `","pages":"` + strings.Repeat("a:1,", 39) + `a:1"}}`
	if len(full) <= 2048 {
		t.Fatalf("the case is meant to exceed the usual limit; it is %d bytes", len(full))
	}
	if w := post(full); w.Code != 200 {
		t.Fatalf("a full place was refused: %d %s", w.Code, w.Body.String())
	}
	w := post(`{"tab":"` + tab + `"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), strings.Repeat("€", 128)) {
		t.Fatalf("a full place was not read back: %d %.80s", w.Code, w.Body.String())
	}
	// Past the limit it is refused, not truncated.
	huge := `{"tab":"` + tab + `","place":{"term":"` + strings.Repeat("x", 9000) + `"}}`
	if w := post(huge); w.Code != 400 {
		t.Fatalf("an oversize place got %d", w.Code)
	}
}

// A row names its workspace and link; the host finds the folder. A path in
// the request is refused before the service is asked anything.
func TestLinkReviewTakesIDsNotPaths(t *testing.T) {
	db, e := database.Open(t.Context(), &database.Config{InMemory: true})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = db.Close() }()
	s, e := filesearch.New(t.Context(), filesearch.Options{DB: db.DB, Policy: func() filesearch.Policy { return filesearch.Policy{} }, Pick: func(context.Context) (string, bool, error) { t.Fatal("unexpected picker"); return "", false, nil }, ValidLink: func(string, string, filesearch.WorkspaceMatch) bool { return true }})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	h := Handler{Service: s, Users: userprofile.LocalUserProvider{}, WorkspaceReady: func() bool { return true }}
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"a path is not accepted", `{"workspace":"w","link":"l","path":"/tmp"}`, 400},
		{"an unknown link is not there", `{"workspace":"w","link":"l"}`, 409},
		{"ids are required", `{}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", Prefix+"review-link", strings.NewReader(tc.body))
			r.RemoteAddr = "127.0.0.1:12345"
			r.Host = "localhost:8765"
			r.Header.Set("Origin", "http://localhost:8765")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestOnlyLocalBrowserCanStartSearchChoice(t *testing.T) {
	db, e := database.Open(t.Context(), &database.Config{InMemory: true})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = db.Close() }()
	s, e := filesearch.New(t.Context(), filesearch.Options{DB: db.DB, Policy: func() filesearch.Policy { return filesearch.Policy{} }, Pick: func(context.Context) (string, bool, error) { t.Fatal("unexpected picker"); return "", false, nil }})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	h := Handler{Service: s, Users: userprofile.LocalUserProvider{}}
	const peer = "127.0.0.1:12345"
	const host = "localhost:8765"
	const origin = "http://localhost:8765"
	const body = `{"kind":"custom"}`
	for _, tc := range []struct {
		name, method, op, peer, host, origin, site, content, header, body string
		status                                                            int
	}{
		{"local intent", "POST", "begin", peer, host, origin, "same-origin", "application/json", "", body, 200},
		{"remote peer", "POST", "begin", "192.0.2.5:12345", host, origin, "", "application/json", "", body, 403},
		{"wrong host", "POST", "begin", peer, "ori.invalid:8765", "http://ori.invalid:8765", "", "application/json", "", body, 403},
		{"wrong port", "POST", "begin", peer, host, "http://localhost:9999", "", "application/json", "", body, 403},
		{"no origin", "POST", "begin", peer, host, "", "", "application/json", "", body, 403},
		{"form", "POST", "begin", peer, host, origin, "", "text/plain", "", body, 403},
		{"no authority in reads", "GET", "begin", peer, host, origin, "", "", "1", "", 405},
		{"private snapshot", "GET", "sources", peer, host, "", "same-origin", "", "1", "", 200},
		{"simple navigation", "GET", "sources", peer, host, "", "", "", "", "", 403},
		{"cross site read", "GET", "sources", peer, host, "", "cross-site", "", "1", "", 403},
		{"url terms refused", "GET", "sources?term=anything", peer, host, "", "", "", "1", "", 403},
		{"arbitrary path", "POST", "select", peer, host, origin, "", "application/json", "", `{"path":"/tmp"}`, 400},
		{"owner override", "POST", "begin", peer, host, origin, "", "application/json", "", `{"kind":"custom","owner":"other"}`, 400},
		{"review needs established profile", "POST", "review", peer, host, origin, "", "application/json", "", `{"intent":"foreign"}`, 409},
		{"connect needs established profile", "POST", "connect", peer, host, origin, "", "application/json", "", `{"intent":"old"}`, 409},
		{"source review rejects remote peer", "POST", "review-source", "192.0.2.5:12345", host, origin, "", "application/json", "", `{"id":"old"}`, 403},
		// Make searchable from a linked folder's row: local only, after setup,
		// and the two ids are all it takes; a path is not accepted.
		{"link review needs established profile", "POST", "review-link", peer, host, origin, "", "application/json", "", `{"workspace":"w","link":"l"}`, 409},
		{"link review rejects remote peer", "POST", "review-link", "192.0.2.5:12345", host, origin, "", "application/json", "", `{"workspace":"w","link":"l"}`, 403},
		{"link review is not a read", "GET", "review-link", peer, host, origin, "", "", "1", "", 405},
		{"review rejects remote peer", "POST", "review", "192.0.2.5:12345", host, origin, "", "application/json", "", `{"intent":"foreign"}`, 403},
		{"foreign token", "POST", "select", peer, host, origin, "", "application/json", "", `{"intent":"foreign"}`, 409},
		{"null source", "POST", "query", peer, host, origin, "", "application/json", "", `{"source":null}`, 400},
		{"empty source", "POST", "query", peer, host, origin, "", "application/json", "", `{"source":""}`, 400},
		{"compact unrestricted", "POST", "query", peer, host, origin, "", "application/json", "", `{}`, 200},
		{"oversize body", "POST", "query", peer, host, origin, "", "application/json", "", `{"term":"` + strings.Repeat("x", 2400) + `"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, Prefix+tc.op, strings.NewReader(tc.body))
			r.RemoteAddr = tc.peer
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", tc.content)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			r.Header.Set("X-Ori-Search", tc.header)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}
