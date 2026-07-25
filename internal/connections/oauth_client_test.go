package connections

import "testing"

func TestResolveOAuthClient_Precedence(t *testing.T) {
	// Save/restore the package-level embedded client (normally ldflags-injected).
	origID, origSecret := embeddedClientID, embeddedClientSecret
	t.Cleanup(func() { embeddedClientID, embeddedClientSecret = origID, origSecret })

	// Unconfigured: neither env nor embedded.
	t.Setenv("ORI_GOOGLE_CONNECTION_CLIENT_ID", "")
	t.Setenv("ORI_GOOGLE_CONNECTION_CLIENT_SECRET", "")
	embeddedClientID, embeddedClientSecret = "", ""
	if id, sec, src := ResolveOAuthClient(); id != "" || sec != "" || src != ClientSourceNone || src.Configured() {
		t.Errorf("unconfigured: got id=%q sec=%q src=%q configured=%v", id, sec, src, src.Configured())
	}

	// Embedded (official build): no env, embedded present.
	embeddedClientID, embeddedClientSecret = "embedded-id", "embedded-secret"
	if id, sec, src := ResolveOAuthClient(); id != "embedded-id" || sec != "embedded-secret" || src != ClientSourceEmbedded {
		t.Errorf("embedded: got id=%q sec=%q src=%q", id, sec, src)
	}

	// Env overrides embedded (self-hosted override).
	t.Setenv("ORI_GOOGLE_CONNECTION_CLIENT_ID", "env-id")
	t.Setenv("ORI_GOOGLE_CONNECTION_CLIENT_SECRET", "env-secret")
	if id, sec, src := ResolveOAuthClient(); id != "env-id" || sec != "env-secret" || src != ClientSourceEnv {
		t.Errorf("env override: got id=%q sec=%q src=%q", id, sec, src)
	}

	if !ClientSourceEnv.Configured() || !ClientSourceEmbedded.Configured() || ClientSourceNone.Configured() {
		t.Error("ClientSource.Configured() classification wrong")
	}
}
