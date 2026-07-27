package llm

import (
	"errors"
	"fmt"
	"time"
)

// Provider-neutral typed execution errors.
//
// The task layer used to decide whether to retry by re-running whatever failed
// and hoping. That is fine for a dropped connection and actively harmful for a
// quota failure: `insufficient_quota` will not resolve in eight seconds, so
// three attempts spend three round-trips to reach the same conclusion, and the
// user is finally shown a generic "task failed" that names neither the cause
// nor the fix.
//
// Providers translate their own failures into ProviderError at their boundary —
// where the structured status code and error code are still available — instead
// of the task layer parsing user-facing strings, which breaks the moment a
// provider rewords a message.
//
// Nothing here retains a provider payload: only a safe category, a stable code,
// an HTTP status, and a bounded retry hint.

// ErrorCategory is the safe, stable classification of a provider failure. It is
// the value the UI switches on and the value that decides retryability.
type ErrorCategory string

const (
	// CategoryQuotaExhausted: billing or a hard/daily quota is exhausted.
	// Deterministic — retrying costs a round-trip and changes nothing (FR 50).
	CategoryQuotaExhausted ErrorCategory = "quota_exhausted"
	// CategoryRateLimited: a TEMPORARY per-minute limit. Retryable (FR 51).
	CategoryRateLimited ErrorCategory = "rate_limited"
	// CategoryAuthentication: missing/invalid/expired credentials.
	CategoryAuthentication ErrorCategory = "authentication"
	// CategoryPermissionDenied: authenticated but not allowed.
	CategoryPermissionDenied ErrorCategory = "permission_denied"
	// CategoryInvalidRequest: malformed request, bad parameters, or a schema the
	// provider rejected.
	CategoryInvalidRequest ErrorCategory = "invalid_request"
	// CategoryInvalidModel: the requested model does not exist or is unavailable.
	CategoryInvalidModel ErrorCategory = "invalid_model"
	// CategoryContextLength: the request exceeded the model's context window.
	CategoryContextLength ErrorCategory = "context_length"
	// CategoryPolicyRejection: the provider refused on content-policy grounds.
	CategoryPolicyRejection ErrorCategory = "policy_rejection"
	// CategoryProviderOverloaded: the provider is temporarily saturated (529).
	CategoryProviderOverloaded ErrorCategory = "provider_overloaded"
	// CategoryProviderError: a provider-side 5xx.
	CategoryProviderError ErrorCategory = "provider_error"
	// CategoryTimeout: the request timed out before a response (408 or client).
	CategoryTimeout ErrorCategory = "timeout"
	// CategoryNetwork: connection reset, EOF, DNS, or TLS failure BEFORE a
	// response was produced.
	CategoryNetwork ErrorCategory = "network"
	// CategoryCanceled: the caller canceled; never retry a cancellation.
	CategoryCanceled ErrorCategory = "canceled"
	// CategoryNotConfigured: no API key or provider configuration.
	CategoryNotConfigured ErrorCategory = "not_configured"
	// CategoryUnknown: unclassified. Treated as deterministic — an unknown error
	// is not evidence that retrying is safe (FR 49).
	CategoryUnknown ErrorCategory = "unknown"
)

// UserAction is the stable code for what the user must do. It never suggests an
// action that cannot fix the category it accompanies.
type UserAction string

const (
	// ActionCheckBilling: visit the provider's billing/quota settings.
	ActionCheckBilling UserAction = "check_provider_billing"
	// ActionWaitAndRetry: nothing to fix; the limit clears on its own.
	ActionWaitAndRetry UserAction = "wait_and_retry"
	// ActionConfigureProvider: set or correct the API key / provider config.
	ActionConfigureProvider UserAction = "configure_provider"
	// ActionChooseModel: pick a different model.
	ActionChooseModel UserAction = "choose_model"
	// ActionReduceInput: shorten the task input or context.
	ActionReduceInput UserAction = "reduce_input"
	// ActionEditTask: the request itself must change.
	ActionEditTask UserAction = "edit_task"
	// ActionRetry: an explicit user-triggered retry is reasonable.
	ActionRetry UserAction = "retry"
	// ActionNone: no user action applies.
	ActionNone UserAction = ""
)

// maxRetryAfter bounds how long a provider's Retry-After may delay us. A
// provider asking for ten minutes is telling us to give up for now, not to hold
// a task slot open — honoring it verbatim would silently exceed task deadlines.
const maxRetryAfter = 30 * time.Second

// ProviderError is a typed, provider-neutral execution failure.
type ProviderError struct {
	// Category is the safe classification the UI and retry policy switch on.
	Category ErrorCategory
	// Provider names the source ("openai", "anthropic", "gemini", "ollama", …).
	// Display metadata only — never used to pick a different provider (FR 52).
	Provider string
	// HTTPStatus is the response status when there was one, else 0.
	HTTPStatus int
	// ProviderCode is the provider's own machine code (e.g. "insufficient_quota")
	// when it published one. Safe: a code, never a payload.
	ProviderCode string
	// Retryable reports whether an AUTOMATIC retry may help. It is derived from
	// the category, not from optimism.
	Retryable bool
	// RetryAfter is the provider's requested delay, already bounded by
	// maxRetryAfter. Zero means "use the backoff schedule".
	RetryAfter time.Duration
	// Action is what the user must do, when anything.
	Action UserAction
	// Message is the safe, specific sentence to show. It contains no provider
	// payload, request body, or credential.
	Message string

	cause error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "<nil provider error>"
	}
	parts := string(e.Category)
	if e.ProviderCode != "" {
		parts += " (" + e.ProviderCode + ")"
	}
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Provider, parts, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Provider, parts)
}

