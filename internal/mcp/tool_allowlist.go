package mcp

// toolExposureHook, when set, gates which tools each server may expose and
// execute. It returns true when toolName on the server reachable at serverURL is
// permitted. A nil hook (the default) permits every tool — only servers the host
// installs a policy for are ever restricted, so non-policied servers keep their
// existing behavior.
//
// This package stays free of the connection/product domain: the host wires the
// actual policy (e.g. Google Drive's fail-closed read-only allowlist) through
// SetToolExposureHook, the same decoupled pattern used for the Google MCP
// identity hook.
var toolExposureHook func(serverURL, toolName string) bool

// SetToolExposureHook installs the tool-exposure policy enforced at BOTH the
// listing boundary (GetAllTools / GetToolsForServer) and the execution boundary
// (Server.CallTool), so a denied tool is neither advertised nor runnable even if
// named directly. Passing nil clears the policy. The host uses this to enforce
// the fail-closed Google Drive read-only allowlist server-side (FR 66, 67).
func SetToolExposureHook(fn func(serverURL, toolName string) bool) {
	toolExposureHook = fn
}

// toolAllowed reports whether a tool may be exposed or executed for the server
// at serverURL. With no policy installed it permits everything.
func toolAllowed(serverURL, toolName string) bool {
	if toolExposureHook == nil {
		return true
	}
	return toolExposureHook(serverURL, toolName)
}
