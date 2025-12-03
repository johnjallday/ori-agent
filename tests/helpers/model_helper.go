package helpers

import (
	"flag"
	"os"
)

// TestModel is a flag for specifying which LLM model to use in tests
var TestModel = flag.String("model", "", "LLM model to use for tests (default: auto-detect)")

// GetTestModel returns the model to use for testing
// Priority: --model flag > OLLAMA_MODEL env var > default (granite4)
func GetTestModel() string {
	// Check command line flag first
	if TestModel != nil && *TestModel != "" {
		return *TestModel
	}

	// Check environment variable
	if model := os.Getenv("OLLAMA_MODEL"); model != "" {
		return model
	}

	// Default to Ollama with granite4 (free local inference)
	// To use OpenAI instead, set OPENAI_MODEL env var or use --model flag
	return "granite4"
}
