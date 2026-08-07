package server

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

// A fresh install has no vault at all. That is not an error to report -- a
// credential can only live in a vault, so "no vault" simply means "nothing
// stored". Reporting it as a failure made a brand-new server tell the user
// "Could not read the saved GitHub connection" when the truthful answer was
// "GitHub is not connected yet".
func TestVaultMCPCredentialStore_NoVaultMeansNoCredential(t *testing.T) {
	store := newTestVaultStore(t)
	adapter := newVaultMCPCredentialStore(store)

	cred, ok, err := adapter.LoadCredential(context.Background(), "mcp:github")
	if err != nil {
		t.Fatalf("expected no error when no vault exists, got: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false when no vault exists, got credential: %+v", cred)
	}
}

func TestVaultMCPCredentialStore_RoundTripsStaticBearerCredential(t *testing.T) {
	store := newTestVaultStore(t)
	createTestVault(t, store, "default")
	adapter := newVaultMCPCredentialStore(store)
	ctx := context.Background()

	const token = "github_pat_roundtrip"
	if err := adapter.SaveCredential(ctx, mcp.RemoteCredential{
		AuthRef:     "mcp:github",
		ServerName:  "github",
		AccessToken: token,
		TokenType:   mcp.StaticBearerTokenType,
	}); err != nil {
		t.Fatalf("SaveCredential error: %v", err)
	}

	cred, ok, err := adapter.LoadCredential(ctx, "mcp:github")
	if err != nil || !ok {
		t.Fatalf("LoadCredential ok=%v err=%v", ok, err)
	}
	if cred.AccessToken != token {
		t.Fatalf("AccessToken = %q, want %q", cred.AccessToken, token)
	}
	if cred.TokenType != mcp.StaticBearerTokenType {
		t.Fatalf("TokenType = %q, want %q", cred.TokenType, mcp.StaticBearerTokenType)
	}
	// A static-bearer credential must not acquire OAuth client fields on the
	// way through the vault.
	if cred.ClientID != "" || cred.ClientSecret != "" || cred.RefreshToken != "" {
		t.Fatalf("static bearer credential picked up OAuth fields: %+v", cred)
	}

	if err := adapter.DeleteCredential(ctx, "mcp:github"); err != nil {
		t.Fatalf("DeleteCredential error: %v", err)
	}
	if _, ok, err := adapter.LoadCredential(ctx, "mcp:github"); err != nil || ok {
		t.Fatalf("expected the credential gone, got ok=%v err=%v", ok, err)
	}
}
