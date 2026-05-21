package chathttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/toolapi"
)

// ConvertUploadedFilesToAttachments converts UploadedFile slice to FileAttachment slice.
// It handles both text content and base64-encoded binary content.
func ConvertUploadedFilesToAttachments(files []UploadedFile) []toolapi.FileAttachment {
	attachments := make([]toolapi.FileAttachment, 0, len(files))

	for _, f := range files {
		var content []byte

		// Try to decode as base64 first (for binary files)
		if decoded, err := base64.StdEncoding.DecodeString(f.Content); err == nil {
			content = decoded
		} else {
			// If not base64, treat as text content
			content = []byte(f.Content)
		}

		attachments = append(attachments, toolapi.FileAttachment{
			Name:    f.Name,
			Type:    f.Type,
			Size:    f.Size,
			Content: content,
		})
	}

	return attachments
}

// DirectToolCommand represents a parsed direct tool invocation command
type DirectToolCommand struct {
	ToolName string
	Args     string
	Files    []toolapi.FileAttachment // Optional file attachments
}

// parseDirectToolCommand parses a direct tool command in the format:
// /tool <tool_name> <json_args>
// Examples:
//
//	/tool math {"operation": "add", "a": 5, "b": 3}
//	/tool weather {"city": "San Francisco"}
func parseDirectToolCommand(message string) (*DirectToolCommand, error) {
	// Remove leading/trailing whitespace
	message = strings.TrimSpace(message)

	// Check if message starts with /tool
	if !strings.HasPrefix(message, "/tool ") {
		return nil, fmt.Errorf("command must start with '/tool '")
	}

	// Remove the /tool prefix
	message = strings.TrimPrefix(message, "/tool ")
	message = strings.TrimSpace(message)

	// Split into tool name and args
	// Find the first '{' to identify where the JSON args start
	jsonStart := strings.Index(message, "{")
	if jsonStart == -1 {
		return nil, fmt.Errorf("missing JSON arguments. Format: /tool <tool_name> {\"key\": \"value\"}")
	}

	// Extract tool name (everything before the '{')
	toolName := strings.TrimSpace(message[:jsonStart])
	if toolName == "" {
		return nil, fmt.Errorf("tool name is required. Format: /tool <tool_name> {\"key\": \"value\"}")
	}

	// Extract JSON args (everything from '{' onwards)
	args := strings.TrimSpace(message[jsonStart:])

	// Validate that args is valid JSON
	var testJSON map[string]any
	if err := json.Unmarshal([]byte(args), &testJSON); err != nil {
		return nil, fmt.Errorf("invalid JSON arguments: %v. Format: /tool <tool_name> {\"key\": \"value\"}", err)
	}

	return &DirectToolCommand{
		ToolName: toolName,
		Args:     args,
	}, nil
}

// DirectToolResult represents the result of a direct tool execution
type DirectToolResult struct {
	Success         bool                      `json:"success"`
	ToolName        string                    `json:"tool_name"`
	ToolArgs        string                    `json:"tool_args"`
	Result          string                    `json:"result"`
	Error           string                    `json:"error,omitempty"`
	ExecutionTimeMs int64                     `json:"execution_time_ms"`
	Structured      bool                      `json:"structured,omitempty"`
	DisplayType     string                    `json:"display_type,omitempty"`
	Title           string                    `json:"title,omitempty"`
	Description     string                    `json:"description,omitempty"`
	StructuredData  *toolapi.StructuredResult `json:"-"` // Don't serialize, used internally
}

