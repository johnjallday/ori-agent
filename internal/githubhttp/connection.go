// Package githubhttp owns Ori's single, global GitHub connection: the
// personal access token behind it, the live checks that say whether it still
// works, and the HTTP surface that connects and disconnects it.
//
// The connection is deliberately global rather than per-workspace (matching
// how email accounts are connected once in Settings): a user who maintains
// several repos connects GitHub once and points each GitHub Ops workspace at
// a different repo. The repo binding is the only per-workspace part.
package githubhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

const (
	// apiBaseURL is the REST API used for connection tests and repo
	// listing. It is a fixed constant, never user-supplied, so calls to it
	// need no SSRF validation.
	apiBaseURL = "https://api.github.com"

	connectionTestTimeout = 15 * time.Second
)

// MCPServerConfig is the registry entry for GitHub's hosted MCP server. The
// definition lives in internal/mcp (which must name it among its default
// servers and cannot import this package); this alias keeps the connection
// code reading in terms of its own domain.
func MCPServerConfig() mcp.ServerConfig { return mcp.GitHubServerConfig() }

// Error categories reported by TestConnection. They are stable tokens the
// setup wizard maps to repair copy, chosen to line up with the
// setupwizard.StepReadiness ErrorCategory convention.
const (
	// ErrorCategoryNotConnected means no token is stored at all.
	ErrorCategoryNotConnected = "not_connected"
	// ErrorCategoryVaultLocked means a connection exists but its vault is
	// locked, so the token cannot be read until the user unlocks it. Kept
	// separate from not_connected because the remedy is different: unlock,
	// not reconnect. Telling a user to reconnect here would have them
	// generate a replacement token they do not need.
	ErrorCategoryVaultLocked = "vault_locked"
	// ErrorCategoryInvalidToken means GitHub rejected the token outright
	// (revoked, expired, or mistyped).
	ErrorCategoryInvalidToken = "invalid_token"
	// ErrorCategoryInsufficientScope means the token authenticated but is
	// not permitted to do what was asked.
	ErrorCategoryInsufficientScope = "insufficient_scope"
	// ErrorCategoryRateLimited means GitHub is throttling this token.
	ErrorCategoryRateLimited = "rate_limited"
	// ErrorCategoryUnavailable covers network failures and GitHub-side
	// errors -- conditions that are nobody's misconfiguration.
	ErrorCategoryUnavailable = "unavailable"
)

// ErrNotConnected is returned when an operation needs a stored token and
// none exists.
var ErrNotConnected = errors.New("github: no connection is configured")

// ConnectionError is a connection failure classified into a stable category
// with plain-language text.
//
// Its message is deliberately safe to surface to the user and to put in a
// StepReadiness summary: it never embeds the token, and never carries a raw
// GitHub API error body (which can contain account details and reads as
// noise). The HTTP status is kept for logging and tests.
type ConnectionError struct {
	Category string
	Message  string
	Status   int
}

func (e *ConnectionError) Error() string { return e.Message }

// Identity is what a successful connection test learned about the token. It
// holds no secret and is safe to return over HTTP.
type Identity struct {
	// Login is the authenticated account's GitHub username.
	Login string `json:"login"`
	// Scopes lists the OAuth scopes GitHub reports for a classic token.
	// Fine-grained tokens report none, so an empty slice is not an error
	// and must not be treated as "no permissions".
	Scopes []string `json:"scopes,omitempty"`
	// TokenType distinguishes a fine-grained token from a classic one, so
	// the UI can explain scope differences honestly.
	TokenType string `json:"token_type,omitempty"`
}

