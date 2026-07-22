package workspace

import (
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"testing"
)

// allowlistRegistryStub returns a fixed set of tools for a single server name,
// regardless of running state, so tests can focus purely on the allowlist
// filter rather than start/lazy-start plumbing.
type allowlistRegistryStub struct {
	serverName string
	tools      []toolapi.Tool
}

func (s *allowlistRegistryStub) GetToolsForServer(name string) ([]toolapi.Tool, error) {
	if name != s.serverName {
		return nil, nil
	}
	return s.tools, nil
}
func (s *allowlistRegistryStub) StartServer(string) error        { return nil }
func (s *allowlistRegistryStub) ListServers() []mcp.ServerConfig { return nil }

func TestGetAgentMCPTools_RestrictsToAllowlistedNames(t *testing.T) {
	const runtimeName = "ws:ws-x:mcp:calendar-mcp:binding-1"
	reg := &allowlistRegistryStub{
		serverName: runtimeName,
		tools: []toolapi.Tool{
			taskHandlerToolStub{name: "list_events"},
			taskHandlerToolStub{name: "list_calendars"},
			taskHandlerToolStub{name: "delete_event"},
		},
	}
	h := &LLMTaskHandler{mcpRegistry: reg}
	ag := &resolvedTaskAgent{
		MCPServers:       []string{runtimeName},
		MCPToolAllowlist: map[string][]string{runtimeName: {"list_events", "list_calendars"}},
	}

	tools := h.getAgentMCPTools(ag)
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2 (delete_event must be filtered out): %+v", len(tools), toolNames(tools))
	}
	for _, tool := range tools {
		name := tool.Definition().Name
		if name != "list_events" && name != "list_calendars" {
			t.Errorf("unexpected tool exposed despite allowlist: %s", name)
		}
	}

	// findTool must not resolve the disallowed tool either, since invocation
	// re-derives from the same filtered list.
	if _, found := h.findTool(ag, Task{}, "delete_event"); found {
		t.Error("findTool resolved a tool outside the binding's AllowedTools")
	}
	if _, found := h.findTool(ag, Task{}, "list_events"); !found {
		t.Error("findTool failed to resolve an allowlisted tool")
	}
}

func TestGetAgentMCPTools_NoAllowlistEntryKeepsLegacyAllToolsBehavior(t *testing.T) {
	const runtimeName = "ws:ws-x:mcp:browser:binding-1"
	reg := &allowlistRegistryStub{
		serverName: runtimeName,
		tools: []toolapi.Tool{
			taskHandlerToolStub{name: "browser_navigate"},
			taskHandlerToolStub{name: "browser_click"},
		},
	}
	h := &LLMTaskHandler{mcpRegistry: reg}
	ag := &resolvedTaskAgent{MCPServers: []string{runtimeName}} // no MCPToolAllowlist at all

	tools := h.getAgentMCPTools(ag)
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2 (nil allowlist must preserve legacy all-tools behavior): %+v", len(tools), toolNames(tools))
	}
}

func TestGetAgentMCPTools_EmptyAllowlistDeniesAllToolsForThatServer(t *testing.T) {
	const runtimeName = "ws:ws-x:mcp:calendar-mcp:binding-1"
	reg := &allowlistRegistryStub{
		serverName: runtimeName,
		tools: []toolapi.Tool{
			taskHandlerToolStub{name: "list_events"},
		},
	}
	h := &LLMTaskHandler{mcpRegistry: reg}
	ag := &resolvedTaskAgent{
		MCPServers:       []string{runtimeName},
		MCPToolAllowlist: map[string][]string{runtimeName: {}},
	}

	tools := h.getAgentMCPTools(ag)
	if len(tools) != 0 {
		t.Fatalf("got %d tools, want 0 (empty AllowedTools must deny all): %+v", len(tools), toolNames(tools))
	}
}

func toolNames(tools []toolapi.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Definition().Name)
	}
	return names
}
