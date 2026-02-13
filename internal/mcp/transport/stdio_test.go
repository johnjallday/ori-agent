package transport

import "testing"

func TestIsMCPPingRequest(t *testing.T) {
	if !isMCPPingRequest([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)) {
		t.Fatal("expected ping request to be detected")
	}
	if isMCPPingRequest([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)) {
		t.Fatal("non-ping request should not be detected as ping")
	}
}

func TestIsMCPPingResponse(t *testing.T) {
	if !isMCPPingResponse([]byte(`{"result":{},"jsonrpc":"2.0","id":1}`)) {
		t.Fatal("expected ping response to be detected")
	}
	if isMCPPingResponse([]byte(`{"result":{"tools":[]},"jsonrpc":"2.0","id":2}`)) {
		t.Fatal("non-empty result should not be treated as ping response")
	}
}

func TestShouldLogMCPWireMessage_PingsQuietByDefault(t *testing.T) {
	t.Setenv("ORI_MCP_LOG_PINGS", "")
	t.Setenv("ORI_VERBOSE", "")

	if shouldLogMCPWireMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)) {
		t.Fatal("expected ping request logs to be suppressed by default")
	}
	if shouldLogMCPWireMessage([]byte(`{"result":{},"jsonrpc":"2.0","id":1}`)) {
		t.Fatal("expected ping response logs to be suppressed by default")
	}
}

func TestShouldLogMCPWireMessage_PingsEnabledByEnv(t *testing.T) {
	t.Setenv("ORI_MCP_LOG_PINGS", "true")
	t.Setenv("ORI_VERBOSE", "")

	if !shouldLogMCPWireMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)) {
		t.Fatal("expected ping request logs to be enabled")
	}
	if !shouldLogMCPWireMessage([]byte(`{"result":{},"jsonrpc":"2.0","id":1}`)) {
		t.Fatal("expected ping response logs to be enabled")
	}
}

func TestShouldLogMCPWireMessage_NonPingAlwaysLogs(t *testing.T) {
	t.Setenv("ORI_MCP_LOG_PINGS", "")
	t.Setenv("ORI_VERBOSE", "")

	if !shouldLogMCPWireMessage([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)) {
		t.Fatal("expected non-ping request to remain logged")
	}
	if !shouldLogMCPWireMessage([]byte(`{"result":{"tools":[]},"jsonrpc":"2.0","id":2}`)) {
		t.Fatal("expected non-ping response to remain logged")
	}
}
