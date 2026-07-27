package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The classification table. Each row is a failure Ori has actually seen or will
// see, and the assertion that matters most is `wantRetryable`: a deterministic
// failure retried three times costs the user three round-trips and tells them
// nothing new.

// httpErr is an error carrying a response, matching the shape both SDKs use.
type httpErr struct {
	status  int
	headers http.Header
	msg     string
}

func (e *httpErr) Error() string { return e.msg }
func (e *httpErr) Response() *http.Response {
	h := e.headers
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{StatusCode: e.status, Header: h}
}

func TestClassifyProviderError(t *testing.T) {
	cases := []struct {
		name          string
		err           error
		status        int
		code          string
		wantCategory  ErrorCategory
		wantRetryable bool
		wantAction    UserAction
	}{
		// --- The quota / rate-limit split, which share a status code ---------
		{
			name:         "429 with insufficient_quota is exhausted, not throttled",
			err:          errors.New("You exceeded your current quota, please check your plan and billing details"),
			status:       http.StatusTooManyRequests,
			code:         "insufficient_quota",
			wantCategory: CategoryQuotaExhausted,
			wantAction:   ActionCheckBilling,
		},
		{
			name:          "429 with no quota marker is a temporary rate limit",
			err:           errors.New("Rate limit reached for gpt-4 in organization org-x"),
			status:        http.StatusTooManyRequests,
			code:          "rate_limit_exceeded",
			wantCategory:  CategoryRateLimited,
			wantRetryable: true,
			wantAction:    ActionWaitAndRetry,
		},
		{
			name:          "anthropic rate_limit_error is retryable",
			err:           errors.New("rate limited"),
			status:        http.StatusTooManyRequests,
			code:          "rate_limit_error",
			wantCategory:  CategoryRateLimited,
			wantRetryable: true,
		},
		{
			name:         "quota detected from message when no code is published",
			err:          errors.New("Your credit balance is too low to access the API"),
			status:       http.StatusTooManyRequests,
			wantCategory: CategoryQuotaExhausted,
			wantAction:   ActionCheckBilling,
		},
		{
			name:         "402 payment required is quota",
			err:          errors.New("payment required"),
			status:       402,
			wantCategory: CategoryQuotaExhausted,
			wantAction:   ActionCheckBilling,
		},
		{
			name:         "403 on a deactivated account is quota, not permissions",
			err:          errors.New("account deactivated"),
			status:       http.StatusForbidden,
			code:         "account_deactivated",
			wantCategory: CategoryQuotaExhausted,
		},

		// --- Deterministic configuration failures ---------------------------
		{
			name:         "401 is an authentication problem",
			err:          errors.New("Incorrect API key provided"),
			status:       http.StatusUnauthorized,
			wantCategory: CategoryAuthentication,
			wantAction:   ActionConfigureProvider,
		},
		{
			name:         "403 is permission denied",
			err:          errors.New("forbidden"),
			status:       http.StatusForbidden,
			wantCategory: CategoryPermissionDenied,
		},
		{
			name:         "404 means the model is unavailable",
			err:          errors.New("The model `gpt-9` does not exist"),
			status:       http.StatusNotFound,
			wantCategory: CategoryInvalidModel,
			wantAction:   ActionChooseModel,
		},
		{
			name:         "context length overflow asks for less input",
			err:          errors.New("This model's maximum context length is 8192 tokens"),
			status:       http.StatusBadRequest,
			code:         "context_length_exceeded",
			wantCategory: CategoryContextLength,
			wantAction:   ActionReduceInput,
		},
		{
			name:         "content policy rejection",
			err:          errors.New("Your request was rejected"),
			status:       http.StatusBadRequest,
			code:         "content_policy_violation",
			wantCategory: CategoryPolicyRejection,
			wantAction:   ActionEditTask,
		},
		{
			name:         "a plain 400 is an invalid request",
			err:          errors.New("Invalid schema for response_format"),
			status:       http.StatusBadRequest,
			wantCategory: CategoryInvalidRequest,
			wantAction:   ActionEditTask,
		},

		// --- The transient allowlist ----------------------------------------
		{
			name:          "408 request timeout",
			err:           errors.New("request timeout"),
			status:        http.StatusRequestTimeout,
			wantCategory:  CategoryTimeout,
			wantRetryable: true,
		},
		{
			name:          "500 internal server error",
			err:           errors.New("internal error"),
			status:        http.StatusInternalServerError,
			wantCategory:  CategoryProviderError,
			wantRetryable: true,
		},
		{
			name:          "502 bad gateway",
			err:           errors.New("bad gateway"),
			status:        http.StatusBadGateway,
			wantCategory:  CategoryProviderError,
			wantRetryable: true,
		},
		{
			name:          "503 service unavailable",
			err:           errors.New("service unavailable"),
			status:        http.StatusServiceUnavailable,
			wantCategory:  CategoryProviderError,
			wantRetryable: true,
		},
		{
			name:          "504 gateway timeout",
			err:           errors.New("gateway timeout"),
			status:        http.StatusGatewayTimeout,
			wantCategory:  CategoryProviderError,
			wantRetryable: true,
		},
		{
			name:          "529 provider overloaded",
			err:           errors.New("overloaded"),
			status:        529,
			wantCategory:  CategoryProviderOverloaded,
			wantRetryable: true,
			wantAction:    ActionWaitAndRetry,
		},

		// --- Anything unclassified stays deterministic ----------------------
		{
			name:         "an unlisted 5xx is not on the allowlist",
			err:          errors.New("i'm a teapot but server-side"),
			status:       599,
			wantCategory: CategoryUnknown,
		},
		{
			name:         "an error with no status at all",
			err:          errors.New("something went sideways"),
			wantCategory: CategoryUnknown,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyProviderError("openai", tc.err, tc.status, tc.code)
			if got.Category != tc.wantCategory {
				t.Fatalf("category = %q, want %q", got.Category, tc.wantCategory)
			}
			if got.Retryable != tc.wantRetryable {
				t.Fatalf("retryable = %v, want %v for %q", got.Retryable, tc.wantRetryable, got.Category)
			}
			if tc.wantAction != "" && got.Action != tc.wantAction {
				t.Fatalf("action = %q, want %q", got.Action, tc.wantAction)
			}
			if strings.TrimSpace(got.Message) == "" {
				t.Fatal("every classified failure needs a user-facing message")
			}
			if got.Provider != "openai" {
				t.Fatalf("provider = %q, want openai", got.Provider)
			}
		})
	}
}

