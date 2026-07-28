package connections

import (
	"os"
	"strings"
)

// embeddedClientID / embeddedClientSecret are the official-build Google identity
// OAuth client, injected at build time via
//
//	-ldflags "-X github.com/johnjallday/ori-agent/internal/connections.embeddedClientID=..."
//
// They are empty in source and in unofficial builds, so a from-source build stays
// "unconfigured" until an operator sets the env vars. An official release bakes in
// a verified client here so Connect works with no per-operator setup, while
// self-hosters can still override it via the env vars (which take precedence).
var (
	embeddedClientID     string
	embeddedClientSecret string
)

// ClientSource reports where the resolved OAuth client credentials came from. It
// is a safe, secret-free signal the UI can use to tell "configured" from
// "unconfigured" (and env-override from baked-in) without ever seeing the
// id/secret.
type ClientSource string

const (
	ClientSourceEnv      ClientSource = "env"      // operator-provided via env (self-hosted)
	ClientSourceEmbedded ClientSource = "embedded" // baked into an official build
	ClientSourceNone     ClientSource = "none"     // not configured
)

// Configured reports whether a usable client was resolved.
func (s ClientSource) Configured() bool { return s == ClientSourceEnv || s == ClientSourceEmbedded }

// ResolveOAuthClient returns the connection identity OAuth client credentials and
// their source, with precedence **env → embedded → none**. The env vars let a
// self-hosted operator override the (possibly empty) embedded official-build
// client. It never logs or returns anything beyond the caller's own use of the
// secret; callers must not echo the returned secret.
func ResolveOAuthClient() (clientID, clientSecret string, source ClientSource) {
	id, secret, src, _ := ResolveOAuthClientChecked()
	return id, secret, src
}

// ResolveOAuthClientChecked is ResolveOAuthClient plus the validation verdict for
// the resolved client. An operator who pastes their Google *account address*
// into ORI_GOOGLE_CONNECTION_CLIENT_ID used to get a confusing failure only once
// the browser reached Google; the problem is detectable here, before any flow
// starts (FR 63-65).
//
// An invalid env client is reported as invalid rather than silently falling back
// to the embedded one — a silent fallback would connect an account the operator
// did not configure. The verdict never contains the secret.
func ResolveOAuthClientChecked() (clientID, clientSecret string, source ClientSource, verdict ClientVerdict) {
	if id := strings.TrimSpace(os.Getenv("ORI_GOOGLE_CONNECTION_CLIENT_ID")); id != "" {
		secret := strings.TrimSpace(os.Getenv("ORI_GOOGLE_CONNECTION_CLIENT_SECRET"))
		return id, secret, ClientSourceEnv, validateClient(id, secret)
	}
	if id := strings.TrimSpace(embeddedClientID); id != "" {
		secret := strings.TrimSpace(embeddedClientSecret)
		return id, secret, ClientSourceEmbedded, validateClient(id, secret)
	}
	return "", "", ClientSourceNone, ClientVerdict{Problem: ClientProblemMissingID}
}

// ClientProblem names what is wrong with a configured OAuth client. It is a
// stable, secret-free code suitable for logs and UI.
type ClientProblem string

const (
	// ClientProblemNone: the client looks like a usable Google OAuth client.
	ClientProblemNone ClientProblem = ""
	// ClientProblemMissingID: no client id is configured at all.
	ClientProblemMissingID ClientProblem = "missing_client_id"
	// ClientProblemEmailAsID: the client id is an email address — almost always
	// the operator's own Google account pasted into the wrong field.
	ClientProblemEmailAsID ClientProblem = "email_as_client_id"
	// ClientProblemMalformedID: the client id is not in Google's
	// <digits>-<hash>.apps.googleusercontent.com form.
	ClientProblemMalformedID ClientProblem = "malformed_client_id"
)

// googleClientIDSuffix is the suffix every Google OAuth client id carries.
const googleClientIDSuffix = ".apps.googleusercontent.com"

// ClientVerdict is the validation result for a configured OAuth client. It
// deliberately carries no secret — only whether one is present.
type ClientVerdict struct {
	// Problem is the specific defect, empty when the client is usable.
	Problem ClientProblem
	// HasSecret reports whether a client secret was supplied. A Desktop client
	// using PKCE may legitimately have none, so this is informational, not a
	// failure (see the self-hosted setup doc).
	HasSecret bool
}

// OK reports whether the client is usable for authorization.
func (v ClientVerdict) OK() bool { return v.Problem == ClientProblemNone }

// Message is the safe, actionable sentence for this verdict. It never echoes the
// configured values.
func (v ClientVerdict) Message() string {
	switch v.Problem {
	case ClientProblemMissingID:
		return "Set ORI_GOOGLE_CONNECTION_CLIENT_ID to your Google OAuth Desktop client ID (it ends in " + googleClientIDSuffix + ")."
	case ClientProblemEmailAsID:
		return "ORI_GOOGLE_CONNECTION_CLIENT_ID looks like an email address. It must be the OAuth client ID from Google Cloud Console, which ends in " + googleClientIDSuffix + "."
	case ClientProblemMalformedID:
		return "ORI_GOOGLE_CONNECTION_CLIENT_ID doesn't look like a Google OAuth client ID. It should end in " + googleClientIDSuffix + "."
	default:
		return ""
	}
}

// validateClient checks the shape of a configured client id. It is deliberately
// shape-only: whether Google accepts the client is Google's call, but the two
// mistakes below are certain to fail and are worth catching locally.
func validateClient(clientID, clientSecret string) ClientVerdict {
	v := ClientVerdict{HasSecret: strings.TrimSpace(clientSecret) != ""}
	id := strings.TrimSpace(clientID)
	switch {
	case id == "":
		v.Problem = ClientProblemMissingID
	case strings.Contains(id, "@"):
		v.Problem = ClientProblemEmailAsID
	case !strings.HasSuffix(id, googleClientIDSuffix) || len(id) <= len(googleClientIDSuffix):
		v.Problem = ClientProblemMalformedID
	}
	return v
}
