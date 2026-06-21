package llm

import "testing"

// TestProviderNativeMCPCapabilities locks the capability matrix: only the CLI
// providers run their own MCP loop (SupportsNativeMCP), and they never claim
// SupportsTools (which would engage ori-agent's internal tool loop). The native
// API providers are the inverse. Capabilities() is called on zero-value structs
// so the test does not require the CLIs on PATH.
func TestProviderNativeMCPCapabilities(t *testing.T) {
	cases := []struct {
		name       string
		caps       ProviderCapabilities
		wantNative bool
		wantTools  bool
	}{
		{"codex", (&CodexProvider{}).Capabilities(), true, false},
		{"claude_code", (&ClaudeCodeProvider{}).Capabilities(), true, false},
		{"openai", (&OpenAIProvider{}).Capabilities(), false, true},
		{"claude", (&ClaudeProvider{}).Capabilities(), false, true},
	}
	for _, tc := range cases {
		if tc.caps.SupportsNativeMCP != tc.wantNative {
			t.Errorf("%s SupportsNativeMCP=%v, want %v", tc.name, tc.caps.SupportsNativeMCP, tc.wantNative)
		}
		if tc.caps.SupportsTools != tc.wantTools {
			t.Errorf("%s SupportsTools=%v, want %v", tc.name, tc.caps.SupportsTools, tc.wantTools)
		}
	}
}

// TestStructuredOutputRequestNoMCPByDefault guards the parse path: a request
// built without MCP servers (as the system-model auto-task parser does) carries
// none, so the CLI providers run text-only for parsing.
func TestStructuredOutputRequestNoMCPByDefault(t *testing.T) {
	req := StructuredOutputRequest{Model: "gpt-5.5", SystemPrompt: "parse", Schema: map[string]any{}}
	if len(req.MCPServers) != 0 {
		t.Errorf("parse-path request must carry no MCP servers, got %d", len(req.MCPServers))
	}
	if req.WorkspaceID != "" {
		t.Errorf("parse-path request must not set WorkspaceID, got %q", req.WorkspaceID)
	}
}
