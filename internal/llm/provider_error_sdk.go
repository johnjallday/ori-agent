package llm

import (
	"errors"
	"net/http"
	"strings"

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
		if isAnthropicWorkspaceNotFound(apiErr) {
			typed := NewProviderError(provider, CategoryPermissionDenied, err).
				WithHTTP(apiErr.StatusCode, string(apiErr.Type()))
			typed.Message = anthropicWorkspaceNotFoundMessage
			return typed
		}
		return ClassifyProviderError(provider, err, apiErr.StatusCode, string(apiErr.Type()))
	}
	return ClassifyProviderError(provider, err, 0, "")
}

// anthropicWorkspaceNotFoundMessage is shown when Anthropic does not know the
// workspace a request named. It is Ori's own sentence, not the provider's.
const anthropicWorkspaceNotFoundMessage = "Anthropic did not find the workspace this request named, so the API key cannot use it. Check the Anthropic Workspace ID in Settings."

// isAnthropicWorkspaceNotFound reports a 404 about the workspace rather than
// the model. Anthropic publishes both as not_found_error, so the message is the
// only signal: "Workspace `<id>` not found."
func isAnthropicWorkspaceNotFound(apiErr *anthropic.Error) bool {
	if apiErr.StatusCode != http.StatusNotFound {
		return false
	}
	text := strings.ToLower(apiErr.Error())
	return strings.Contains(text, "workspace `") && strings.Contains(text, "not found")
}
