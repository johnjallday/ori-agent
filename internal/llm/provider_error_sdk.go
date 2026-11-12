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
		if message := anthropicWorkspaceProblem(apiErr); message != "" {
			typed := NewProviderError(provider, CategoryPermissionDenied, err).
				WithHTTP(apiErr.StatusCode, string(apiErr.Type()))
			typed.Message = message
			return typed
		}
		return ClassifyProviderError(provider, err, apiErr.StatusCode, string(apiErr.Type()))
	}
	return ClassifyProviderError(provider, err, 0, "")
}

// Ori's own sentences for the two workspace problems; neither repeats the
// provider's payload or the workspace id.
const (
	anthropicWorkspaceNotFoundMessage = "Anthropic did not find the workspace this request named, so the API key cannot use it. Check the Anthropic Workspace ID in Settings."
	anthropicWorkspaceMissingMessage  = "This Anthropic API key belongs to the whole organization, so each request must name a workspace. Set the Anthropic Workspace ID in Settings."
)

// anthropicWorkspaceProblem returns the sentence to show when Anthropic
// refused a request over its workspace rather than its model or content, and
// "" otherwise. Anthropic publishes these under the generic not_found_error and
// invalid_request_error types, so the message is the only signal:
//   - 404 "Workspace `<id>` not found."
//   - 400 "API key is not scoped to a workspace, must include anthropic-workspace-id"
func anthropicWorkspaceProblem(apiErr *anthropic.Error) string {
	text := strings.ToLower(apiErr.Error())
	switch {
	case apiErr.StatusCode == http.StatusNotFound &&
		strings.Contains(text, "workspace `") && strings.Contains(text, "not found"):
		return anthropicWorkspaceNotFoundMessage
	case apiErr.StatusCode == http.StatusBadRequest &&
		(strings.Contains(text, "not scoped to a workspace") || strings.Contains(text, "must include anthropic-workspace-id")):
		return anthropicWorkspaceMissingMessage
	}
	return ""
}
