package llm

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Classification of real provider failures into the typed contract.
//
// Everything here reads STRUCTURED signals first — the HTTP status and the
// provider's own error code — because those are stable. Message text is
// consulted only as a last resort, and only for the distinctions providers
// genuinely encode nowhere else. That ordering matters: the previous behavior
// (retry anything that failed) was safe-looking and wrong, and a classifier
// built on prose would decay silently the first time a provider reworded a
// message.

// httpError is the shape both the OpenAI and Anthropic SDKs use for an API
// error: a status code plus the response. Matching structurally keeps this
// package free of SDK imports and covers any future SDK with the same shape.
type httpError interface {
	error
	Response() *http.Response
}

// statusCoder matches SDK errors exposing a StatusCode field via a method or,
// more commonly, a struct field read through the reflective helpers below.
type statusCoder interface{ StatusCode() int }

// ClassifyProviderError maps any error returned by a provider call into the
// typed contract. An error that is already typed passes through unchanged, so a
// provider that classified precisely is never second-guessed.
//
// status/code carry the structured signal when the caller extracted it
// (providers pass their SDK's status and error code); pass 0 and "" when there
// is none.
func ClassifyProviderError(provider string, err error, status int, code string) *ProviderError {
	if err == nil {
		return nil
	}
	if typed, ok := AsProviderError(err); ok {
		return typed
	}

	// Cancellation outranks everything: a canceled request tells us nothing
	// about the provider, and retrying it would defy the caller.
	if errors.Is(err, context.Canceled) {
		return NewProviderError(provider, CategoryCanceled, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewProviderError(provider, CategoryTimeout, err)
	}

	// Transport failures happen BEFORE any response, so nothing was produced and
	// a retry is genuinely safe (FR 53).
	if category, ok := classifyTransport(err); ok {
		return NewProviderError(provider, category, err)
	}

	if status == 0 {
		status = statusFrom(err)
	}
	if status > 0 {
		return classifyStatus(provider, err, status, code)
	}
	return NewProviderError(provider, CategoryUnknown, err)
}

// classifyTransport recognizes pre-response network failures. It deliberately
// does NOT treat every net.Error as retryable: a non-timeout, non-temporary
// network error (e.g. "no route to host" from a misconfigured base URL) is a
// configuration problem that repeats identically.
func classifyTransport(err error) (ErrorCategory, bool) {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return CategoryNetwork, true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return CategoryTimeout, true
		}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		// A permanent NXDOMAIN is a misconfigured endpoint, not a blip.
		if dnsErr.IsTemporary || dnsErr.IsTimeout {
			return CategoryNetwork, true
		}
		return CategoryInvalidRequest, true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return CategoryNetwork, true
	}
	// Connection reset / broken pipe / TLS handshake failures surface as opaque
	// errors from several layers; these substrings are the stable part.
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection reset by peer",
		"connection refused",
		"broken pipe",
		"unexpected eof",
		"tls: handshake failure",
		"tls handshake timeout",
		"server closed idle connection",
		"no such host",
	} {
		if strings.Contains(text, marker) {
			if marker == "no such host" {
				return CategoryInvalidRequest, true
			}
			return CategoryNetwork, true
		}
	}
	return "", false
}

