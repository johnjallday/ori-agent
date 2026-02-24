package helpers

import (
	"flag"
	"os"
)

// TestModel is a flag for specifying which LLM model to use in tests
var TestModel = flag.String("model", "", "LLM model to use for tests (default: auto-detect)")

// GetTestModel returns the model to use for testing
// Priority:
//  1. --model flag
//  2. Provider-specific env var (OPENAI_MODEL / ANTHROPIC_MODEL / OLLAMA_MODEL)
//  3. Provider-specific default
func GetTestModel() string {
	// Check command line flag first
	if TestModel != nil && *TestModel != "" {
		return *TestModel
	}

	// Prefer Ollama when explicitly configured.
	useOllama := os.Getenv("USE_OLLAMA") == "true" || os.Getenv("OLLAMA_HOST") != ""
	if useOllama {
		if model := os.Getenv("OLLAMA_MODEL"); model != "" {
			return model
		}
		return "granite4"
	}

	// If using OpenAI, default to a valid OpenAI model unless overridden.
	if os.Getenv("OPENAI_API_KEY") != "" {
		if model := os.Getenv("OPENAI_MODEL"); model != "" {
			return model
		}
		return "gpt-4.1-mini"
	}

	// If using Anthropic, default to a valid Anthropic model unless overridden.
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		if model := os.Getenv("ANTHROPIC_MODEL"); model != "" {
			return model
		}
		return "claude-3-5-haiku-20241022"
	}

	// Fallback for callers that only set a model env var.
	if model := os.Getenv("OPENAI_MODEL"); model != "" {
		return model
	}
	if model := os.Getenv("ANTHROPIC_MODEL"); model != "" {
		return model
	}
	if model := os.Getenv("OLLAMA_MODEL"); model != "" {
		return model
	}

	// Final fallback: keep tests local-first when no provider is configured.
	return "granite4"
}
