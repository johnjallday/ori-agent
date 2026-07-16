package config

import (
	"path/filepath"
	"testing"
)

func TestEmailGoogleOAuthSettingsFirstThenEnv(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err := m.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Env fallback when nothing is configured.
	t.Setenv("ORI_EMAIL_GOOGLE_CLIENT_ID", "env-id")
	t.Setenv("ORI_EMAIL_GOOGLE_CLIENT_SECRET", "env-secret")
	if id, secret := m.GetEmailGoogleOAuth(); id != "env-id" || secret != "env-secret" {
		t.Fatalf("expected env fallback, got %q/%q", id, secret)
	}
	if !m.GetEmailGoogleOAuthConfigured() {
		t.Fatal("env-configured should report configured")
	}

	// In-app settings take precedence over env.
	if err := m.SetEmailGoogleOAuth("cfg-id", "cfg-secret"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if id, secret := m.GetEmailGoogleOAuth(); id != "cfg-id" || secret != "cfg-secret" {
		t.Fatalf("settings should win over env, got %q/%q", id, secret)
	}

	// Persisted across a reload.
	m2 := NewManager(m.filePath)
	if err := m2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if id, _ := m2.GetEmailGoogleOAuth(); id != "cfg-id" {
		t.Fatalf("credentials should persist, got %q", id)
	}
}

func TestEmailGoogleOAuthUnconfigured(t *testing.T) {
	t.Setenv("ORI_EMAIL_GOOGLE_CLIENT_ID", "")
	t.Setenv("ORI_EMAIL_GOOGLE_CLIENT_SECRET", "")
	m := NewManager(filepath.Join(t.TempDir(), "settings.json"))
	_ = m.Load()
	if m.GetEmailGoogleOAuthConfigured() {
		t.Fatal("should be unconfigured with no settings or env")
	}
}
