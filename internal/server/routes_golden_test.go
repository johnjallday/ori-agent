package server

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// -update-golden regenerates testdata/route_table.golden from current behavior.
var updateGolden = flag.Bool("update-golden", false, "regenerate testdata/route_table.golden")

// probeMethods is the fixed method matrix replayed against every path. Probing a
// matrix (rather than parsing the method out of the frontend's 500+ fetch()
// sites) captures the routing outcome for every (method, path) — which is
// precisely what the ServeMux migration must preserve.
var probeMethods = []string{
	http.MethodGet,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
}

// probeExcludedPrefixes lists environment-coupled endpoints that do real OS /
// hardware / GUI / network work. Their responses vary by machine and can
// intermittently hang (e.g. mac-wake's permission call was observed to return
// 400 on one run and TIMEOUT on the next), so they cannot participate in a
// deterministic golden. None of them are hand-rolled dispatchers, so excluding
// them costs the router refactor no safety-net coverage. Migration-target
// siblings are deliberately kept (e.g. /api/location/zones stays covered while
// only /api/location/current — real geolocation — is excluded).
var probeExcludedPrefixes = []string{
	"/api/settings/mac-wake",    // macOS wake/power/permission APIs (observed to hang)
	"/api/device/",              // real hardware + wifi detection (slow, machine-specific)
	"/api/location/current",     // real geolocation prompt
	"/api/folder-picker",        // native folder-picker GUI
	"/api/launch-folder-picker", // native folder-picker GUI
	// Installed-tool / registry-network detection: the response shape or status
	// varies by whether external CLIs or the skills registry's network deps are
	// present, so it differs between a developer box and CI. (Confirmed by
	// diffing a tools-present vs tools-absent baseline; these were the only
	// routes that moved.)
	"/api/external-agents",              // detects installed Claude Code / Codex CLIs (keys vary)
	"/api/skills/marketplace/installed", // skills registry needs node/network (502 when absent)
	"/api/skills/marketplace/check",     // skills registry needs node/network (502 when absent)
	"/api/skills/marketplace/update",    // skills registry needs node/network (502 when absent)
}

func isExcludedPath(path string) bool {
	for _, p := range probeExcludedPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// TestGoldenRouteTable is the router-refactor safety net (PRD FR1–FR4).
//
// It replays a fixed (method, path) matrix against the real, production-wired
// handler (same mux + middleware chain as `Server.Handler()`), fingerprints each
// response, and asserts the whole table matches testdata/route_table.golden.
//
// Why this catches migration regressions: converting a hand-rolled string
// dispatcher (e.g. routeWorkspaceRuntimeRequest) into ServeMux patterns risks
// dropping a route (it starts 404ing) or shifting a method boundary (a handled
// method starts returning 405, or vice versa). Both change a route's routing
// class and fail this test, pointing at the exact route that moved.
//
// The fingerprint is a coarse ROUTING class — handled / not-found /
// method-not-allowed / redirect — NOT the handler's status or body. This is
// deliberate: a handler's 2xx/4xx/5xx output varies by environment (installed
// CLIs, OS speech/hardware), and that variance is not a routing change, so it
// must not fail the safety net. Wrong-handler mis-routes that keep the same
// class are covered instead by each migration group's own pattern-level tests.
// Requests run against an empty, sandboxed HOME (see newRoutesTestHandler).
//
// Regenerate after an intentional change (a documented bug fix, or a newly
// migrated group extending coverage):
//
//	go test ./internal/server -run TestGoldenRouteTable -update-golden
//
// then review the diff — every changed line is a route whose behavior moved.
func TestGoldenRouteTable(t *testing.T) {
	// Read testdata BEFORE building the handler: newRoutesTestHandler chdirs into
	// a temp HOME, after which these relative paths would no longer resolve.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	pathsFile := filepath.Join(wd, "testdata", "route_paths.txt")
	goldenFile := filepath.Join(wd, "testdata", "route_table.golden")

	paths := readRoutePaths(t, pathsFile)
	if len(paths) == 0 {
		t.Fatalf("no route paths loaded from %s", pathsFile)
	}

	handler := newRoutesTestHandler(t)

	lines := make([]string, 0, len(paths)*len(probeMethods))
	for _, p := range paths {
		for _, m := range probeMethods {
			lines = append(lines, formatRouteLine(m, p, probeRoute(handler, m, p)))
		}
	}
	sort.Strings(lines)
	got := strings.Join(lines, "\n") + "\n"

	if *updateGolden {
		if err := os.WriteFile(goldenFile, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %d route entries to %s", len(lines), goldenFile)
		return
	}

	want, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatalf("read golden (run with -update-golden to create it): %v", err)
	}
	if got != string(want) {
		t.Fatalf("route table drifted from testdata/route_table.golden.\n"+
			"If this change is intentional, regenerate and review the diff:\n"+
			"  go test ./internal/server -run TestGoldenRouteTable -update-golden\n\n"+
			"first difference:\n%s", firstDiff(string(want), got))
	}
}

// readRoutePaths loads the deduplicated, sorted list of paths to probe. Blank
// lines and `#` comments are ignored.
func readRoutePaths(t *testing.T, file string) []string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read route paths: %v", err)
	}
	seen := map[string]bool{}
	var paths []string
	for ln := range strings.SplitSeq(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") || isExcludedPath(ln) {
			continue
		}
		if !seen[ln] {
			seen[ln] = true
			paths = append(paths, ln)
		}
	}
	sort.Strings(paths)
	return paths
}

