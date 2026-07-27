package llm

import (
	"errors"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	openai "github.com/openai/openai-go/v3"
)

// SDK-specific extraction of the structured signal each provider publishes.
//
// Both SDKs expose an API error carrying the HTTP status and the provider's own
// machine code. Reading them here — rather than letting the generic classifier
// guess from message text — is what makes the `insufficient_quota` vs
// "slow down" distinction reliable (FR 46, 47, 50, 51).

// classifyOpenAIError maps an OpenAI SDK failure into the typed contract.
func classifyOpenAIError(provider string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		// Code is OpenAI's machine code ("insufficient_quota", "model_not_found",
		// …); Type is the coarser family it belongs to. Prefer the code.
		code := apiErr.Code
		if code == "" {
			code = apiErr.Type
		}
		return ClassifyProviderError(provider, err, apiErr.StatusCode, code)
	}
	return ClassifyProviderError(provider, err, 0, "")
}

// classifyAnthropicError maps an Anthropic SDK failure into the typed contract.
func classifyAnthropicError(provider string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		// Anthropic publishes the family as an error type ("rate_limit_error",
		// "overloaded_error", …) rather than a per-condition code.
		return ClassifyProviderError(provider, err, apiErr.StatusCode, string(apiErr.Type()))
	}
	return ClassifyProviderError(provider, err, 0, "")
}
