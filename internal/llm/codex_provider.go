package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

// DefaultModels returns visible Codex CLI models from local cache with curated fallback.
func (p *CodexProvider) DefaultModels() []string {
	cached := loadCodexCachedModels()
	if len(cached) > 0 {
		return prioritizeCodexModels(dedupeModels(cached))
	}
	return prioritizeCodexModels(dedupeModels(modelinfo.GetCodexModels()))
}

// Chat sends a chat request via the Codex CLI.
func (p *CodexProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	prompt := buildCodexPrompt(req.SystemPrompt, req.Messages)
	content, err := p.runCodexExec(ctx, req.Model, prompt, req.ReasoningEffort, nil)
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
	content, err := p.runCodexExec(ctx, req.Model, prompt, req.ReasoningEffort, req.Schema)
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

func (p *CodexProvider) runCodexExec(ctx context.Context, model, prompt, reasoningEffort string, schema interface{}) (string, error) {
	var schemaPath string
	if schema != nil {
		tmpSchema, err := os.CreateTemp("", "codex-schema-*.json")
		if err != nil {
			return "", fmt.Errorf("codex schema temp file: %w", err)
		}
		defer func() { _ = os.Remove(tmpSchema.Name()) }()
		schemaPath = tmpSchema.Name()

		payload, err := json.Marshal(schema)
		if err != nil {
			_ = tmpSchema.Close()
			return "", fmt.Errorf("codex schema marshal: %w", err)
		}
		if _, err := tmpSchema.Write(payload); err != nil {
			_ = tmpSchema.Close()
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
	_ = tmpOut.Close()
	defer func() { _ = os.Remove(tmpOutPath) }()

	args := []string{
		"exec",
		"-c",
		`model_reasoning_effort="` + normalizeCodexReasoningEffort(reasoningEffort) + `"`,
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

func normalizeCodexReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh":
		return "xhigh"
	default:
		return "medium"
	}
}

func loadCodexCachedModels() []string {
	cachePath := filepath.Join(codexHomeDir(), "models_cache.json")
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil
	}

	var payload struct {
		Models []struct {
			Slug       string `json:"slug"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}

	models := make([]string, 0, len(payload.Models))
	for _, model := range payload.Models {
		slug := strings.TrimSpace(model.Slug)
		if slug == "" {
			continue
		}
		// Trust the Codex CLI cache for which models are selectable. This includes
		// newer GPT-5 variants such as gpt-5.4 that do not include "codex" in
		// the slug but are still available in the local CLI.
		if !isCodexCacheModelVisible(model.Visibility) {
			continue
		}
		models = append(models, slug)
	}
	return models
}

func isCodexCacheModelVisible(visibility string) bool {
	switch strings.ToLower(strings.TrimSpace(visibility)) {
	case "hide", "hidden":
		return false
	default:
		return true
	}
}

func codexHomeDir() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return home
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return ".codex"
	}
	return filepath.Join(userHome, ".codex")
}

func dedupeModels(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	deduped := make([]string, 0, len(models))

	for _, model := range models {
		clean := strings.TrimSpace(model)
		if clean == "" {
			continue
		}
		key := strings.ToLower(clean)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, clean)
	}

	return deduped
}

func prioritizeCodexModels(models []string) []string {
	if len(models) == 0 {
		return models
	}

	prioritized := append([]string(nil), models...)
	sort.SliceStable(prioritized, func(i, j int) bool {
		iPreferred := isPreferredCodexModel(prioritized[i])
		jPreferred := isPreferredCodexModel(prioritized[j])
		if iPreferred != jPreferred {
			return iPreferred
		}
		return false
	})
	return prioritized
}

func isPreferredCodexModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	return normalized == "gpt-5.3-codex" ||
		strings.HasPrefix(normalized, "gpt-5.3-codex-") ||
		normalized == "gpt-5-3-codex" ||
		strings.HasPrefix(normalized, "gpt-5-3-codex-")
}
