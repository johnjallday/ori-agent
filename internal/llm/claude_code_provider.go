package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ClaudeCodeProvider implements the Provider interface using the Claude CLI.
type ClaudeCodeProvider struct {
	cliPath  string
	mcpStore *CLIMCPConfigStore
}

// NewClaudeCodeProvider creates a new Claude Code provider backed by the Claude CLI.
func NewClaudeCodeProvider() (*ClaudeCodeProvider, error) {
	cliPath, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("claude CLI not found: %w", err)
	}
	return &ClaudeCodeProvider{
		cliPath:  cliPath,
		mcpStore: NewCLIMCPConfigStore(),
	}, nil
}

// Name returns the provider name.
func (p *ClaudeCodeProvider) Name() string {
	return "claude_code"
}

// Type returns the provider type.
func (p *ClaudeCodeProvider) Type() ProviderType {
	return ProviderTypeCloud
}

// Capabilities returns Claude Code CLI capabilities.
func (p *ClaudeCodeProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsTools:          false,
		SupportsNativeMCP:      true,
		SupportsStreaming:      false,
		SupportsSystemPrompt:   true,
		SupportsTemperature:    false,
		RequiresAPIKey:         false,
		SupportsCustomEndpoint: false,
		MaxContextWindow:       200000,
		SupportedFormats:       []string{"text"},
	}
}

// ValidateConfig validates the Claude Code configuration.
func (p *ClaudeCodeProvider) ValidateConfig(_ ProviderConfig) error {
	if p.cliPath == "" {
		return fmt.Errorf("claude CLI not available")
	}
	return nil
}

// DefaultModels returns available Claude models from the curated pricing data.
func (p *ClaudeCodeProvider) DefaultModels() []string {
	return []string{"opus", "sonnet", "haiku"}
}

// Chat sends a chat request via the Claude CLI.
func (p *ClaudeCodeProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	prompt := buildClaudeCodePrompt(req.SystemPrompt, req.Messages)
	nat, err := p.prepareNativeMCP(req.MCPServers, req.WorkspaceID, req.WorkspaceDir)
	if err != nil {
		return nil, err
	}
	content, err := p.runClaudeExec(ctx, req.Model, prompt, nil, nat)
	if err != nil {
		return nil, err
	}
	return &ChatResponse{
		Content:  content,
		Model:    req.Model,
		Provider: p.Name(),
	}, nil
}

// StreamChat streams a chat completion response (not yet implemented).
func (p *ClaudeCodeProvider) StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error) {
	return nil, fmt.Errorf("streaming not yet implemented for Claude Code provider")
}

// ChatWithStructuredOutput sends a chat request with a JSON schema using the Claude CLI.
func (p *ClaudeCodeProvider) ChatWithStructuredOutput(ctx context.Context, req StructuredOutputRequest) (*ChatResponse, error) {
	prompt := buildClaudeCodePrompt(req.SystemPrompt, req.Messages)
	nat, err := p.prepareNativeMCP(req.MCPServers, req.WorkspaceID, req.WorkspaceDir)
	if err != nil {
		return nil, err
	}
	content, err := p.runClaudeExec(ctx, req.Model, prompt, req.Schema, nat)
	if err != nil {
		return nil, err
	}
	return &ChatResponse{
		Content:  content,
		Model:    req.Model,
		Provider: p.Name(),
	}, nil
}

// claudeNativeMCP holds the resolved native-MCP execution context for a run.
type claudeNativeMCP struct {
	ConfigPath   string // path passed to --mcp-config
	WorkspaceDir string // working dir / --add-dir scope (may be empty)
}

// prepareNativeMCP ensures the per-workspace Claude MCP config exists for the
// given specs and returns the run context. Returns nil when there are no MCP
// servers (text-only run) — leaving the existing behavior untouched.
func (p *ClaudeCodeProvider) prepareNativeMCP(specs []MCPServerSpec, workspaceID, workspaceDir string) (*claudeNativeMCP, error) {
	if len(specs) == 0 || p.mcpStore == nil {
		return nil, nil
	}
	configPath, err := p.mcpStore.EnsureClaudeConfig(workspaceID, specs)
	if err != nil {
		return nil, fmt.Errorf("prepare claude mcp config: %w", err)
	}
	if configPath == "" {
		return nil, nil
	}
	return &claudeNativeMCP{ConfigPath: configPath, WorkspaceDir: workspaceDir}, nil
}

