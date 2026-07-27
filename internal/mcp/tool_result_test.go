package mcp

import (
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSanitizeResultText_FencesEachBlockInPlace(t *testing.T) {
	SetToolResultTextHook(func(serverURL, text string, i int) string {
		if strings.Contains(serverURL, "drivemcp.googleapis.com") {
			return "FENCED:" + text
		}
		return text
	})
	defer SetToolResultTextHook(nil)

	res := &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: "file one"},
			&sdkmcp.TextContent{Text: "file two"},
		},
	}
	sanitizeResultText("https://drivemcp.googleapis.com/mcp/v1", res)

	if got := res.Content[0].(*sdkmcp.TextContent).Text; got != "FENCED:file one" {
		t.Errorf("block 0 not fenced in place: %q", got)
	}
	if got := res.Content[1].(*sdkmcp.TextContent).Text; got != "FENCED:file two" {
		t.Errorf("block 1 not fenced in place: %q", got)
	}
}

func TestSanitizeResultText_NonPoliciedServerUntouched(t *testing.T) {
	SetToolResultTextHook(func(serverURL, text string, i int) string {
		if strings.Contains(serverURL, "drivemcp.googleapis.com") {
			return "FENCED:" + text
		}
		return text
	})
	defer SetToolResultTextHook(nil)

	res := &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "x"}}}
	sanitizeResultText("https://example.com/mcp", res)
	if got := res.Content[0].(*sdkmcp.TextContent).Text; got != "x" {
		t.Errorf("non-Drive result must be untouched, got %q", got)
	}
}

func TestSanitizeResultText_NoHookNoPanic(t *testing.T) {
	SetToolResultTextHook(nil)
	sanitizeResultText("https://drivemcp.googleapis.com/mcp/v1", nil) // nil result must be safe
	res := &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "y"}}}
	sanitizeResultText("https://drivemcp.googleapis.com/mcp/v1", res)
	if got := res.Content[0].(*sdkmcp.TextContent).Text; got != "y" {
		t.Errorf("with no hook installed, content must be unchanged, got %q", got)
	}
}