// Unwrap exposes the underlying cause for server-side inspection only.
func (e *ProviderError) Unwrap() error { return e.cause }

// ErrProvider matches any typed provider failure via errors.Is.
var ErrProvider = errors.New("llm: provider error")

// Is makes errors.Is(err, ErrProvider) true for every ProviderError.
func (e *ProviderError) Is(target error) bool { return target == ErrProvider }

// AsProviderError unwraps a *ProviderError from err, if present.
func AsProviderError(err error) (*ProviderError, bool) {
	if err == nil {
		return nil, false
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

// retryableCategories is the ALLOWLIST of automatically-retryable categories
// (FR 53). Everything absent from it is deterministic by default, which is the
// safe direction: a wrongly-retried deterministic failure wastes the user's
// money and time, while a wrongly-deterministic transient failure costs one
// explicit retry click.
var retryableCategories = map[ErrorCategory]bool{
	CategoryNetwork:            true,
	CategoryTimeout:            true,
	CategoryRateLimited:        true,
	CategoryProviderError:      true,
	CategoryProviderOverloaded: true,
}

// IsRetryableCategory reports whether a category is on the automatic-retry
// allowlist.
func IsRetryableCategory(c ErrorCategory) bool { return retryableCategories[c] }

// defaultActions maps each category to the one action that can actually resolve
// it. Notably, quota maps to provider billing — never to a mail/connection
// repair, and never to "switch model", which Ori does not do automatically
// (FR 52).
var defaultActions = map[ErrorCategory]UserAction{
	CategoryQuotaExhausted:     ActionCheckBilling,
	CategoryRateLimited:        ActionWaitAndRetry,
	CategoryAuthentication:     ActionConfigureProvider,
	CategoryNotConfigured:      ActionConfigureProvider,
	CategoryPermissionDenied:   ActionConfigureProvider,
	CategoryInvalidModel:       ActionChooseModel,
	CategoryContextLength:      ActionReduceInput,
	CategoryInvalidRequest:     ActionEditTask,
	CategoryPolicyRejection:    ActionEditTask,
	CategoryProviderError:      ActionRetry,
	CategoryProviderOverloaded: ActionWaitAndRetry,
	CategoryTimeout:            ActionRetry,
	CategoryNetwork:            ActionRetry,
	CategoryCanceled:           ActionNone,
	CategoryUnknown:            ActionRetry,
}

// defaultMessages are the safe, specific sentences shown to the user. Each says
// what happened and what will change it — never "an error occurred".
var defaultMessages = map[ErrorCategory]string{
	CategoryQuotaExhausted:     "Your AI provider reports the account is out of quota or credit. Check the provider's billing settings; retrying won't help until that's resolved.",
	CategoryRateLimited:        "Your AI provider is rate-limiting requests right now. This usually clears on its own within a minute.",
	CategoryAuthentication:     "Your AI provider rejected the API key. Check the key in Settings.",
	CategoryNotConfigured:      "No API key is configured for this provider. Add one in Settings.",
	CategoryPermissionDenied:   "Your AI provider denied access to this model or endpoint for this account.",
	CategoryInvalidModel:       "The configured model isn't available to this account. Choose a different model.",
	CategoryContextLength:      "The request was longer than this model's context window. Shorten the task or its inputs.",
	CategoryInvalidRequest:     "Your AI provider rejected the request as invalid. The task or its output format needs to change.",
	CategoryPolicyRejection:    "Your AI provider declined this request on content-policy grounds.",
	CategoryProviderError:      "Your AI provider had a server-side error.",
	CategoryProviderOverloaded: "Your AI provider is overloaded right now.",
	CategoryTimeout:            "The request to your AI provider timed out.",
	CategoryNetwork:            "Ori couldn't reach your AI provider.",
	CategoryCanceled:           "The task was canceled.",
	CategoryUnknown:            "The task failed with an error Ori couldn't classify. Retrying may not help.",
}

// NewProviderError builds a typed error, deriving retryability, action, and
// message from the category so those three can never disagree.
func NewProviderError(provider string, category ErrorCategory, cause error) *ProviderError {
	return &ProviderError{
		Category:  category,
		Provider:  provider,
		Retryable: IsRetryableCategory(category),
		Action:    defaultActions[category],
		Message:   defaultMessages[category],
		cause:     cause,
	}
}

// WithHTTP records the response status and the provider's own error code.
func (e *ProviderError) WithHTTP(status int, providerCode string) *ProviderError {
	if e == nil {
		return nil
	}
	e.HTTPStatus = status
	e.ProviderCode = providerCode
	return e
}

// WithRetryAfter records a provider-requested delay, bounded by maxRetryAfter.
// A delay is only meaningful for a retryable category; recording one on a
// deterministic failure would imply a wait that cannot help.
func (e *ProviderError) WithRetryAfter(d time.Duration) *ProviderError {
	if e == nil || !e.Retryable || d <= 0 {
		return e
	}
	e.RetryAfter = min(d, maxRetryAfter)
	return e
}