// classifyStatus maps an HTTP status plus the provider's error code onto a
// category. The 429 split is the crux: a temporary per-minute limit and an
// exhausted account share a status code and mean opposite things (FR 50, 51).
func classifyStatus(provider string, err error, status int, code string) *ProviderError {
	code = strings.ToLower(strings.TrimSpace(code))
	text := strings.ToLower(err.Error())

	var category ErrorCategory
	switch {
	case status == http.StatusTooManyRequests:
		if isQuotaExhausted(code, text) {
			category = CategoryQuotaExhausted
		} else {
			category = CategoryRateLimited
		}
	case status == http.StatusRequestTimeout:
		category = CategoryTimeout
	case status == http.StatusUnauthorized:
		category = CategoryAuthentication
	case status == http.StatusForbidden:
		// Some providers report a suspended/unpaid account as 403.
		if isQuotaExhausted(code, text) {
			category = CategoryQuotaExhausted
		} else {
			category = CategoryPermissionDenied
		}
	case status == http.StatusNotFound:
		category = CategoryInvalidModel
	case status == http.StatusRequestEntityTooLarge:
		category = CategoryContextLength
	case status == 402: // Payment Required
		category = CategoryQuotaExhausted
	case status == 529: // Anthropic overloaded
		category = CategoryProviderOverloaded
	case status == http.StatusInternalServerError,
		status == http.StatusBadGateway,
		status == http.StatusServiceUnavailable,
		status == http.StatusGatewayTimeout:
		category = CategoryProviderError
	case status >= 400 && status < 500:
		category = classify4xxBody(code, text)
	case status >= 500:
		// An unlisted 5xx is still a provider-side fault, but not on the retry
		// allowlist: only the four statuses above are known-safe to repeat.
		category = CategoryUnknown
	default:
		category = CategoryUnknown
	}

	typed := NewProviderError(provider, category, err).WithHTTP(status, code)
	if after := retryAfterFrom(err); after > 0 {
		typed = typed.WithRetryAfter(after)
	}
	return typed
}

// quotaCodes are the provider error codes that mean "this account cannot make
// the request until billing changes" — as opposed to "slow down".
var quotaCodes = map[string]bool{
	"insufficient_quota":         true,
	"billing_hard_limit_reached": true,
	"quota_exceeded":             true,
	"account_deactivated":        true,
	"credit_balance_too_low":     true,
}

// isQuotaExhausted distinguishes an exhausted account from a temporary limit.
// The code is authoritative; the text check exists only for providers that send
// no machine code at all, and is kept to unambiguous phrases.
func isQuotaExhausted(code, text string) bool {
	if quotaCodes[code] {
		return true
	}
	if strings.Contains(code, "quota") || strings.Contains(code, "billing") {
		return true
	}
	for _, marker := range []string{
		"insufficient_quota",
		"exceeded your current quota",
		"billing hard limit",
		"credit balance is too low",
		"check your plan and billing",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// classify4xxBody refines an otherwise-generic 4xx using the provider's code.
func classify4xxBody(code, text string) ErrorCategory {
	switch {
	case strings.Contains(code, "context_length") || strings.Contains(text, "context length") ||
		strings.Contains(text, "maximum context") || strings.Contains(text, "too many tokens"):
		return CategoryContextLength
	case strings.Contains(code, "model_not_found") || strings.Contains(text, "does not exist") ||
		strings.Contains(text, "model not found"):
		return CategoryInvalidModel
	case strings.Contains(code, "content_policy") || strings.Contains(code, "content_filter") ||
		strings.Contains(text, "content policy"):
		return CategoryPolicyRejection
	case strings.Contains(code, "authentication") || strings.Contains(code, "invalid_api_key"):
		return CategoryAuthentication
	default:
		return CategoryInvalidRequest
	}
}

// statusFrom digs a status code out of an SDK error that exposes one.
func statusFrom(err error) int {
	var withResponse httpError
	if errors.As(err, &withResponse) {
		if resp := withResponse.Response(); resp != nil {
			return resp.StatusCode
		}
	}
	var withStatus statusCoder
	if errors.As(err, &withStatus) {
		return withStatus.StatusCode()
	}
	return 0
}

// retryAfterFrom reads a Retry-After header when the SDK exposed the response.
// The value is a hint, not an instruction: WithRetryAfter bounds it so a
// provider cannot hold a task slot open past its deadline (FR 54).
func retryAfterFrom(err error) time.Duration {
	var withResponse httpError
	if !errors.As(err, &withResponse) {
		return 0
	}
	resp := withResponse.Response()
	if resp == nil {
		return 0
	}
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}
