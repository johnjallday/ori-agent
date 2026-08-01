package downloadsjanitorhttp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// registeredHandlers extracts the handler method name for every route this
// package mounts, by reading routes.go rather than by maintaining a list.
//
// A hand-maintained list is the wrong shape for this check: the failure it
// guards against is someone adding a route and forgetting the ownership check,
// and that same person would forget the list.
func registeredHandlers(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "routes.go", nil, 0)
	if err != nil {
		t.Fatalf("parse routes.go: %v", err)
	}

	var handlers []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "HandleFunc" {
			return true
		}
		// The handler is the second argument: h.Something
		if len(call.Args) < 2 {
			return true
		}
		method, ok := call.Args[1].(*ast.SelectorExpr)
		if !ok {
			return true
		}
		handlers = append(handlers, method.Sel.Name)
		return true
	})
	if len(handlers) == 0 {
		t.Fatal("found no registered handlers; this audit would pass vacuously")
	}
	return handlers
}

// packageFiles parses every non-test source file in this package.
func packageFiles(t *testing.T) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		files = append(files, file)
	}
	return files
}

// callsWorkspaceGuard reports whether a handler body invokes resolveWorkspace.
func callsWorkspaceGuard(t *testing.T, handlerName string) bool {
	t.Helper()
	found := false
	guarded := false
	for _, file := range packageFiles(t) {
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Name.Name != handlerName || fn.Recv == nil {
				return true
			}
			found = true
			ast.Inspect(fn.Body, func(inner ast.Node) bool {
				call, ok := inner.(*ast.CallExpr)
				if !ok {
					return true
				}
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok &&
					selector.Sel.Name == "resolveWorkspace" {
					guarded = true
				}
				return true
			})
			return false
		})
	}
	if !found {
		t.Fatalf("handler %s is registered but not defined in this package", handlerName)
	}
	return guarded
}

// Every File Janitor route is workspace-scoped and owner-checked.
//
// resolveWorkspace is the single place that establishes the workspace exists
// and belongs to the requesting user, and reports someone else's workspace as
// not found rather than forbidden. A handler that skips it would read, scan, or
// MUTATE FILES in a workspace on behalf of whoever asked (FR-140, FR-146).
//
// This is asserted structurally because the risk is a future route, not the
// current ones: a per-route test suite proves today's set is safe and says
// nothing about tomorrow's.
func TestEveryRouteEnforcesWorkspaceOwnership(t *testing.T) {
	// An audit that cannot fail is worse than no audit, because it reads as
	// evidence. SetAutomation is a real method in this package that legitimately
	// does not guard anything, so it proves the detector distinguishes.
	if callsWorkspaceGuard(t, "SetAutomation") {
		t.Fatal("the guard detector reports true for a method that does not guard; this audit proves nothing")
	}

	for _, handler := range registeredHandlers(t) {
		t.Run(handler, func(t *testing.T) {
			if !callsWorkspaceGuard(t, handler) {
				t.Errorf("%s does not call resolveWorkspace; every route must be owner-scoped", handler)
			}
		})
	}
}

// The behavioral counterpart: another user's workspace is 404 on every route,
// under BOTH the canonical and the compatibility prefix. An alias that skipped
// the check would be a second, weaker door into the same data (FR-132, FR-140).
func TestEveryRouteRefusesAnotherUsersWorkspace(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{
		"ws-1":     userprofile.LocalUserID,
		"ws-other": "someone-else",
	})

	// One request per route shape. Methods match the registration so a 405
	// cannot be mistaken for a passing ownership check.
	routes := []struct {
		method string
		suffix string
	}{
		{http.MethodGet, ""},
		{http.MethodGet, "/readiness"},
		{http.MethodPost, "/setup"},
		{http.MethodPost, "/pause"},
		{http.MethodPatch, "/settings"},
		{http.MethodPost, "/content-consent"},
		{http.MethodPost, "/relink"},
		{http.MethodPost, "/revoke"},
		{http.MethodGet, "/skipped"},
		{http.MethodGet, "/categories"},
		{http.MethodGet, "/batches"},
		{http.MethodGet, "/batches/latest"},
		{http.MethodPost, "/test-scan"},
		{http.MethodPost, "/scan"},
		{http.MethodPost, "/decisions"},
		{http.MethodPost, "/skipped/reset"},
		{http.MethodPost, "/preview"},
		{http.MethodPost, "/apply"},
		{http.MethodGet, "/history"},
		{http.MethodPost, "/history/action-1/undo"},
	}

	for _, prefix := range []string{"file-janitor", "downloads-janitor"} {
		for _, route := range routes {
			name := prefix + " " + route.method + " " + route.suffix
			t.Run(name, func(t *testing.T) {
				target := "/api/workspaces/ws-other/" + prefix + route.suffix
				rec, _ := serve(t, h, route.method, target, "{}")
				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404 for another user's workspace (%s)",
						rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// And an unknown workspace is equally 404, so the two are indistinguishable
// from outside: a different answer would confirm that another user's workspace
// exists.
func TestUnknownWorkspaceIsIndistinguishableFromSomeoneElses(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{
		"ws-1":     userprofile.LocalUserID,
		"ws-other": "someone-else",
	})

	unknown, _ := serve(t, h, http.MethodGet, "/api/workspaces/ws-nonexistent/file-janitor", "")
	theirs, _ := serve(t, h, http.MethodGet, "/api/workspaces/ws-other/file-janitor", "")

	if unknown.Code != theirs.Code {
		t.Fatalf("unknown = %d, someone else's = %d; these must be identical",
			unknown.Code, theirs.Code)
	}
	if unknown.Body.String() != theirs.Body.String() {
		t.Errorf("response bodies differ:\n  unknown: %s\n  theirs:  %s",
			unknown.Body.String(), theirs.Body.String())
	}
}