func buildClaudeCodePrompt(systemPrompt string, messages []Message) string {
	var b strings.Builder

	if systemPrompt != "" {
		b.WriteString("System:\n")
		b.WriteString(systemPrompt)
		b.WriteString("\n\n")
	}

	for _, msg := range messages {
		var role string
		switch strings.ToLower(msg.Role) {
		case RoleSystem:
			role = "System"
		case RoleUser:
			role = "User"
		case RoleAssistant:
			role = "Assistant"
		case RoleTool:
			role = "Tool"
		case "":
			role = "Message"
		default:
			role = strings.ToUpper(msg.Role)
		}
		b.WriteString(role)
		b.WriteString(":\n")
		b.WriteString(msg.Content)
		b.WriteString("\n\n")
	}

	return strings.TrimSpace(b.String())
}

type claudeCLIResponse struct {
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Errors           []string        `json:"errors"`
}

// buildClaudeArgs assembles the claude CLI args. A non-nil nat selects the
// native-MCP run (full toolset + --mcp-config, auto-approved, workspace-confined);
// nil keeps the text-only behavior (--tools "" --permission-mode dontAsk).
func buildClaudeArgs(model, prompt string, schema any, nat *claudeNativeMCP) ([]string, error) {
	args := []string{
		"--print",
		"--output-format",
		"json",
	}

	if nat != nil {
		// Native-MCP run: enable the full toolset plus the workspace's MCP
		// servers, auto-approve tool calls (headless), and confine writes to the
		// workspace folder. The CLI runs its own MCP loop and returns the final
		// text once tools have run.
		args = append(args,
			"--permission-mode", "bypassPermissions",
			"--mcp-config", nat.ConfigPath,
		)
		if nat.WorkspaceDir != "" {
			args = append(args, "--add-dir", nat.WorkspaceDir)
		}
	} else {
		// Text-only run (unchanged): no tools, no MCP.
		args = append(args, "--tools", "", "--permission-mode", "dontAsk")
	}

	if model != "" {
		args = append(args, "--model", model)
	}
	if schema != nil {
		payload, err := json.Marshal(schema)
		if err != nil {
			return nil, fmt.Errorf("claude schema marshal: %w", err)
		}
		args = append(args, "--json-schema", string(payload))
	}
	args = append(args, prompt)
	return args, nil
}

func (p *ClaudeCodeProvider) runClaudeExec(ctx context.Context, model, prompt string, schema any, nat *claudeNativeMCP) (string, error) {
	args, err := buildClaudeArgs(model, prompt, schema, nat)
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, p.cliPath, args...)
	if nat != nil && nat.WorkspaceDir != "" {
		cmd.Dir = nat.WorkspaceDir
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if parsedMsg := parseClaudeCLIError(stdout.Bytes()); parsedMsg != "" {
			msg = parsedMsg
		}
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = "claude CLI failed"
		}
		return "", fmt.Errorf("%s: %w", msg, err)
	}

	out := stdout.Bytes()
	var resp claudeCLIResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		content := strings.TrimSpace(string(out))
		if content == "" {
			return "", fmt.Errorf("claude CLI returned empty response")
		}
		return content, nil
	}

	if resp.IsError {
		msg := strings.TrimSpace(resp.Result)
		if msg == "" && len(resp.Errors) > 0 {
			msg = strings.Join(resp.Errors, "; ")
		}
		if msg == "" {
			msg = "claude CLI error"
		}
		return "", fmt.Errorf("%s", msg)
	}

	if schema != nil {
		if !structuredOutputEmpty(resp.StructuredOutput) {
			return strings.TrimSpace(string(resp.StructuredOutput)), nil
		}
		fallback := strings.TrimSpace(resp.Result)
		if fallback != "" {
			return fallback, nil
		}
		return "", fmt.Errorf("claude CLI returned empty structured_output")
	}

	content := strings.TrimSpace(resp.Result)
	if content == "" {
		return "", fmt.Errorf("claude CLI returned empty response")
	}
	return content, nil
}

func parseClaudeCLIError(output []byte) string {
	var resp claudeCLIResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		return ""
	}
	if !resp.IsError {
		return ""
	}
	msg := strings.TrimSpace(resp.Result)
	if msg == "" && len(resp.Errors) > 0 {
		msg = strings.Join(resp.Errors, "; ")
	}
	return msg
}

func structuredOutputEmpty(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return true
	}
	return trimmed == "null" || trimmed == `""`
}
