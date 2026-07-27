package workspace

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/mcp"
)

// The bug this file guards: an Email Ops workspace binds `gmail` (a NATIVE Ori
// capability), the runtime resolver treated it as an MCP binding, looked up a
// template named "gmail", correctly failed to find one, and blocked the whole
// task with "server gmail not found" — taking the workspace's real MCP bindings
// down with it.

// recordingTemplateLookup fails every lookup like a real registry with no gmail
// template, and records what was asked for so a test can assert the resolver
// never asked at all.
type recordingTemplateLookup struct {
	servers map[string]mcp.ServerConfig
	asked   []string
}

func (r *recordingTemplateLookup) GetServer(name string) (*mcp.ServerConfig, error) {
	r.asked = append(r.asked, name)
	cfg, ok := r.servers[name]
	if !ok {
		// Mirrors the real registry's message, which is what surfaced in the UI.
		return nil, errTemplateNotFound{name: name}
	}
	clone := cfg
	return &clone, nil
}

func (r *recordingTemplateLookup) askedFor(name string) bool {
	for _, n := range r.asked {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// emailOpsResolver builds the exact shape that used to fail: one real MCP
// binding plus one native email binding, and a registry that has no gmail
// template (because there is correctly no such thing).
func emailOpsResolver(t *testing.T, gmailBinding MCPBinding) (*AgentRuntimeResolver, *recordingTemplateLookup, *runtimeRegistryStub) {
	t.Helper()
	ws := &Workspace{
		ID: "ws-email-ops",
		AgentInstances: []AgentInstance{
			{ID: "inst-inbox", Name: "Inbox", NodeID: "inbox-node"},
		},
		MCPBindings: []MCPBinding{
			{ID: "b-fs", ServerName: "filesystem", Enabled: true, Config: map[string]any{"roots": []any{t.TempDir()}}},
			gmailBinding,
		},
	}
	agentStore := &resolverAgentStoreStub{agents: map[string]*agent.Agent{"Inbox": {}}}
	registry := &runtimeRegistryStub{}
	templates := &recordingTemplateLookup{servers: map[string]mcp.ServerConfig{
		"filesystem": {Name: "filesystem", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem"}},
	}}
	return NewAgentRuntimeResolver(agentStore, newTestWorkspaceStore(t, ws), registry, templates), templates, registry
}

func TestResolver_NativeEmailBindingIsNotAnMCPServer(t *testing.T) {
	// Both shapes must behave identically: the explicitly-marked binding written
	// by today's linker, and the legacy binding written before the field existed.
	shapes := map[string]MCPBinding{
		"explicit native_email": {
			ID: "b-mail", ServerName: "gmail", Enabled: true,
			RuntimeKind: RuntimeKindNativeEmail,
			Config:      map[string]any{"account_id": "acct-1"},
		},
		"legacy binding with no runtime_kind": {
			ID: "b-mail", ServerName: "gmail", Enabled: true,
			Config: map[string]any{"account_id": "acct-1"},
		},
	}

	for name, gmailBinding := range shapes {
		t.Run(name, func(t *testing.T) {
			resolver, templates, registry := emailOpsResolver(t, gmailBinding)

			resolved, err := resolver.ResolveAgentForWorkspace("Inbox", "ws-email-ops", "inbox-node")
			if err != nil {
				t.Fatalf("resolution failed because of a native email binding: %v", err)
			}

			// The regression assertion (FR 90).
			if templates.askedFor("gmail") {
				t.Fatalf("resolver called GetServer(\"gmail\"); asked for %v", templates.asked)
			}
			for _, name := range registry.namesContaining("gmail") {
				t.Fatalf("resolver materialized a gmail MCP runtime: %s", name)
			}

			// The real MCP binding is unaffected.
			if len(resolved.MCPServers) != 1 {
				t.Fatalf("MCP servers = %v, want just the filesystem runtime", resolved.MCPServers)
			}
			if !strings.Contains(resolved.MCPServers[0], ":mcp:filesystem:") {
				t.Fatalf("materialized %q, want the filesystem binding", resolved.MCPServers[0])
			}
		})
	}
}

// The native binding must still be present in workspace state, because that is
// what authorizes the mailbox tools (FR 26).
func TestResolver_NativeEmailBindingSurvivesInWorkspaceState(t *testing.T) {
	gmail := MCPBinding{
		ID: "b-mail", ServerName: "gmail", Enabled: true,
		RuntimeKind: RuntimeKindNativeEmail,
		Config:      map[string]any{"account_id": "acct-1"},
	}
	resolver, _, _ := emailOpsResolver(t, gmail)
	if _, err := resolver.ResolveAgentForWorkspace("Inbox", "ws-email-ops", "inbox-node"); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	ws, err := resolver.workspaceStore.Get("ws-email-ops")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	binding, ok := ws.GetMCPBinding("b-mail")
	if !ok {
		t.Fatal("the native email binding was removed from workspace state")
	}
	if !binding.IsNativeEmail() {
		t.Fatalf("binding %+v no longer classifies as native email", binding)
	}
}

// A workspace whose ONLY binding is native email resolves cleanly with no MCP
// servers, rather than erroring out.
func TestResolver_OnlyNativeEmailBindingResolvesWithNoServers(t *testing.T) {
	ws := &Workspace{
		ID:             "ws-mail-only",
		AgentInstances: []AgentInstance{{ID: "inst-inbox", Name: "Inbox", NodeID: "inbox-node"}},
		MCPBindings: []MCPBinding{
			{ID: "b-mail", ServerName: "gmail", Enabled: true, RuntimeKind: RuntimeKindNativeEmail},
		},
	}
	templates := &recordingTemplateLookup{}
	resolver := NewAgentRuntimeResolver(
		&resolverAgentStoreStub{agents: map[string]*agent.Agent{"Inbox": {}}},
		newTestWorkspaceStore(t, ws), &runtimeRegistryStub{}, templates,
	)

	resolved, err := resolver.ResolveAgentForWorkspace("Inbox", "ws-mail-only", "inbox-node")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(resolved.MCPServers) != 0 {
		t.Fatalf("MCP servers = %v, want none", resolved.MCPServers)
	}
	if len(templates.asked) != 0 {
		t.Fatalf("looked up templates %v for a mail-only workspace", templates.asked)
	}
}

// Tool allowlist bookkeeping must skip native bindings too (FR 25) — an
// allowlist keyed on a runtime server that will never exist is dead weight the
// exposure checks would have to reason about.
func TestResolver_NativeEmailBindingIsNotInToolAllowlist(t *testing.T) {
	gmail := MCPBinding{
		ID: "b-mail", ServerName: "gmail", Enabled: true,
		RuntimeKind:  RuntimeKindNativeEmail,
		AllowedTools: []string{"mail_search_threads"},
		Config:       map[string]any{"account_id": "acct-1"},
	}
	resolver, _, _ := emailOpsResolver(t, gmail)

	resolved, err := resolver.ResolveAgentForWorkspace("Inbox", "ws-email-ops", "inbox-node")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for name := range resolved.MCPToolAllowlist {
		if strings.Contains(name, "gmail") {
			t.Fatalf("allowlist carries a native email entry: %s", name)
		}
	}
}

// FR 24, 28: the exported direct-materialization entry point fails closed rather
// than registering a stand-in server for a native or unknown binding.
func TestMaterializeRuntimeBinding_RefusesNonMCPBindings(t *testing.T) {
	resolver, templates, registry := emailOpsResolver(t, MCPBinding{ID: "b-mail", ServerName: "gmail", Enabled: true})

	cases := map[string]MCPBinding{
		"native email": {ID: "b-mail", ServerName: "gmail", RuntimeKind: RuntimeKindNativeEmail},
		"legacy email": {ID: "b-mail2", ServerName: "email"},
		"unknown kind": {ID: "b-x", ServerName: "filesystem", RuntimeKind: BindingRuntimeKind("native_calendar")},
	}
	for name, binding := range cases {
		t.Run(name, func(t *testing.T) {
			runtimeName, err := resolver.MaterializeRuntimeBinding("ws-email-ops", binding)
			if err == nil {
				t.Fatalf("materialized %q; a non-MCP binding must fail closed", runtimeName)
			}
			if templates.askedFor(binding.ServerName) && binding.ServerName != "filesystem" {
				t.Fatalf("looked up a template for %q before failing", binding.ServerName)
			}
			if len(registry.configs) != 0 {
				t.Fatalf("registered %d servers; expected none", len(registry.configs))
			}
		})
	}
}

// A real MCP binding whose server name merely sounds like mail is unaffected.
func TestResolver_ExplicitMCPKindOnEmailNameStillMaterializes(t *testing.T) {
	ws := &Workspace{
		ID:             "ws-x",
		AgentInstances: []AgentInstance{{ID: "i1", Name: "Coder", NodeID: "n1"}},
		MCPBindings: []MCPBinding{
			{ID: "b-1", ServerName: "email", Enabled: true, RuntimeKind: RuntimeKindMCP},
		},
	}
	templates := &recordingTemplateLookup{servers: map[string]mcp.ServerConfig{
		"email": {Name: "email", Command: "email-mcp"},
	}}
	resolver := NewAgentRuntimeResolver(
		&resolverAgentStoreStub{agents: map[string]*agent.Agent{"Coder": {}}},
		newTestWorkspaceStore(t, ws), &runtimeRegistryStub{}, templates,
	)

	resolved, err := resolver.ResolveAgentForWorkspace("Coder", "ws-x", "n1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(resolved.MCPServers) != 1 {
		t.Fatalf("MCP servers = %v, want the email MCP runtime", resolved.MCPServers)
	}
}

func (r *runtimeRegistryStub) namesContaining(substr string) []string {
	var out []string
	for name := range r.configs {
		if strings.Contains(strings.ToLower(name), strings.ToLower(substr)) {
			out = append(out, name)
		}
	}
	return out
}
