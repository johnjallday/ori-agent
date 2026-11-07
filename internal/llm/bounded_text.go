package llm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

// BoundedTextProvider adds the limits needed by cost-reserved, one-call text
// operations. Implementations honor ChatRequest.MaxTokens and do not repair,
// downgrade or retry a SingleAttempt Chat request. Isolation is still a separate
// capability check. CLI providers without an enforceable output cap do not
// implement this contract, even if they can disable their tools.
//
// The fingerprint binds the actual configured destination, without exposing
// endpoint paths, credentials or local filenames. Reading it does no I/O.
type BoundedTextProvider interface {
	Provider
	BoundedTextConfiguration() string
}

var ErrProviderConfigurationChanged = errors.New("reviewed provider configuration changed")

func checkTextConfiguration(req ChatRequest, actual string) error {
	if req.ProviderConfiguration != "" && req.ProviderConfiguration != actual {
		return ErrProviderConfigurationChanged
	}
	return nil
}

func textConfiguration(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func openAITextConfiguration() string {
	return textConfiguration("openai", os.Getenv("OPENAI_BASE_URL"), os.Getenv("OPENAI_ORG_ID"), os.Getenv("OPENAI_PROJECT_ID"))
}

func claudeTextConfiguration(workspaceID string) string {
	return textConfiguration("claude", os.Getenv("ANTHROPIC_BASE_URL"), workspaceID)
}

func (p *OpenAIProvider) BoundedTextConfiguration() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.textConfiguration
}

func (p *ClaudeProvider) BoundedTextConfiguration() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.textConfiguration
}

func (p *GeminiProvider) BoundedTextConfiguration() string {
	return textConfiguration(p.Name(), p.baseURL)
}

func (p *OllamaProvider) BoundedTextConfiguration() string {
	return textConfiguration(p.Name(), p.baseURL)
}

func (p *OpenAICompatibleLocalProvider) BoundedTextConfiguration() string {
	return textConfiguration(p.Name(), p.baseURL)
}
