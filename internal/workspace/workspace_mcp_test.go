package workspace

import "testing"

func TestWorkspaceMCPBindingLifecycle(t *testing.T) {
	ws := &Workspace{}

	if err := ws.UpsertMCPBinding(WorkspaceMCPBinding{
		ID:         "binding-1",
		ServerName: "filesystem",
		Enabled:    true,
		Scope: map[string]interface{}{
			"roots": []string{"/tmp/repo"},
		},
	}); err != nil {
		t.Fatalf("UpsertMCPBinding returned error: %v", err)
	}

	binding, ok := ws.GetMCPBinding("binding-1")
	if !ok || binding == nil {
		t.Fatalf("expected binding to exist after upsert")
	}
	if binding.ServerName != "filesystem" {
		t.Fatalf("expected server_name filesystem, got %q", binding.ServerName)
	}

	if err := ws.SetAgentMCPAccess(WorkspaceAgentMCPAccess{
		AgentInstanceID:   "agent-1",
		EnabledBindingIDs: []string{"binding-1", "binding-1"},
	}); err != nil {
		t.Fatalf("SetAgentMCPAccess returned error: %v", err)
	}

	access, ok := ws.GetAgentMCPAccess("agent-1")
	if !ok || access == nil {
		t.Fatalf("expected agent MCP access to exist")
	}
	if len(access.EnabledBindingIDs) != 1 || access.EnabledBindingIDs[0] != "binding-1" {
		t.Fatalf("expected deduped enabled binding IDs, got %v", access.EnabledBindingIDs)
	}

	if err := ws.DeleteMCPBinding("binding-1"); err != nil {
		t.Fatalf("DeleteMCPBinding returned error: %v", err)
	}

	if _, ok := ws.GetMCPBinding("binding-1"); ok {
		t.Fatalf("expected binding to be deleted")
	}

	access, ok = ws.GetAgentMCPAccess("agent-1")
	if !ok || access == nil {
		t.Fatalf("expected agent MCP access entry to remain after binding deletion")
	}
	if len(access.EnabledBindingIDs) != 0 {
		t.Fatalf("expected deleted binding to be removed from access entry, got %v", access.EnabledBindingIDs)
	}
}
