package mcp

import "testing"

func TestRegistryUpsertServer_ReplacesChangedConfig(t *testing.T) {
	registry := NewRegistry()

	initial := ServerConfig{
		Name: "ws:one:mcp:filesystem",
		Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/one"},
	}
	if err := registry.UpsertServer(initial); err != nil {
		t.Fatalf("UpsertServer(initial) error = %v", err)
	}

	updated := ServerConfig{
		Name: "ws:one:mcp:filesystem",
		Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/two"},
	}
	if err := registry.UpsertServer(updated); err != nil {
		t.Fatalf("UpsertServer(updated) error = %v", err)
	}

	server, err := registry.GetServer(updated.Name)
	if err != nil {
		t.Fatalf("GetServer(updated) error = %v", err)
	}
	if got := server.GetConfig().Args[len(server.GetConfig().Args)-1]; got != "/tmp/two" {
		t.Fatalf("server args were not updated, got=%q full=%v", got, server.GetConfig().Args)
	}
}
