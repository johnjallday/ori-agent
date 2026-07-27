package connections

import (
	"errors"
	"strings"
	"testing"
)

// FR 63-65: a self-hosted operator who configures the wrong value must learn
// before a browser flow starts, and the secret must never surface.
func TestValidateClient(t *testing.T) {
	const good = "1234567890-abcdefg.apps.googleusercontent.com"
	cases := []struct {
		name        string
		clientID    string
		clientSecre string
		wantProblem ClientProblem
	}{
		{"a real desktop client id", good, "GOCSPX-x", ClientProblemNone},
		{"a desktop client with no secret is fine (PKCE)", good, "", ClientProblemNone},
		{"nothing configured", "", "", ClientProblemMissingID},
		{"whitespace only", "   ", "", ClientProblemMissingID},
		{"the operator's own address", "operator@gmail.com", "GOCSPX-x", ClientProblemEmailAsID},
		{"a workspace address", "ops@example.co.uk", "", ClientProblemEmailAsID},
		{"a project name", "my-ori-project", "GOCSPX-x", ClientProblemMalformedID},
		{"the suffix alone", googleClientIDSuffix, "", ClientProblemMalformedID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := validateClient(tc.clientID, tc.clientSecre)
			if v.Problem != tc.wantProblem {
				t.Fatalf("problem = %q, want %q", v.Problem, tc.wantProblem)
			}
			if v.OK() != (tc.wantProblem == ClientProblemNone) {
				t.Fatalf("OK() = %v for problem %q", v.OK(), v.Problem)
			}
			if v.HasSecret != (strings.TrimSpace(tc.clientSecre) != "") {
				t.Fatalf("HasSecret = %v", v.HasSecret)
			}
			if tc.clientSecre != "" && strings.Contains(v.Message(), tc.clientSecre) {
				t.Fatalf("verdict message leaked the client secret: %s", v.Message())
			}
			if !v.OK() && v.Message() == "" {
				t.Fatal("every problem needs an actionable message")
			}
		})
	}
}

// A misconfigured client blocks authorization up front, and still satisfies the
// existing not-configured handling so no caller silently proceeds.
func TestBeginConnect_RejectsMisconfiguredClient(t *testing.T) {
	flow, _ := newTestFlow(t, "https://token.example", fakeVerifier{})
	flow.config.Verdict = ClientVerdict{Problem: ClientProblemEmailAsID}

	_, err := flow.BeginConnect(BeginConnectParams{RedirectURL: testRedirect})
	if err == nil {
		t.Fatal("expected a misconfigured client to block Connect")
	}
	var cfgErr *ClientConfigError
	if !errors.As(err, &cfgErr) || cfgErr.Verdict.Problem != ClientProblemEmailAsID {
		t.Fatalf("error = %v, want ClientConfigError(email_as_client_id)", err)
	}
	if !errors.Is(err, ErrOAuthNotConfigured) {
		t.Fatal("must still match ErrOAuthNotConfigured for existing handling")
	}
}

// The zero verdict means "not checked" so directly-constructed configs keep
// working — the validation is opt-in at the resolution boundary.
func TestUncheckedClientVerdictIsUsable(t *testing.T) {
	if !(ClientVerdict{}).OK() {
		t.Fatal("an unchecked verdict must be treated as usable")
	}
}

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