// Cancellation is never a provider fault and must never be retried.
func TestClassifyProviderError_Cancellation(t *testing.T) {
	got := ClassifyProviderError("openai", context.Canceled, 0, "")
	if got.Category != CategoryCanceled || got.Retryable {
		t.Fatalf("cancel = %+v, want canceled and non-retryable", got)
	}
	if got.Action != ActionNone {
		t.Fatalf("action = %q, want none — there is nothing for the user to fix", got.Action)
	}

	deadline := ClassifyProviderError("openai", context.DeadlineExceeded, 0, "")
	if deadline.Category != CategoryTimeout || !deadline.Retryable {
		t.Fatalf("deadline = %+v, want a retryable timeout", deadline)
	}
}

// Pre-response transport failures are the classic safe retry: nothing was
// produced, so repeating the request cannot duplicate work.
func TestClassifyProviderError_Transport(t *testing.T) {
	cases := []struct {
		name          string
		err           error
		wantCategory  ErrorCategory
		wantRetryable bool
	}{
		{"eof", io.EOF, CategoryNetwork, true},
		{"unexpected eof", io.ErrUnexpectedEOF, CategoryNetwork, true},
		{"connection reset", errors.New("read tcp 1.2.3.4:443: connection reset by peer"), CategoryNetwork, true},
		{"connection refused", errors.New("dial tcp: connection refused"), CategoryNetwork, true},
		{"tls handshake timeout", errors.New("net/http: TLS handshake timeout"), CategoryNetwork, true},
		{"temporary dns failure", &net.DNSError{Err: "server misbehaving", IsTemporary: true}, CategoryNetwork, true},
		// A permanent NXDOMAIN is a misconfigured endpoint: identical every time.
		{"permanent dns failure", &net.DNSError{Err: "no such host", Name: "api.typo.com"}, CategoryInvalidRequest, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyProviderError("openai", tc.err, 0, "")
			if got.Category != tc.wantCategory {
				t.Fatalf("category = %q, want %q", got.Category, tc.wantCategory)
			}
			if got.Retryable != tc.wantRetryable {
				t.Fatalf("retryable = %v, want %v", got.Retryable, tc.wantRetryable)
			}
		})
	}
}

