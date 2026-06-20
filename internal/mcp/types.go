package mcp

import sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

type Tool = sdkmcp.Tool
type ToolCallResult = sdkmcp.CallToolResult

// Implementation describes an MCP server's reported identity (name/title/version)
// from the initialize handshake.
type Implementation = sdkmcp.Implementation
