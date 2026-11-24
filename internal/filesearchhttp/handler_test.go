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
