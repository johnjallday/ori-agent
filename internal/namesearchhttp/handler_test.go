package namesearchhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/namesearch"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type tree struct{}

func (tree) GetWorkspaceTree(context.Context) ([]session.Workspace, error) {
	return []session.Workspace{{ID: "w1", Name: "Alpha Album"}}, nil
}

type notes struct{}

func (notes) ListNotesByWorkspace(context.Context, string) ([]session.WorkspaceNoteListItem, error) {
	return []session.WorkspaceNoteListItem{{ID: "n1", Name: "Alpha note"}}, nil
}

func handler() Handler {
	return Handler{Service: namesearch.New(namesearch.Sources{
		Tree:  tree{},
		Notes: notes{},
		Users: userprofile.LocalUserProvider{},
	})}
}

func post(h http.Handler, target, body, peer string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if peer != "" {
		req.RemoteAddr = peer
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAnswersASearchAndNeverCachesIt(t *testing.T) {
	rec := post(handler(), Path, `{"term":"alpha"}`, "127.0.0.1:1234", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	var out namesearch.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Kinds) != len(namesearch.Kinds) {
		t.Fatalf("kinds = %d", len(out.Kinds))
	}
	if w := out.Kinds[0]; w.Kind != "workspaces" || len(w.Items) != 1 || w.Items[0].Label != "Alpha Album" {
		t.Errorf("workspaces = %+v", w)
	}
	// Sources that are not wired answer as unavailable, never as "nothing found".
	for _, k := range out.Kinds {
		if k.Kind == "tickets" && (k.Status != namesearch.StatusUnavailable || k.Count != nil) {
			t.Errorf("tickets = %+v", k)
		}
	}
}

// Away from the Mac that runs Ori the search still answers: it reads only Ori's
// own records, and the filename index (linked folders) is a separate, local-only
// endpoint the page does not call from there. The page says so in one line.
func TestAnswersABrowserThatIsNotOnTheLocalMac(t *testing.T) {
	for _, peer := range []string{"192.0.2.5:5555", "[2001:db8::1]:5555"} {
		rec := post(handler(), Path, `{"term":"alpha","kinds":["workspaces","notes"]}`, peer, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: status %d", peer, rec.Code)
		}
		var out namesearch.Response
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Kinds) != 2 || len(out.Kinds[1].Items) != 1 {
			t.Errorf("%s: %+v", peer, out.Kinds)
		}
		for _, k := range out.Kinds {
			for _, item := range k.Items {
				if item.Kind != "workspace" && item.Kind != "note" {
					t.Errorf("%s: an unexpected kind of item: %+v", peer, item)
				}
			}
		}
	}
}

func TestRefusesWhatItShould(t *testing.T) {
	cases := []struct {
		name    string
		do      func() *httptest.ResponseRecorder
		status  int
		message string
	}{
		{"a GET", func() *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path, nil))
			return rec
		}, 405, ""},
		{"a query string", func() *httptest.ResponseRecorder {
			return post(handler(), Path+"?term=alpha", `{"term":"alpha"}`, "", nil)
		}, 400, ""},
		{"a cross-site page", func() *httptest.ResponseRecorder {
			return post(handler(), Path, `{"term":"alpha"}`, "", map[string]string{"Sec-Fetch-Site": "cross-site"})
		}, 403, ""},
		{"a same-site but not same-origin page", func() *httptest.ResponseRecorder {
			return post(handler(), Path, `{"term":"alpha"}`, "", map[string]string{"Sec-Fetch-Site": "same-site"})
		}, 403, ""},
		{"a form post", func() *httptest.ResponseRecorder {
			return post(handler(), Path, `term=alpha`, "", map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
		}, 415, ""},
		{"text/plain", func() *httptest.ResponseRecorder {
			return post(handler(), Path, `{"term":"alpha"}`, "", map[string]string{"Content-Type": "text/plain"})
		}, 415, ""},
		{"an unknown field", func() *httptest.ResponseRecorder {
			return post(handler(), Path, `{"term":"alpha","limit":500}`, "", nil)
		}, 400, ""},
		{"two JSON values", func() *httptest.ResponseRecorder {
			return post(handler(), Path, `{"term":"alpha"}{"term":"beta"}`, "", nil)
		}, 400, ""},
		{"trailing junk", func() *httptest.ResponseRecorder {
			return post(handler(), Path, `{"term":"alpha"} x`, "", nil)
		}, 400, ""},
		{"not JSON", func() *httptest.ResponseRecorder { return post(handler(), Path, `alpha`, "", nil) }, 400, ""},
		{"an empty term", func() *httptest.ResponseRecorder { return post(handler(), Path, `{"term":"   "}`, "", nil) }, 400, ""},
		{"a term over 128 runes", func() *httptest.ResponseRecorder {
			return post(handler(), Path, `{"term":"`+strings.Repeat("a", 129)+`"}`, "", nil)
		}, 400, ""},
		{"a body over the cap", func() *httptest.ResponseRecorder {
			return post(handler(), Path, `{"term":"alpha","pad":"`+strings.Repeat("x", 5000)+`"}`, "", nil)
		}, 400, ""},
		{"a bad cursor", func() *httptest.ResponseRecorder {
			return post(handler(), Path, `{"term":"alpha","cursors":{"notes":"-1"}}`, "", nil)
		}, 400, ""},
		{"no service wired", func() *httptest.ResponseRecorder { return post(Handler{}, Path, `{"term":"alpha"}`, "", nil) }, 503, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.do()
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.status, rec.Body.String())
			}
			// Every refusal is uncached too.
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestAnErrorNeverEchoesTheTermOrTheRequest(t *testing.T) {
	logs := &bytes.Buffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	const term = "zebra-secret-term"
	for _, body := range []string{
		`{"term":"` + term + `","bogus":1}`,                                // unknown field
		`{"term":"` + term + `"} trailing`,                                 // trailing junk
		`{"term":"` + term + `","kinds":["nope"]}`,                         // invalid request
		`{"term":"` + term + `","cursors":{"notes":"` + term + `"}}`,       // invalid cursor
		`{"term":"` + strings.Repeat(term, 40) + `"}`,                      // too long
		`{"term":"` + term + `","scope":{"workspace_id":"` + term + `x"}}`, // unknown scope
	} {
		rec := post(handler(), Path, body, "", nil)
		if strings.Contains(rec.Body.String(), term) {
			t.Errorf("the response echoed the term for %q: %s", body, rec.Body.String())
		}
		for name, values := range rec.Header() {
			for _, v := range values {
				if strings.Contains(v, term) {
					t.Errorf("header %s echoed the term", name)
				}
			}
		}
	}
	if strings.Contains(logs.String(), term) {
		t.Errorf("the term reached the log:\n%s", logs.String())
	}
}

func TestTheTermNeverAppearsInTheResponseAddressOrHeaders(t *testing.T) {
	rec := post(handler(), Path, `{"term":"alpha"}`, "", nil)
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("Location = %q", loc)
	}
	for name, values := range rec.Header() {
		for _, v := range values {
			if strings.Contains(strings.ToLower(v), "alpha") {
				t.Errorf("header %s carries the term: %q", name, v)
			}
		}
	}
}
