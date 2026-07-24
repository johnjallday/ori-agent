package mcp

import sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

// toolResultTextHook, when set, may rewrite each text block of a tool result
// before it is returned to callers (and thence the LLM). It receives the server
// URL, the block's text, and its zero-based index within the result; returning
// the text unchanged is a no-op. The host uses it to fence and bound untrusted
// Google Drive content (FR 71, 73). This package stays free of the Drive domain
// — the policy is wired in via SetToolResultTextHook.
var toolResultTextHook func(serverURL, text string, blockIndex int) string

// SetToolResultTextHook installs the result-text policy applied in
// Server.CallTool. Passing nil clears it.
func SetToolResultTextHook(fn func(serverURL, text string, blockIndex int) string) {
	toolResultTextHook = fn
}

// sanitizeResultText applies the installed result-text policy to every
// TextContent block of result, in place. With no policy installed, or a nil
// result, it does nothing — so non-policied servers are untouched.
func sanitizeResultText(serverURL string, result *sdkmcp.CallToolResult) {
	if toolResultTextHook == nil || result == nil {
		return
	}
	for i, block := range result.Content {
		if tc, ok := block.(*sdkmcp.TextContent); ok {
			tc.Text = toolResultTextHook(serverURL, tc.Text, i)
		}
	}
}
