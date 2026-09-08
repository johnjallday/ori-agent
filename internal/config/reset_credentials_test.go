package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/vault"
)

type resetCredentialStore struct {
	values    map[vault.SecretKey]string
	status    vault.StoreStatus
	getErr    map[vault.SecretKey]error
	deleteErr map[vault.SecretKey]error
	gets      []vault.SecretKey
	deletes   []vault.SecretKey
}

func (s *resetCredentialStore) Get(key vault.SecretKey) (string, error) {
	s.gets = append(s.gets, key)
	if err := s.getErr[key]; err != nil {
		return "", err
	}
	value, ok := s.values[key]
	if !ok {
		return "", vault.ErrSecretNotFound
	}
	return value, nil
}
func (s *resetCredentialStore) Set(key vault.SecretKey, value string) error {
	s.values[key] = value
	return nil
}
func (s *resetCredentialStore) Delete(key vault.SecretKey) error {
	s.deletes = append(s.deletes, key)
	if err := s.deleteErr[key]; err != nil {
		return err
	}
	delete(s.values, key)
	return nil
}
func (s *resetCredentialStore) Status() vault.StoreStatus { return s.status }

func availableResetCredentialStore() *resetCredentialStore {
	return &resetCredentialStore{
		values: make(map[vault.SecretKey]string), getErr: make(map[vault.SecretKey]error), deleteErr: make(map[vault.SecretKey]error),
		status: vault.StoreStatus{Backend: vault.BackendPassphraseFallback, Available: true, Writable: true},
	}
}

func TestMigrateInstallationSecretNamespaceCopiesKnownSlotsWithoutDeletingOrOverwriting(t *testing.T) {
	source := vault.NewMemorySecretStore()
	destination := vault.NewMemorySecretStore()
	for _, key := range []vault.SecretKey{
		vault.SecretKeyOpenAIAPIKey,
		vault.SecretKeyAnthropicAPIKey,
		vault.SecretKeyGeminiAPIKey,
		vault.SecretKeyBraveAPIKey,
		vault.SecretKeyVaultDEK,
	} {
		if err := source.Set(key, "source-"+string(key)); err != nil {
			t.Fatal(err)
		}
	}
	const unrelated vault.SecretKey = "unrelated_other_app"
	if err := source.Set(unrelated, "do-not-copy"); err != nil {
		t.Fatal(err)
	}
	if err := destination.Set(vault.SecretKeyOpenAIAPIKey, "existing-destination"); err != nil {
		t.Fatal(err)
	}

	if err := MigrateInstallationSecretNamespace(source, destination); err != nil {
		t.Fatal(err)
	}
	if got, err := destination.Get(vault.SecretKeyOpenAIAPIKey); err != nil || got != "existing-destination" {
		t.Fatalf("existing destination overwritten: value present=%t err=%v", got != "", err)
	}
	for _, key := range []vault.SecretKey{
		vault.SecretKeyAnthropicAPIKey,
		vault.SecretKeyGeminiAPIKey,
		vault.SecretKeyBraveAPIKey,
		vault.SecretKeyVaultDEK,
	} {
		if got, err := destination.Get(key); err != nil || got != "source-"+string(key) {
			t.Fatalf("slot %s not copied: value present=%t err=%v", key, got != "", err)
		}
		if got, err := source.Get(key); err != nil || got == "" {
			t.Fatalf("source slot %s was deleted: %v", key, err)
		}
	}
	if _, err := destination.Get(unrelated); !errors.Is(err, vault.ErrSecretNotFound) {
		t.Fatalf("unrelated slot copied: %v", err)
	}
}

func TestInspectResetCredentialsDistinguishesSecureLegacyAndAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"openai_api_key":"sk-legacy-openai-credential-fixture","utility":{"brave_api_key":"legacy-brave"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := availableResetCredentialStore()
	secrets.values[vault.SecretKeyAnthropicAPIKey] = "secure-anthropic"
	secrets.values[vault.SecretKeyVaultDEK] = "retained-dek"
	manager := NewManagerWithSecretStore(path, secrets)
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	inspection, err := manager.InspectResetCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if len(inspection.Slots) != 4 {
		t.Fatalf("slots = %d", len(inspection.Slots))
	}
	byKey := make(map[vault.SecretKey]ResetCredentialSlot)
	for _, slot := range inspection.Slots {
		byKey[slot.Key] = slot
	}
	if !byKey[vault.SecretKeyOpenAIAPIKey].LegacyPlaintext || byKey[vault.SecretKeyOpenAIAPIKey].SecurePresent == nil || *byKey[vault.SecretKeyOpenAIAPIKey].SecurePresent {
		t.Fatalf("openai slot = %+v", byKey[vault.SecretKeyOpenAIAPIKey])
	}
	if byKey[vault.SecretKeyAnthropicAPIKey].SecurePresent == nil || !*byKey[vault.SecretKeyAnthropicAPIKey].SecurePresent {
		t.Fatalf("anthropic slot = %+v", byKey[vault.SecretKeyAnthropicAPIKey])
	}
	if !byKey[vault.SecretKeyBraveAPIKey].LegacyPlaintext {
		t.Fatalf("brave slot = %+v", byKey[vault.SecretKeyBraveAPIKey])
	}
	if _, included := byKey[vault.SecretKeyVaultDEK]; included {
		t.Fatal("vault DEK entered provider/search inspection")
	}
}

