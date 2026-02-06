package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/johnjallday/ori-agent/internal/modelinfo"
)

// CodexProvider implements the Provider interface using the Codex CLI.
type CodexProvider struct {
	cliPath string
}

// NewCodexProvider creates a new Codex provider backed by the Codex CLI.
func NewCodexProvider() (*CodexProvider, error) {
	cliPath, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("codex CLI not found: %w", err)
	}
	return &CodexProvider{
		cliPath: cliPath,
	}, nil
}

// Name returns the provider name.
func (p *CodexProvider) Name() string {
	return "codex"
}

// Type returns the provider type.
func (p *CodexProvider) Type() ProviderType {
	return ProviderTypeCloud
}

// Capabilities returns Codex CLI capabilities.
func (p *CodexProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsTools:          false,
		SupportsStreaming:      false,
		SupportsSystemPrompt:   true,
		SupportsTemperature:    false,
		RequiresAPIKey:         false,
		SupportsCustomEndpoint: false,
		MaxContextWindow:       128000,
		SupportedFormats:       []string{"text"},
	}
}

// ValidateConfig validates the Codex configuration.
func (p *CodexProvider) ValidateConfig(_ ProviderConfig) error {
	if p.cliPath == "" {
		return fmt.Errorf("codex CLI not available")
	}
	return nil
}

// DefaultModels returns available Codex models from the curated pricing data.
func (p *CodexProvider) DefaultModels() []string {
	return modelinfo.GetCodexModels()
}

// Chat sends a chat request via the Codex CLI.
func (p *CodexProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	prompt := buildCodexPrompt(req.SystemPrompt, req.Messages)
	content, err := p.runCodexExec(ctx, req.Model, prompt, nil)
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
func (p *CodexProvider) StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error) {
	return nil, fmt.Errorf("streaming not yet implemented for Codex provider")
}

// ChatWithStructuredOutput sends a chat request with a JSON schema using Codex CLI.
func (p *CodexProvider) ChatWithStructuredOutput(ctx context.Context, req StructuredOutputRequest) (*ChatResponse, error) {
	prompt := buildCodexPrompt(req.SystemPrompt, req.Messages)
	content, err := p.runCodexExec(ctx, req.Model, prompt, req.Schema)
	if err != nil {
		return nil, err
	}
	return &ChatResponse{
		Content:  content,
		Model:    req.Model,
		Provider: p.Name(),
	}, nil
}

func buildCodexPrompt(systemPrompt string, messages []Message) string {
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

func (p *CodexProvider) runCodexExec(ctx context.Context, model, prompt string, schema interface{}) (string, error) {
	var schemaPath string
	if schema != nil {
		tmpSchema, err := os.CreateTemp("", "codex-schema-*.json")
		if err != nil {
			return "", fmt.Errorf("codex schema temp file: %w", err)
		}
		defer os.Remove(tmpSchema.Name())
		schemaPath = tmpSchema.Name()

		payload, err := json.Marshal(schema)
		if err != nil {
			tmpSchema.Close()
			return "", fmt.Errorf("codex schema marshal: %w", err)
		}
		if _, err := tmpSchema.Write(payload); err != nil {
			tmpSchema.Close()
			return "", fmt.Errorf("codex schema write: %w", err)
		}
		if err := tmpSchema.Close(); err != nil {
			return "", fmt.Errorf("codex schema close: %w", err)
		}
	}

	tmpOut, err := os.CreateTemp("", "codex-output-*.txt")
	if err != nil {
		return "", fmt.Errorf("codex output temp file: %w", err)
	}
	tmpOutPath := tmpOut.Name()
	tmpOut.Close()
	defer os.Remove(tmpOutPath)

	args := []string{
		"exec",
		"-c",
		`model_reasoning_effort="high"`,
		"--color",
		"never",
		"--sandbox",
		"read-only",
		"--skip-git-repo-check",
		"--output-last-message",
		tmpOutPath,
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if schemaPath != "" {
		args = append(args, "--output-schema", schemaPath)
	}
	args = append(args, "-")

	cmd := exec.CommandContext(ctx, p.cliPath, args...)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = "codex exec failed"
		}
		return "", fmt.Errorf("%s: %w", msg, err)
	}

	outBytes, err := os.ReadFile(tmpOutPath)
	if err != nil {
		return "", fmt.Errorf("codex read output: %w", err)
	}
	content := strings.TrimSpace(string(outBytes))
	if content == "" {
		content = strings.TrimSpace(stdout.String())
	}
	if content == "" {
		return "", fmt.Errorf("codex returned empty response")
	}

	return content, nil
}