// executeDirectTool executes a tool directly without LLM decision-making
func (h *Handler) executeDirectTool(ctx context.Context, ag *resolvedChatAgent, cmd *DirectToolCommand) *DirectToolResult {
	startTime := time.Now()
	result := &DirectToolResult{
		ToolName: cmd.ToolName,
		ToolArgs: cmd.Args,
	}

	if ag == nil || ag.Agent == nil || !isUtilityToolAllowedForAgent(ag.Agent, cmd.ToolName) {
		result.Success = false
		result.Error = "tool disabled by agent policy"
		result.Result = fmt.Sprintf("❌ %s", disallowedUtilityToolMessage(cmd.ToolName))
		result.ExecutionTimeMs = time.Since(startTime).Milliseconds()
		return result
	}

	// Find the tool (with lazy loading support)
	tool, found := h.findTool(ag, cmd.ToolName)
	if !found {
		// Provide helpful error with available tools
		availableTools := h.getAvailableToolNames(ag)
		result.Success = false
		result.Error = fmt.Sprintf("Tool '%s' not found. Available tools: %s", cmd.ToolName, strings.Join(availableTools, ", "))
		result.Result = fmt.Sprintf("❌ %s", result.Error)
		result.ExecutionTimeMs = time.Since(startTime).Milliseconds()
		return result
	}

	// Execute the tool with timeout
	toolCtx, toolCancel := context.WithTimeout(ctx, 30*time.Second)
	defer toolCancel()

	logger.Debug("Direct tool execution: with args", logger.Fields{"tool": cmd.ToolName, "args": cmd.Args})

	var toolResult string
	var err error

	toolResult, err = ExecuteToolWithFilesDebug(toolCtx, tool, cmd.ToolName, cmd.Args, cmd.Files)
	duration := time.Since(startTime)

	// Handle execution error
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		result.Result = fmt.Sprintf("Error executing %s: %v", cmd.ToolName, err)
		result.ExecutionTimeMs = duration.Milliseconds()
		logger.Error("Direct tool execution failed", logger.Fields{"tool": err})
		return result
	}

	// Check if result is a structured result
	if sr, err := toolapi.ParseStructuredResult(toolResult); err == nil {
		result.Structured = true
		result.DisplayType = string(sr.DisplayType)
		result.Title = sr.Title
		result.Description = sr.Description
		result.StructuredData = sr
	}

	// Success
	result.Success = true
	result.Result = toolResult
	result.ExecutionTimeMs = duration.Milliseconds()

	logger.Info("Direct tool execution completed in ms", logger.Fields{"tool": result.ExecutionTimeMs, "toolname": cmd.ToolName})

	return result
}

// getAvailableToolNames returns a list of available tool names for the current agent
func (h *Handler) getAvailableToolNames(ag *resolvedChatAgent) []string {
	if ag == nil || ag.Agent == nil {
		return nil
	}

	var toolNames []string

	// Add native utility tools first
	if h.utilityRegistry != nil {
		for _, def := range h.utilityRegistry.ListToolDefinitions() {
			if ag == nil || ag.Agent == nil || !isUtilityToolAllowedForAgent(ag.Agent, def.Name) {
				continue
			}
			toolNames = append(toolNames, def.Name)
		}
	}

	// Add workspace-scoped tools
	if ag.WorkspaceTools != nil {
		for _, wt := range ag.WorkspaceTools.Tools() {
			toolNames = append(toolNames, wt.Definition().Name)
		}
	}

	// Add MCP tools
	if h.mcpRegistry != nil && ag != nil && len(ag.MCPServers) > 0 {
		for _, serverName := range ag.MCPServers {
			mcpTools, err := h.getMCPToolsForServer(serverName)
			if err != nil {
				continue
			}
			for _, mcpTool := range mcpTools {
				toolNames = append(toolNames, mcpTool.Definition().Name)
			}
		}
	}

	return toolNames
}

// formatDirectToolResponse formats a direct tool result into a chat response
func formatDirectToolResponse(result *DirectToolResult) map[string]any {
	receipt := buildActionReceipt(
		"direct_tool",
		"Executed direct tool command",
		"explicit /tool command",
		result.ToolName,
		result.ToolArgs,
		result.Result,
		result.ExecutionTimeMs,
		result.Success,
		result.Error,
	)

	response := attachActionReceipts(attachRouteMetadata(map[string]any{
		"response":          result.Result,
		"direct_tool_call":  true,
		"tool_name":         result.ToolName,
		"execution_time_ms": result.ExecutionTimeMs,
		"success":           result.Success,
	}, chatRouteMetadata{
		Mode:      routeModeDirectTool,
		ToolName:  result.ToolName,
		ToolCount: 1,
	}), []ActionReceipt{receipt})

	// Add structured result metadata if available
	if result.Structured {
		response["structured"] = true
		response["displayType"] = result.DisplayType
		response["title"] = result.Title
		response["description"] = result.Description
	}

	// Add error if present
	if result.Error != "" {
		response["error"] = result.Error
	}

	return response
}
