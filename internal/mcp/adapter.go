package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/toolapi"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPAdapter adapts an MCP server to the toolapi.Tool interface
// This allows MCP tools to be used seamlessly alongside native tools
type MCPAdapter struct {
	server      *Server
	tool        Tool
	agentCtx    toolapi.AgentContext
	hasAgentCtx bool
}

// NewMCPAdapter creates a new adapter for an MCP tool
func NewMCPAdapter(server *Server, tool Tool) *MCPAdapter {
	return &MCPAdapter{
		server: server,
		tool:   tool,
	}
}

// Definition converts MCP tool schema to generic tool definition
// This is the bridge that makes MCP tools compatible with any LLM provider
func (a *MCPAdapter) Definition() toolapi.ToolDefinition {
	// Convert MCP inputSchema to generic parameters format
	parameters := map[string]any(nil)
	switch schema := a.tool.InputSchema.(type) {
	case map[string]any:
		parameters = schema
	case json.RawMessage:
		_ = json.Unmarshal(schema, &parameters)
	default:
		if schema != nil {
			data, err := json.Marshal(schema)
			if err == nil {
				_ = json.Unmarshal(data, &parameters)
			}
		}
	}
	if parameters == nil {
		parameters = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	return toolapi.ToolDefinition{
		Name:        a.tool.Name,
		Description: a.tool.Description,
		Parameters:  parameters,
	}
}

// Call executes the MCP tool and returns the result
func (a *MCPAdapter) Call(ctx context.Context, args string) (string, error) {
	// Parse arguments
	var arguments map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal([]byte(args), &arguments); err != nil {
			return "", fmt.Errorf("failed to parse arguments: %w", err)
		}
	}
	arguments = normalizeFilesystemArguments(a.tool.Name, arguments, a.server.GetConfig())

	// Call the MCP tool
	result, err := a.server.CallTool(ctx, a.tool.Name, arguments)
	if err != nil {
		return "", fmt.Errorf("MCP tool call failed: %w", err)
	}

	if result.IsError {
		// Extract error message from content
		errorMsg := "tool returned error"
		if len(result.Content) > 0 {
			if text, ok := result.Content[0].(*sdkmcp.TextContent); ok && strings.TrimSpace(text.Text) != "" {
				errorMsg = text.Text
			}
		}
		return "", fmt.Errorf("%s", errorMsg)
	}

	// Convert content to string result
	formatted, err := a.formatResult(result)
	if err != nil {
		return "", err
	}
	return annotateGetFileInfoResult(a.tool.Name, formatted, arguments), nil
}

// formatResult converts MCP tool result content to a string
func (a *MCPAdapter) formatResult(result *ToolCallResult) (string, error) {
	if len(result.Content) == 0 {
		return "", nil
	}

	// If single text item, return it directly
	if len(result.Content) == 1 {
		if text, ok := result.Content[0].(*sdkmcp.TextContent); ok {
			return text.Text, nil
		}
	}

	// Multiple items or complex content - return as JSON
	formatted := make([]map[string]any, 0, len(result.Content))
	for _, item := range result.Content {
		formattedItem := map[string]any{}
		switch c := item.(type) {
		case *sdkmcp.TextContent:
			formattedItem["type"] = "text"
			formattedItem["text"] = c.Text
		case *sdkmcp.ImageContent:
			formattedItem["type"] = "image"
			formattedItem["mimeType"] = c.MIMEType
			formattedItem["data"] = base64.StdEncoding.EncodeToString(c.Data)
		case *sdkmcp.EmbeddedResource:
			formattedItem["type"] = "resource"
			if c.Resource != nil {
				formattedItem["uri"] = c.Resource.URI
				if c.Resource.MIMEType != "" {
					formattedItem["mimeType"] = c.Resource.MIMEType
				}
				if c.Resource.Text != "" {
					formattedItem["text"] = c.Resource.Text
				}
				if len(c.Resource.Blob) > 0 {
					formattedItem["blob"] = base64.StdEncoding.EncodeToString(c.Resource.Blob)
				}
			}
		case *sdkmcp.ResourceLink:
			formattedItem["type"] = "resource_link"
			formattedItem["uri"] = c.URI
			if c.Name != "" {
				formattedItem["name"] = c.Name
			}
			if c.MIMEType != "" {
				formattedItem["mimeType"] = c.MIMEType
			}
		default:
			raw, err := json.Marshal(c)
			if err != nil {
				return "", fmt.Errorf("failed to marshal unsupported content item: %w", err)
			}
			if err := json.Unmarshal(raw, &formattedItem); err != nil {
				return "", fmt.Errorf("failed to decode unsupported content item: %w", err)
			}
			if _, ok := formattedItem["type"]; !ok {
				formattedItem["type"] = fmt.Sprintf("%T", c)
			}
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
func (a *MCPAdapter) SetAgentContext(ctx toolapi.AgentContext) {
	a.agentCtx = ctx
	a.hasAgentCtx = true
}

// Version returns the adapter version (implements VersionedTool)
func (a *MCPAdapter) Version() string {
	return "mcp-adapter-0.1.0"
}

// Ensure MCPAdapter implements required interfaces
var _ toolapi.Tool = (*MCPAdapter)(nil)
var _ toolapi.AgentAwareTool = (*MCPAdapter)(nil)
var _ toolapi.VersionedTool = (*MCPAdapter)(nil)

func normalizeFilesystemArguments(toolName string, arguments map[string]any, serverConfig ServerConfig) map[string]any {
	if len(arguments) == 0 || !isFilesystemTool(toolName) {
		return arguments
	}

	basePath := resolveFilesystemBasePath(serverConfig)
	if basePath == "" {
		if homeDir, err := os.UserHomeDir(); err == nil {
			basePath = homeDir
		}
	}
	if basePath == "" {
		return arguments
	}

	normalized := make(map[string]any, len(arguments))
	for key, value := range arguments {
		textValue, ok := value.(string)
		if !ok || !isFilesystemPathArgumentKey(key) {
			normalized[key] = value
			continue
		}
		normalized[key] = normalizeFilesystemPathValue(textValue, basePath)
	}

	return normalized
}

func isFilesystemTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "list_directory",
		"list_directory_with_sizes",
		"search_files",
		"get_file_info",
		"read_file",
		"write_file",
		"move_file",
		"copy_file",
		"delete_file",
		"create_directory":
		return true
	default:
		return false
	}
}

func isFilesystemPathArgumentKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "path", "source", "destination":
		return true
	default:
		return false
	}
}