// probeRoute issues one request through the real handler and returns its routing
// class. It bounds slow/streaming handlers two ways: a 3s request context (SSE
// and other ctx-aware handlers return promptly) and a 10s hard goroutine
// backstop (recorded as a TIMEOUT class).
func probeRoute(handler http.Handler, method, path string) string {
	var body io.Reader
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		body = strings.NewReader("{}")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req := httptest.NewRequest(method, path, body).WithContext(ctx)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Several runtime routes require a loopback caller; give them one.
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		// The production ErrorRecovery middleware already turns handler panics
		// into 500s; this recover is a backstop so a panic can never abort the
		// whole matrix run.
		defer func() { _ = recover(); close(done) }()
		handler.ServeHTTP(rec, req)
	}()

	select {
	case <-done:
		return routingClass(rec.Code)
	case <-time.After(10 * time.Second):
		return "TIMEOUT"
	}
}

// routingClass maps an HTTP status to a coarse routing outcome. net/http.ServeMux
// itself produces 404 (no pattern matched), 405 (path matched but the method is
// not registered), and 3xx (trailing-slash redirects); every other status means
// the request reached a handler. Collapsing all handler statuses into "handled"
// makes the golden immune to environment-driven handler behavior — a route that
// returns 200 on one machine and 400/500/503 on another is the same routing
// outcome — while still failing when a route is dropped (handled -> not-found)
// or a method boundary shifts (handled <-> method-not-allowed).
func routingClass(status int) string {
	switch status {
	case http.StatusNotFound:
		return "not-found"
	case http.StatusMethodNotAllowed:
		return "method-not-allowed"
	case http.StatusMovedPermanently, http.StatusFound,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return "redirect"
	default:
		return "handled"
	}
}

func formatRouteLine(method, path, class string) string {
	return fmt.Sprintf("%-6s %-58s => %s", method, path, class)
}

// firstDiff returns the first line that differs between want and got, for a
// readable failure message.
func firstDiff(want, got string) string {
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	n := min(len(wl), len(gl))
	for i := range n {
		if wl[i] != gl[i] {
			return fmt.Sprintf("  want: %s\n  got:  %s", wl[i], gl[i])
		}
	}
	if len(wl) != len(gl) {
		return fmt.Sprintf("  table length changed: golden has %d lines, current run produced %d", len(wl), len(gl))
	}
	return "  (no line-level difference found)"
}
