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
	if id := strings.TrimSpace(os.Getenv("ORI_GOOGLE_CONNECTION_CLIENT_ID")); id != "" {
		return id, strings.TrimSpace(os.Getenv("ORI_GOOGLE_CONNECTION_CLIENT_SECRET")), ClientSourceEnv
	}
	if id := strings.TrimSpace(embeddedClientID); id != "" {
		return id, strings.TrimSpace(embeddedClientSecret), ClientSourceEmbedded
	}
	return "", "", ClientSourceNone
}
