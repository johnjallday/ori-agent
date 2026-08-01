package downloadsjanitorhttp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// No API response may contain the absolute managed path.
//
// The browser has no use for one: it sends candidate IDs and category IDs, and
// it renders destinations as labels relative to the folder ("Filed/Documents").
// An absolute path in a response ends up in devtools, in a copied bug report,
// and in whatever a page happens to log — and it is the user's home directory
// layout, which nothing about filing a download requires anyone to see
// (FR-110, FR-143).
//
// Settings is the one deliberate exception: the folder the user chose is shown
// back to them there, because a person cannot verify a grant they cannot read.
func TestResponses_NeverCarryTheAbsoluteRoot(t *testing.T) {
	h, root := configuredHandler(t)
	agedFile(t, root, "report.pdf", 128)
	agedFile(t, root, "payload.bin", 64)

	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/scan", ""); rec.Code != http.StatusOK {
		t.Fatalf("scan failed: %s", rec.Body.String())
	}

	// Every read a review surface performs.
	reads := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/workspaces/ws-1/file-janitor/batches/latest", ""},
		{http.MethodGet, "/api/workspaces/ws-1/file-janitor/batches", ""},
		{http.MethodGet, "/api/workspaces/ws-1/file-janitor/categories", ""},
		{http.MethodGet, "/api/workspaces/ws-1/file-janitor/history", ""},
		{http.MethodGet, "/api/workspaces/ws-1/file-janitor/skipped", ""},
		{http.MethodGet, "/api/workspaces/ws-1/file-janitor/readiness", ""},
		{http.MethodPost, "/api/workspaces/ws-1/file-janitor/test-scan", ""},
	}

	for _, read := range reads {
		t.Run(read.path, func(t *testing.T) {
			rec, _ := serve(t, h, read.method, read.path, read.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), root) {
				t.Errorf("response leaks the absolute managed path %q", root)
			}
		})
	}
}

// An error is the easiest place for a path to escape, because error strings are
// assembled from whatever was at hand. These are the failures a user is most
// likely to hit, and each one is checked for the absolute root.
func TestErrors_NeverCarryTheAbsoluteRoot(t *testing.T) {
	h, root := configuredHandler(t)
	agedFile(t, root, "payload.bin", 64)
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/scan", ""); rec.Code != http.StatusOK {
		t.Fatalf("scan failed: %s", rec.Body.String())
	}

	_, latest := serve(t, h, http.MethodGet,
		"/api/workspaces/ws-1/file-janitor/batches/latest?filter=needs_review", "")
	candidates, _ := latest["candidates"].([]any)
	if len(candidates) == 0 {
		t.Skip("this fixture produced no low-confidence candidate")
	}
	flagged, _ := candidates[0].(map[string]any)
	id, _ := flagged["id"].(string)

	failures := []struct {
		name string
		path string
		body string
	}{
		{
			name: "unresolved needs-review file",
			path: "/api/workspaces/ws-1/file-janitor/preview",
			body: `{"decisions":[{"candidate_id":"` + id + `","operation":"move"}]}`,
		},
		{
			name: "unknown candidate",
			path: "/api/workspaces/ws-1/file-janitor/preview",
			body: `{"decisions":[{"candidate_id":"cand-does-not-exist","operation":"move","category":"documents"}]}`,
		},
		{
			name: "unknown category",
			path: "/api/workspaces/ws-1/file-janitor/preview",
			body: `{"decisions":[{"candidate_id":"` + id + `","operation":"move","category":"not-a-category"}]}`,
		},
		{
			name: "spent approval",
			path: "/api/workspaces/ws-1/file-janitor/apply",
			body: `{"approval_token":"tok-nonexistent","decisions":[{"candidate_id":"` + id + `","operation":"move","category":"documents"}]}`,
		},
		{
			name: "unknown undo target",
			path: "/api/workspaces/ws-1/file-janitor/history/action-nonexistent/undo",
			body: `{}`,
		},
	}

	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			rec, _ := serve(t, h, http.MethodPost, failure.path, failure.body)
			if rec.Code < 400 {
				t.Fatalf("expected a failure, got %d: %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), root) {
				t.Errorf("error leaks the absolute managed path %q: %s", root, rec.Body.String())
			}
			// And no internal sentinel wording either — these are read by users.
			if strings.Contains(strings.ToLower(rec.Body.String()), "downloads janitor") {
				t.Errorf("error names the retired product: %s", rec.Body.String())
			}
		})
	}
}

// Settings is where the user verifies what they granted, so the folder they
// chose IS shown there. This states that exception explicitly rather than
// leaving it as an accident of which endpoints the test above happened to list.
func TestSettings_DeliberatelyShowsTheChosenFolder(t *testing.T) {
	h, root := configuredHandler(t)

	rec, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/file-janitor", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	status, _ := body["status"].(map[string]any)
	settings, _ := status["settings"].(map[string]any)
	if settings["root_path"] != root {
		t.Errorf("root_path = %v, want the folder the user approved (%q)", settings["root_path"], root)
	}
}

// The review payload carries relative destination labels only. An absolute
// destination supplied by a client would be a path the server then acted on,
// which is the whole class of escape this design forecloses (FR-141).
func TestCandidates_CarryRelativeDestinationsOnly(t *testing.T) {
	h, root := configuredHandler(t)
	agedFile(t, root, "report.pdf", 128)
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/scan", ""); rec.Code != http.StatusOK {
		t.Fatalf("scan failed: %s", rec.Body.String())
	}

	_, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/file-janitor/batches/latest", "")
	raw, err := json.Marshal(body["candidates"])
	if err != nil {
		t.Fatalf("marshal candidates: %v", err)
	}
	var candidates []map[string]any
	if err := json.Unmarshal(raw, &candidates); err != nil {
		t.Fatalf("decode candidates: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected candidates")
	}
	for _, candidate := range candidates {
		destination, _ := candidate["destination"].(string)
		if destination == "" {
			t.Errorf("candidate %v has no destination label", candidate["id"])
			continue
		}
		if strings.HasPrefix(destination, "/") || strings.Contains(destination, "..") {
			t.Errorf("destination %q is not a relative label", destination)
		}
	}
}
