package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/johnjallday/ori-agent/pluginapi"
	"github.com/openai/openai-go/v2"
)

// MCPAdapter adapts an MCP server to the pluginapi.Tool interface
// This allows MCP tools to be used seamlessly alongside native plugins
type MCPAdapter struct {
	server      *Server
	tool        Tool
	agentCtx    pluginapi.AgentContext
	hasAgentCtx bool
}

// NewMCPAdapter creates a new adapter for an MCP tool
func NewMCPAdapter(server *Server, tool Tool) *MCPAdapter {
	return &MCPAdapter{
		server: server,
		tool:   tool,
	}
}

// Definition converts MCP tool schema to OpenAI function definition
// This is the bridge that makes MCP tools compatible with any LLM provider
func (a *MCPAdapter) Definition() openai.FunctionDefinitionParam {
	// Convert MCP inputSchema to OpenAI parameters format
	parameters := a.tool.InputSchema
	if parameters == nil {
		parameters = map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}

	return openai.FunctionDefinitionParam{
		Name:        a.tool.Name,
		Description: openai.String(a.tool.Description),
		Parameters:  parameters,
	}
}

// Call executes the MCP tool and returns the result
func (a *MCPAdapter) Call(ctx context.Context, args string) (string, error) {
	// Parse arguments
	var arguments map[string]interface{}
	if len(args) > 0 {
		if err := json.Unmarshal([]byte(args), &arguments); err != nil {
			return "", fmt.Errorf("failed to parse arguments: %w", err)
		}
	}

	// Call the MCP tool
	result, err := a.server.CallTool(ctx, a.tool.Name, arguments)
	if err != nil {
		return "", fmt.Errorf("MCP tool call failed: %w", err)
	}

	if result.IsError {
		// Extract error message from content
		errorMsg := "tool returned error"
		if len(result.Content) > 0 && result.Content[0].Type == "text" {
			errorMsg = result.Content[0].Text
		}
		return "", fmt.Errorf("%s", errorMsg)
	}

	// Convert content to string result
	return a.formatResult(result)
}

// formatResult converts MCP tool result content to a string
func (a *MCPAdapter) formatResult(result *ToolCallResult) (string, error) {
	if len(result.Content) == 0 {
		return "", nil
	}

	// If single text item, return it directly
	if len(result.Content) == 1 && result.Content[0].Type == "text" {
		return result.Content[0].Text, nil
	}

	// Multiple items or complex content - return as JSON
	formatted := make([]map[string]interface{}, 0, len(result.Content))
	for _, item := range result.Content {
		formattedItem := map[string]interface{}{
			"type": item.Type,
		}
		switch item.Type {
		case "text":
			formattedItem["text"] = item.Text
		case "image":
			formattedItem["data"] = item.Data
			formattedItem["mimeType"] = item.MimeType
		case "resource":
			formattedItem["uri"] = item.URI
		}
		formatted = append(formatted, formattedItem)
	}

	data, err := json.MarshalIndent(formatted, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format result: %w", err)
	}

	return string(data), nil
}

// SetAgentContext implements AgentAwareTool interface
func (a *MCPAdapter) SetAgentContext(ctx pluginapi.AgentContext) {
	a.agentCtx = ctx
	a.hasAgentCtx = true
}

// Version returns the adapter version (implements VersionedTool)
func (a *MCPAdapter) Version() string {
	return "mcp-adapter-0.1.0"
}

// Ensure MCPAdapter implements required interfaces
var _ pluginapi.Tool = (*MCPAdapter)(nil)
var _ pluginapi.AgentAwareTool = (*MCPAdapter)(nil)
var _ pluginapi.VersionedTool = (*MCPAdapter)(nil)