// Status is the non-secret view of the global connection.
type Status struct {
	Connected bool      `json:"connected"`
	Login     string    `json:"login,omitempty"`
	Scopes    []string  `json:"scopes,omitempty"`
	TokenType string    `json:"token_type,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	// Error is the plain-language reason the connection is not usable,
	// when a live check failed. Empty when Connected is true.
	Error string `json:"error,omitempty"`
	// ErrorCategory is the stable token behind Error.
	ErrorCategory string `json:"error_category,omitempty"`
}

// Connection is the global GitHub connection.
//
// It deliberately persists exactly one thing: the token, in the vault, via
// the MCP credential store. Everything else a caller might want to
// know -- the login, whether it still works, which workspaces depend on
// it -- is derived on demand from that token and from the workspaces' own
// repo bindings.
//
// The alternative (a separate connection record caching login/validity
// alongside the token) was rejected: it introduces a second source of truth
// that drifts the moment a token is revoked outside Ori, which is exactly
// the failure the PRD calls out -- linked workspaces still claiming to be
// ready after a disconnect. With nothing cached, "connected" cannot outlive
// the credential it describes.
type Connection struct {
	httpClient *http.Client
}

// NewConnection builds the global connection accessor. A nil client falls
// back to a bounded default; callers inject one in tests.
func NewConnection(client *http.Client) *Connection {
	if client == nil {
		client = &http.Client{Timeout: connectionTestTimeout}
	}
	return &Connection{httpClient: client}
}

// Connect validates token against GitHub and, only if it works, stores it.
//
// Validating first is the point: a token that cannot even identify itself is
// never written to the vault, so a failed connect attempt leaves a previously
// working connection untouched rather than replacing it with a broken one.
func (c *Connection) Connect(ctx context.Context, token string) (Identity, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return Identity{}, &ConnectionError{
			Category: ErrorCategoryInvalidToken,
			Message:  "Enter a GitHub personal access token to connect.",
		}
	}

	identity, err := c.identify(ctx, trimmed)
	if err != nil {
		return Identity{}, err
	}

	if err := mcp.SaveStaticBearerToken(ctx, MCPServerConfig(), trimmed); err != nil {
		// Deliberately not wrapped: a storage error's text comes from the
		// vault layer and is not guaranteed to be token-free.
		return Identity{}, &ConnectionError{
			Category: ErrorCategoryUnavailable,
			Message:  "Could not save the GitHub connection securely. Check that a vault is set up, then try again.",
		}
	}
	return identity, nil
}

// Disconnect removes the stored token. Disconnecting when nothing is
// connected is not an error.
//
// Because no connection state is cached anywhere else, this single delete is
// enough to make every dependent workspace evaluate as not-ready on its next
// check -- there is no second record left behind to claim otherwise.
func (c *Connection) Disconnect(ctx context.Context) error {
	return mcp.DeleteStaticBearerToken(ctx, MCPServerConfig())
}

// IsConnected reports whether a token is stored, without calling GitHub. Use
// it for cheap checks; use Status for "does it actually still work".
func (c *Connection) IsConnected(ctx context.Context) (bool, error) {
	_, ok, err := mcp.LoadStaticBearerToken(ctx, MCPServerConfig())
	return ok, err
}

// TestConnection performs a live `GET /user` with the stored token and
// returns the authenticated identity, or a classified ConnectionError.
func (c *Connection) TestConnection(ctx context.Context) (Identity, error) {
	token, ok, err := mcp.LoadStaticBearerToken(ctx, MCPServerConfig())
	if err != nil {
		if errors.Is(err, mcp.ErrCredentialStoreLocked) {
			return Identity{}, &ConnectionError{
				Category: ErrorCategoryVaultLocked,
				Message:  "Your vault is locked. Unlock it to use the GitHub connection.",
			}
		}
		return Identity{}, &ConnectionError{
			Category: ErrorCategoryUnavailable,
			Message:  "Could not read the saved GitHub connection.",
		}
	}
	if !ok {
		return Identity{}, &ConnectionError{
			Category: ErrorCategoryNotConnected,
			Message:  "GitHub is not connected yet.",
		}
	}
	return c.identify(ctx, token)
}

// Status returns the non-secret connection view, running a live check so the
// answer reflects the token's current state rather than the state it had when
// it was saved.
func (c *Connection) Status(ctx context.Context) Status {
	identity, err := c.TestConnection(ctx)
	if err != nil {
		var connErr *ConnectionError
		if errors.As(err, &connErr) {
			return Status{
				Connected:     false,
				CheckedAt:     time.Now().UTC(),
				Error:         connErr.Message,
				ErrorCategory: connErr.Category,
			}
		}
		return Status{
			Connected:     false,
			CheckedAt:     time.Now().UTC(),
			Error:         "GitHub could not be reached.",
			ErrorCategory: ErrorCategoryUnavailable,
		}
	}
	return Status{
		Connected: true,
		Login:     identity.Login,
		Scopes:    identity.Scopes,
		TokenType: identity.TokenType,
		CheckedAt: time.Now().UTC(),
	}
}

// identify runs `GET /user` with an explicit token. It is the single place a
// token is attached to an outbound REST request.
func (c *Connection) identify(ctx context.Context, token string) (Identity, error) {
	resp, err := c.do(ctx, token, http.MethodGet, apiBaseURL+"/user")
	if err != nil {
		return Identity{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return Identity{}, classifyResponse(resp)
	}

	var body struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || strings.TrimSpace(body.Login) == "" {
		return Identity{}, &ConnectionError{
			Category: ErrorCategoryUnavailable,
			Message:  "GitHub returned a response Ori could not read. Try again in a moment.",
			Status:   resp.StatusCode,
		}
	}

	return Identity{
		Login:     body.Login,
		Scopes:    parseScopes(resp.Header.Get("X-OAuth-Scopes")),
		TokenType: tokenKind(token),
	}, nil
}

// do issues an authenticated request. Errors from the transport are replaced
// wholesale rather than wrapped, so no URL, header, or token fragment from
// the underlying error can reach the user.
func (c *Connection) do(ctx context.Context, token, method, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, &ConnectionError{
			Category: ErrorCategoryUnavailable,
			Message:  "Could not build a request to GitHub.",
		}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &ConnectionError{
			Category: ErrorCategoryUnavailable,
			Message:  "Could not reach GitHub. Check your internet connection and try again.",
		}
	}
	return resp, nil
}

// classifyResponse turns a non-2xx GitHub response into a stable category
// with plain-language repair copy. The response body is deliberately never
// read into the message.
func classifyResponse(resp *http.Response) *ConnectionError {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return &ConnectionError{
			Category: ErrorCategoryInvalidToken,
			Message:  "GitHub rejected this token. It may have been revoked, expired, or copied incompletely.",
			Status:   resp.StatusCode,
		}
	case http.StatusForbidden, http.StatusTooManyRequests:
		if isRateLimited(resp) {
			return &ConnectionError{
				Category: ErrorCategoryRateLimited,
				Message:  rateLimitMessage(resp),
				Status:   resp.StatusCode,
			}
		}
		return &ConnectionError{
			Category: ErrorCategoryInsufficientScope,
			Message:  "This token does not have permission for that. Re-create it with the permissions Ori lists, then reconnect.",
			Status:   resp.StatusCode,
		}
	case http.StatusNotFound:
		// On an authenticated request, GitHub returns 404 rather than 403
		// for resources a fine-grained token cannot see -- so this is a
		// permission signal, not a missing-resource one.
		return &ConnectionError{
			Category: ErrorCategoryInsufficientScope,
			Message:  "This token cannot see that resource. It may not have been granted access to it.",
			Status:   resp.StatusCode,
		}
	default:
		return &ConnectionError{
			Category: ErrorCategoryUnavailable,
			Message:  "GitHub is not responding correctly right now. Try again in a moment.",
			Status:   resp.StatusCode,
		}
	}
}

// isRateLimited distinguishes a throttled 403 from a permissions 403. GitHub
// signals exhaustion with a zeroed X-RateLimit-Remaining, and secondary
// limits with a Retry-After.
func isRateLimited(resp *http.Response) bool {
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if resp.Header.Get("Retry-After") != "" {
		return true
	}
	remaining := strings.TrimSpace(resp.Header.Get("X-RateLimit-Remaining"))
	return remaining == "0"
}

// rateLimitMessage adds the reset time when GitHub supplies one, since "try
// later" without a "when" is not actionable.
func rateLimitMessage(resp *http.Response) string {
	const base = "GitHub's rate limit for this token has been reached."
	resetRaw := strings.TrimSpace(resp.Header.Get("X-RateLimit-Reset"))
	if resetRaw == "" {
		return base + " Try again in a few minutes."
	}
	seconds, err := strconv.ParseInt(resetRaw, 10, 64)
	if err != nil {
		return base + " Try again in a few minutes."
	}
	wait := time.Until(time.Unix(seconds, 0))
	if wait <= 0 {
		return base + " It should be clear now — try again."
	}
	return fmt.Sprintf("%s It resets in about %d minute(s).", base, int(wait.Minutes())+1)
}

// parseScopes splits GitHub's comma-separated X-OAuth-Scopes header. A
// fine-grained token omits the header entirely, which yields nil.
func parseScopes(header string) []string {
	trimmed := strings.TrimSpace(header)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, ",")
	scopes := make([]string, 0, len(parts))
	for _, part := range parts {
		if scope := strings.TrimSpace(part); scope != "" {
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) == 0 {
		return nil
	}
	return scopes
}

// tokenKind reports the token's family from its documented prefix, so the UI
// can explain why a fine-grained token reports no scopes. It is presentation
// only -- nothing branches on it for authorization.
func tokenKind(token string) string {
	switch {
	case strings.HasPrefix(token, "github_pat_"):
		return "fine_grained"
	case strings.HasPrefix(token, "ghp_"):
		return "classic"
	default:
		return ""
	}
}