func TestDeleteResetCredentialsUsesExactAllowlistAndPreservesExternalSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := []byte(`{"openai_api_key":"sk-legacy-openai-credential-fixture","anthropic_api_key":"legacy-anthropic","gemini_api_key":"legacy-gemini","utility":{"brave_api_key":"legacy-brave"},"workspace_root":"/retained/root"}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := availableResetCredentialStore()
	for _, key := range resetCredentialKeys {
		secrets.values[key] = "secure-" + string(key)
	}
	secrets.values[vault.SecretKeyVaultDEK] = "retained-dek"
	otherKey := vault.SecretKey("unrelated_token")
	secrets.values[otherKey] = "retained-other"
	manager := NewManagerWithSecretStore(path, secrets)
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "external-openai")
	result, err := manager.DeleteResetCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if !result.LegacyPlaintextCleared || !result.SettingsSaved || len(result.Credentials) != 4 {
		t.Fatalf("result = %+v", result)
	}
	if !slices.Equal(secrets.deletes, resetCredentialKeys[:]) {
		t.Fatalf("deleted keys = %v", secrets.deletes)
	}
	if got, err := secrets.Get(vault.SecretKeyVaultDEK); err != nil || got != "retained-dek" {
		t.Fatalf("vault DEK changed: %q %v", got, err)
	}
	if got, err := secrets.Get(otherKey); err != nil || got != "retained-other" {
		t.Fatalf("unrelated secret changed: %q %v", got, err)
	}
	if got := manager.GetAPIKey(); got != "external-openai" {
		t.Fatalf("external credential was not preserved: %q", got)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"sk-legacy-openai-credential-fixture", "legacy-anthropic", "legacy-gemini", "legacy-brave"} {
		if bytes.Contains(saved, []byte(secret)) {
			t.Fatalf("legacy credential remained in settings: %s", secret)
		}
	}
	if !bytes.Contains(saved, []byte("/retained/root")) {
		t.Fatal("unrelated settings were not preserved")
	}
	verification, err := manager.InspectResetCredentials()
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range verification.Slots {
		if slot.SecurePresent == nil || *slot.SecurePresent || slot.LegacyPlaintext {
			t.Fatalf("credential remained: %+v", slot)
		}
	}
}

func TestInspectResetCredentialsRefusesSharedRelativeNamespaceBeforeBackendAccess(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("settings.json", []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := availableResetCredentialStore()
	secrets.values[vault.SecretKeyOpenAIAPIKey] = "must-not-be-read"
	manager := NewManagerWithSecretStore("settings.json", secrets)
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.InspectResetCredentials(); !errors.Is(err, ErrResetCredentialNamespace) {
		t.Fatalf("relative namespace inspection = %v", err)
	}
	if len(secrets.gets) != 0 || len(secrets.deletes) != 0 {
		t.Fatal("ambiguous namespace accessed the credential backend")
	}
}

func TestDeleteResetCredentialsRefusesLockedStoreBeforeMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	before := []byte(`{"openai_api_key":"sk-legacy-openai-credential-fixture"}`)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := availableResetCredentialStore()
	secrets.status.Locked = true
	manager := NewManagerWithSecretStore(path, secrets)
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.DeleteResetCredentials(); !errors.Is(err, ErrResetCredentialStoreLocked) {
		t.Fatalf("locked delete = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || len(secrets.deletes) != 0 {
		t.Fatal("locked preflight mutated credentials or settings")
	}
}

func TestDeleteResetCredentialsReportsPartialSecureFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"gemini_api_key":"legacy-gemini"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := availableResetCredentialStore()
	for _, key := range resetCredentialKeys {
		secrets.values[key] = "secure-" + string(key)
	}
	secrets.deleteErr[vault.SecretKeyAnthropicAPIKey] = errors.New("synthetic delete refusal")
	manager := NewManagerWithSecretStore(path, secrets)
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	result, err := manager.DeleteResetCredentials()
	if err == nil {
		t.Fatal("partial deletion was reported as success")
	}
	if len(result.Credentials) != 4 || !result.SettingsSaved {
		t.Fatalf("partial result = %+v", result)
	}
	if _, err := secrets.Get(vault.SecretKeyAnthropicAPIKey); err != nil {
		t.Fatalf("failed key was not retained: %v", err)
	}
	if _, err := secrets.Get(vault.SecretKeyBraveAPIKey); !errors.Is(err, vault.ErrSecretNotFound) {
		t.Fatalf("later key was not attempted: %v", err)
	}
}