func resolveFilesystemBasePath(serverConfig ServerConfig) string {
	// Server-filesystem accepts one or more allowed directories as args after package name.
	for i := 0; i < len(serverConfig.Args); i++ {
		candidate := strings.TrimSpace(serverConfig.Args[i])
		if candidate == "" {
			continue
		}
		if strings.HasPrefix(candidate, "@") || strings.HasPrefix(candidate, "-") {
			continue
		}
		if filepath.IsAbs(candidate) {
			return filepath.Clean(candidate)
		}
	}
	return ""
}

func normalizeFilesystemPathValue(pathValue, basePath string) string {
	trimmed := strings.TrimSpace(pathValue)
	if trimmed == "" {
		return pathValue
	}
	// Keep URLs and opaque non-path values as-is.
	if strings.Contains(trimmed, "://") {
		return pathValue
	}

	if strings.HasPrefix(trimmed, "~") {
		if homeDir, err := os.UserHomeDir(); err == nil {
			if trimmed == "~" {
				return homeDir
			}
			if strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, "~\\") {
				return filepath.Join(homeDir, strings.TrimLeft(trimmed[1:], `/\`))
			}
		}
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}

	cleanBase := filepath.Clean(basePath)
	cleanRelative := filepath.Clean(trimmed)
	candidate := filepath.Join(cleanBase, cleanRelative)
	if cleanBase != "" {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		if collapsedRelative, ok := collapseRedundantBasePrefix(cleanRelative, cleanBase); ok {
			if collapsedRelative == "." {
				return cleanBase
			}
			return filepath.Join(cleanBase, collapsedRelative)
		}
	}

	return candidate
}

func collapseRedundantBasePrefix(relativePath, basePath string) (string, bool) {
	baseName := strings.TrimSpace(filepath.Base(filepath.Clean(basePath)))
	if baseName == "" || baseName == "." || baseName == string(filepath.Separator) {
		return "", false
	}

	segments := splitFilesystemPathSegments(relativePath)
	if len(segments) == 0 || !strings.EqualFold(segments[0], baseName) {
		return "", false
	}

	if len(segments) == 1 {
		return ".", true
	}
	return filepath.Join(segments[1:]...), true
}

func splitFilesystemPathSegments(pathValue string) []string {
	replaced := strings.ReplaceAll(pathValue, "\\", "/")
	rawSegments := strings.Split(replaced, "/")
	segments := make([]string, 0, len(rawSegments))
	for _, segment := range rawSegments {
		trimmed := strings.TrimSpace(segment)
		if trimmed == "" || trimmed == "." {
			continue
		}
		segments = append(segments, trimmed)
	}
	return segments
}

func annotateGetFileInfoResult(toolName, result string, arguments map[string]any) string {
	if !strings.EqualFold(strings.TrimSpace(toolName), "get_file_info") {
		return result
	}
	if strings.Contains(strings.ToLower(result), "name:") || strings.Contains(strings.ToLower(result), "path:") {
		return result
	}

	pathArg, ok := arguments["path"].(string)
	if !ok || strings.TrimSpace(pathArg) == "" {
		return result
	}

	base := filepath.Base(pathArg)
	return fmt.Sprintf("path: %s\nname: %s\n%s", pathArg, base, result)
}
