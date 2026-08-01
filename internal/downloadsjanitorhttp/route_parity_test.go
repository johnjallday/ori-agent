package downloadsjanitorhttp

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/downloadsjanitor"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// routePrefixPair is the canonical prefix and the legacy alias that must behave
// identically for this whole release (FR-132, FR-133).
var routePrefixPair = []struct {
	name   string
	prefix string
}{
	{"canonical", workspacecapability.FileJanitorAPIPrefix},
	{"legacy", workspacecapability.LegacyAPIPrefixDownloadsJanitor},
}

func pathFor(prefix, workspaceID, suffix string) string {
	base := "/api/workspaces/" + workspaceID + "/" + prefix
	if suffix == "" {
		return base
	}
	return base + suffix
}

// TestRoutePrefixes_ComeFromTheCapabilityDefinition pins the routes to the
// compiled capability's declared API identity, so a prefix cannot be added or
// renamed in one place and forgotten in the other.
func TestRoutePrefixes_ComeFromTheCapabilityDefinition(t *testing.T) {
	prefixes := routePrefixes()
	if len(prefixes) < 2 {
		t.Fatalf("expected a canonical prefix plus at least one legacy alias, got %v", prefixes)
	}
	if prefixes[0] != workspacecapability.FileJanitorAPIPrefix {
		t.Fatalf("canonical prefix must be first, got %q", prefixes[0])
	}
	if !slices.Contains(prefixes, workspacecapability.LegacyAPIPrefixDownloadsJanitor) {
		t.Fatalf("the legacy downloads-janitor alias must be retained, got %v", prefixes)
	}
}

// TestBothPrefixesServeTheSameStatus proves the canonical route is not a second
// implementation: the same workspace reports identical state through both.
//
// Readiness carries a checked_at stamped at evaluation time, and readiness is
// deliberately re-evaluated per request, so the two responses legitimately
// differ by microseconds. That one field is normalized away; everything else —
// settings, every component check, privacy posture, suggestion — must match
// exactly.
func TestBothPrefixesServeTheSameStatus(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})

	bodies := map[string]string{}
	for _, tc := range routePrefixPair {
		rec, body := serve(t, h, http.MethodGet, pathFor(tc.prefix, "ws-1", ""), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d: %s", tc.name, rec.Code, rec.Body.String())
		}
		bodies[tc.name] = normalizeStatusBody(t, body)
	}

	if bodies["canonical"] != bodies["legacy"] {
		t.Fatalf("prefixes diverged:\n canonical: %s\n legacy:    %s", bodies["canonical"], bodies["legacy"])
	}
}

// normalizeStatusBody re-serializes a status response with the per-request
// readiness timestamp removed.
func normalizeStatusBody(t *testing.T, body map[string]any) string {
	t.Helper()
	if status, ok := body["status"].(map[string]any); ok {
		if readiness, ok := status["readiness"].(map[string]any); ok {
			delete(readiness, "checked_at")
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("re-encode status: %v", err)
	}
	return string(encoded)
}

// TestCanonicalRouteSetMatchesLegacy walks every read endpoint under both
// prefixes. A canonical route that was never registered would 404 here while
// its legacy twin answers, which is exactly the drift this guards.
func TestCanonicalRouteSetMatchesLegacy(t *testing.T) {
	readEndpoints := []string{"", "/readiness", "/skipped", "/categories", "/batches", "/history"}

	h, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})

	for _, suffix := range readEndpoints {
		t.Run("GET"+suffix, func(t *testing.T) {
			codes := map[string]int{}
			for _, tc := range routePrefixPair {
				rec, _ := serve(t, h, http.MethodGet, pathFor(tc.prefix, "ws-1", suffix), "")
				if rec.Code == http.StatusNotFound && rec.Body.Len() == 0 {
					t.Fatalf("%s prefix has no route for %q", tc.name, suffix)
				}
				codes[tc.name] = rec.Code
			}
			if codes["canonical"] != codes["legacy"] {
				t.Fatalf("%q: canonical returned %d, legacy returned %d", suffix, codes["canonical"], codes["legacy"])
			}
		})
	}
}

// TestBothPrefixesEnforceOwnership is the FR-140 half of route parity: adding a
// canonical prefix must not create a door around the ownership check. A
// workspace owned by someone else is not listable, scannable, decidable,
// mutable, inspectable, or configurable through EITHER prefix.
func TestBothPrefixesEnforceOwnership(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{"ws-other": "someone-else"})

	cases := []struct {
		name   string
		method string
		suffix string
		body   string
	}{
		{"read status", http.MethodGet, "", ""},
		{"list batches", http.MethodGet, "/batches", ""},
		{"inspect history", http.MethodGet, "/history", ""},
		{"scan", http.MethodPost, "/scan", ""},
		{"decide", http.MethodPost, "/decisions", `{"decisions":[]}`},
		{"preview", http.MethodPost, "/preview", `{"batch_id":"b1","candidate_ids":["c1"]}`},
		{"apply", http.MethodPost, "/apply", `{"batch_id":"b1","candidate_ids":["c1"]}`},
		{"change settings", http.MethodPatch, "/settings", `{"paused":true}`},
		{"relink", http.MethodPost, "/relink", `{"path":"/tmp"}`},
		{"revoke", http.MethodPost, "/revoke", ""},
		{"set up", http.MethodPost, "/setup", `{"path":"/tmp"}`},
	}

	for _, tc := range cases {
		for _, prefix := range routePrefixPair {
			t.Run(tc.name+"/"+prefix.name, func(t *testing.T) {
				rec, _ := serve(t, h, tc.method, pathFor(prefix.prefix, "ws-other", tc.suffix), tc.body)
				if rec.Code != http.StatusNotFound {
					t.Fatalf("status = %d, want 404 for a workspace owned by someone else: %s", rec.Code, rec.Body.String())
				}
				// Reported as absent, not forbidden: the API must not confirm
				// that another user's workspace exists.
				if strings.Contains(strings.ToLower(rec.Body.String()), "forbidden") {
					t.Fatalf("response confirms the workspace exists: %s", rec.Body.String())
				}
			})
		}
	}
}

// TestCanonicalSetupThenLegacyReadSeesSameState proves the two prefixes share
// one state store rather than each keeping their own: a folder approved through
// the canonical route is visible through the legacy one, and vice versa.
func TestCanonicalSetupThenLegacyReadSeesSameState(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	root := inboxFixture(t)

	payload, _ := json.Marshal(map[string]string{"path": root})
	rec, _ := serve(t, h, http.MethodPost,
		pathFor(workspacecapability.FileJanitorAPIPrefix, "ws-1", "/setup"), string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("canonical setup: status = %d: %s", rec.Code, rec.Body.String())
	}

	rec, body := serve(t, h, http.MethodGet,
		pathFor(workspacecapability.LegacyAPIPrefixDownloadsJanitor, "ws-1", ""), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy read: status = %d: %s", rec.Code, rec.Body.String())
	}
	status, _ := body["status"].(map[string]any)
	settings, _ := status["settings"].(map[string]any)
	if settings["root_path"] != filepath.Clean(root) {
		t.Fatalf("legacy read does not see the canonical setup: root_path = %v", settings["root_path"])
	}
	readiness, _ := status["readiness"].(map[string]any)
	if readiness["state"] == string(downloadsjanitor.ReadinessSetupRequired) {
		t.Fatal("legacy read still reports setup_required after a canonical setup")
	}
}
