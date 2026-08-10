package workspace

import (
	"testing"
)

// A binding that names a repository must surface it on the resolved runtime,
// or the guard that enforces it never gets installed and the boundary is
// silently absent. This is the wiring test the unit tests around the guard
// itself cannot make.
func TestRuntime_SurfacesBindingRepoScope(t *testing.T) {
	ws := newToolboxRuntimeWorkspace()
	// The scope mechanism is server-agnostic at this layer -- the workspace
	// only carries the constraint; internal/githubscope is what interprets
	// it -- so this exercises it through a binding the test fixture already
	// knows how to materialize.
	for i := range ws.MCPBindings {
		if ws.MCPBindings[i].ID == "mb-2" {
			ws.MCPBindings[i].Scope = map[string]any{"repo": "octocat/demo"}
		}
	}

	resolved, err := newToolboxRuntimeResolver(t, ws).ResolveAgentForWorkspace("Coder", ws.ID, "coder-1")
	if err != nil {
		t.Fatalf("ResolveAgentForWorkspace() error = %v", err)
	}

	runtimeName := RuntimeMCPServerName(ws.ID, "docs", "mb-2")
	got, ok := resolved.MCPRepoScope[runtimeName]
	if !ok {
		t.Fatalf("expected a repo scope for %q, got %v", runtimeName, resolved.MCPRepoScope)
	}
	if got != "octocat/demo" {
		t.Fatalf("repo scope = %q, want octocat/demo", got)
	}

	// A binding with no scope must stay unconstrained -- this must not
	// become a blanket restriction on every MCP server.
	notesRuntime := RuntimeMCPServerName(ws.ID, "notes", "mb-1")
	if _, present := resolved.MCPRepoScope[notesRuntime]; present {
		t.Fatalf("an unscoped binding must not gain a repo constraint, got %v", resolved.MCPRepoScope)
	}
}

func TestBindingRepoScope(t *testing.T) {
	cases := []struct {
		name    string
		binding MCPBinding
		want    string
	}{
		{"no scope", MCPBinding{}, ""},
		{"empty scope", MCPBinding{Scope: map[string]any{}}, ""},
		{"other keys only", MCPBinding{Scope: map[string]any{"other": "x"}}, ""},
		{"not a string", MCPBinding{Scope: map[string]any{"repo": 42}}, ""},
		{"blank", MCPBinding{Scope: map[string]any{"repo": "   "}}, ""},
		{"present", MCPBinding{Scope: map[string]any{"repo": " octocat/demo "}}, "octocat/demo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BindingRepoScope(tc.binding); got != tc.want {
				t.Fatalf("BindingRepoScope = %q, want %q", got, tc.want)
			}
		})
	}
}