// A provider's Retry-After is honored, but bounded: a provider asking for ten
// minutes is telling us to stop, not to hold a task slot open.
func TestClassifyProviderError_RetryAfterIsBounded(t *testing.T) {
	withHeader := func(value string) error {
		return &httpErr{status: http.StatusTooManyRequests, msg: "rate limited",
			headers: http.Header{"Retry-After": []string{value}}}
	}

	got := ClassifyProviderError("openai", withHeader("5"), http.StatusTooManyRequests, "rate_limit_exceeded")
	if got.RetryAfter != 5*time.Second {
		t.Fatalf("retry after = %v, want 5s", got.RetryAfter)
	}

	got = ClassifyProviderError("openai", withHeader("600"), http.StatusTooManyRequests, "rate_limit_exceeded")
	if got.RetryAfter != maxRetryAfter {
		t.Fatalf("retry after = %v, want it bounded to %v", got.RetryAfter, maxRetryAfter)
	}

	// A deterministic failure must not carry a delay that implies waiting helps.
	quota := ClassifyProviderError("openai", withHeader("30"), http.StatusTooManyRequests, "insufficient_quota")
	if quota.RetryAfter != 0 {
		t.Fatalf("quota retry after = %v, want 0", quota.RetryAfter)
	}
}

// The status can be read from the error itself when the caller has none.
func TestClassifyProviderError_ReadsStatusFromError(t *testing.T) {
	got := ClassifyProviderError("openai", &httpErr{status: http.StatusServiceUnavailable, msg: "down"}, 0, "")
	if got.Category != CategoryProviderError || !got.Retryable {
		t.Fatalf("got %+v, want a retryable provider error", got)
	}
}

// An already-typed error is trusted, not re-derived — a provider that
// classified precisely must not be second-guessed by generic rules.
func TestClassifyProviderError_PassesThroughTypedErrors(t *testing.T) {
	original := NewProviderError("anthropic", CategoryQuotaExhausted, errors.New("boom"))
	wrapped := fmt.Errorf("task execution: %w", original)

	got := ClassifyProviderError("openai", wrapped, http.StatusServiceUnavailable, "")
	if got != original {
		t.Fatalf("got %+v, want the original typed error", got)
	}
	if got.Provider != "anthropic" {
		t.Fatalf("provider = %q, want the original anthropic", got.Provider)
	}
}

func TestProviderError_ErrorsIsAndAs(t *testing.T) {
	err := error(NewProviderError("openai", CategoryRateLimited, errors.New("slow down")))
	if !errors.Is(err, ErrProvider) {
		t.Fatal("every ProviderError must match ErrProvider")
	}
	typed, ok := AsProviderError(fmt.Errorf("wrapped: %w", err))
	if !ok || typed.Category != CategoryRateLimited {
		t.Fatalf("AsProviderError = %+v, %v", typed, ok)
	}
	if _, ok := AsProviderError(errors.New("plain")); ok {
		t.Fatal("a plain error must not classify as a provider error")
	}
	if _, ok := AsProviderError(nil); ok {
		t.Fatal("nil must not classify")
	}
}

// The error string is for server logs; it must carry no provider payload.
func TestProviderError_MessageCarriesNoPayload(t *testing.T) {
	secretish := errors.New(`{"request":{"messages":[{"role":"user","content":"my private note"}]}}`)
	got := NewProviderError("openai", CategoryQuotaExhausted, secretish)
	if strings.Contains(got.Message, "private note") {
		t.Fatalf("user message leaked the request payload: %s", got.Message)
	}
	// The wrapped cause stays available for server-side inspection.
	if !errors.Is(got, secretish) {
		t.Fatal("the cause must remain unwrappable for logs")
	}
}
