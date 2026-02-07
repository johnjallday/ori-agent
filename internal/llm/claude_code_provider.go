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
	cliPath string
}

// NewClaudeCodeProvider creates a new Claude Code provider backed by the Claude CLI.
func NewClaudeCodeProvider() (*ClaudeCodeProvider, error) {
	cliPath, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("claude CLI not found: %w", err)
	}
	return &ClaudeCodeProvider{
		cliPath: cliPath,
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
	content, err := p.runClaudeExec(ctx, req.Model, prompt, nil)
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
	content, err := p.runClaudeExec(ctx, req.Model, prompt, req.Schema)
	if err != nil {
		return nil, err
	}
	return &ChatResponse{
		Content:  content,
		Model:    req.Model,
		Provider: p.Name(),
	}, nil
}

func buildClaudeCodePrompt(systemPrompt string, messages []Message) string {
	var b strings.Builder

	if systemPrompt != "" {
		b.WriteString("System:\n")
		b.WriteString(systemPrompt)
		b.WriteString("\n\n")
	}

	for _, msg := range messages {
		role := "Message"
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

func (p *ClaudeCodeProvider) runClaudeExec(ctx context.Context, model, prompt string, schema interface{}) (string, error) {
	args := []string{
		"--print",
		"--output-format",
		"json",
		"--tools",
		"",
		"--permission-mode",
		"dontAsk",
	}

	if model != "" {
		args = append(args, "--model", model)
	}
	if schema != nil {
		payload, err := json.Marshal(schema)
		if err != nil {
			return "", fmt.Errorf("claude schema marshal: %w", err)
		}
		args = append(args, "--json-schema", string(payload))
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, p.cliPath, args...)
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
