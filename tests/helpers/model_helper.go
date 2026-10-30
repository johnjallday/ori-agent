package helpers

import (
	"flag"
	"os"
	"strings"
)

// TestModel is a flag for specifying which LLM model to use in tests.
var TestModel = flag.String("model", "", "LLM model to use for tests (default: the test provider's cheapest tool-capable model)")

// TestProvider is a flag for specifying which LLM provider to use in tests.
var TestProvider = flag.String("provider", "", "LLM provider to use for tests (default: claude when ANTHROPIC_API_KEY is set, else openai)")

// Environment variables that steer the live test suites without a flag.
const (
	TestProviderEnv = "ORI_TEST_PROVIDER"
	TestModelEnv    = "ORI_TEST_MODEL"
)

// Per-provider defaults: the cheapest model of each vendor that can still
// call tools, so the suites exercise tool use without spending much.
const (
	DefaultClaudeTestModel = "claude-haiku-5-5"
	DefaultOpenAITestModel = "gpt-5-mini"
	DefaultOllamaTestModel = "granite4"
)

// GetTestProvider returns the provider name the live suites configure on the
// server and on the agents they create.
//
// Priority: --provider flag > ORI_TEST_PROVIDER > USE_OLLAMA=true (ollama) >
// claude when ANTHROPIC_API_KEY is set > openai.
func GetTestProvider() string {
	if TestProvider != nil && strings.TrimSpace(*TestProvider) != "" {
		return strings.TrimSpace(*TestProvider)
	}
	if provider := strings.TrimSpace(os.Getenv(TestProviderEnv)); provider != "" {
		return provider
	}
	if os.Getenv("USE_OLLAMA") == "true" {
		return "ollama"
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "claude"
	}
	return "openai"
}

// GetTestModel returns the model to use for testing. It always matches the
// provider GetTestProvider picks, so a Claude run never asks for an OpenAI
// model and vice versa.
//
// Priority: --model flag > ORI_TEST_MODEL > OLLAMA_MODEL (ollama only) >
// the provider's default.
func GetTestModel() string {
	if TestModel != nil && strings.TrimSpace(*TestModel) != "" {
		return strings.TrimSpace(*TestModel)
	}
	if model := strings.TrimSpace(os.Getenv(TestModelEnv)); model != "" {
		return model
	}
	switch GetTestProvider() {
	case "claude":
		return DefaultClaudeTestModel
	case "ollama":
		if model := strings.TrimSpace(os.Getenv("OLLAMA_MODEL")); model != "" {
			return model
		}
		return DefaultOllamaTestModel
	default:
		return DefaultOpenAITestModel
	}
}
