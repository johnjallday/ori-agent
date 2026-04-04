package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls []fakeCall
	resp  map[string]fakeResponse
}

type fakeCall struct {
	stdin string
	name  string
	args  []string
}

type fakeResponse struct {
	output []byte
	err    error
}

func (r *fakeRunner) Run(stdin string, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, fakeCall{
		stdin: stdin,
		name:  name,
		args:  append([]string{}, args...),
	})
	key := name + " " + strings.Join(args, " ")
	if resp, ok := r.resp[key]; ok {
		return resp.output, resp.err
	}
	return nil, nil
}

func TestNewAutoSecretStore_SelectsDarwinBackend(t *testing.T) {
	available := true
	store := NewAutoSecretStore(AutoSecretStoreOptions{
		GOOS:            "darwin",
		DarwinAvailable: &available,
		Namespace:       "test-darwin",
		Runner:          &fakeRunner{},
	})

	status := store.Status()
	if status.Backend != BackendDarwinKeychain {
		t.Fatalf("expected darwin backend, got %s", status.Backend)
	}
	if !status.Available || !status.Writable {
		t.Fatalf("expected available writable darwin backend, got %+v", status)
	}
}

func TestNewAutoSecretStore_SelectsLinuxBackend(t *testing.T) {
	available := true
	store := NewAutoSecretStore(AutoSecretStoreOptions{
		GOOS:           "linux",
		LinuxAvailable: &available,
		Namespace:      "test-linux",
		Runner:         &fakeRunner{},
	})

	status := store.Status()
	if status.Backend != BackendLinuxSecretService {
		t.Fatalf("expected linux backend, got %s", status.Backend)
	}
	if !status.Available || !status.Writable {
		t.Fatalf("expected available writable linux backend, got %+v", status)
	}
}

func TestNewAutoSecretStore_UsesPassphraseFallback(t *testing.T) {
	store := NewAutoSecretStore(AutoSecretStoreOptions{
		GOOS:               "linux",
		LinuxAvailable:     boolPtr(false),
		Namespace:          "test-fallback",
		FallbackPassphrase: "test-passphrase",
		FallbackPath:       filepath.Join(t.TempDir(), "vault_secrets.json"),
	})

	status := store.Status()
	if status.Backend != BackendPassphraseFallback {
		t.Fatalf("expected passphrase fallback, got %s", status.Backend)
	}
	if !status.Available || status.Locked {
		t.Fatalf("expected unlocked passphrase fallback, got %+v", status)
	}
}

func TestNewAutoSecretStore_UnavailableWithoutFallback(t *testing.T) {
	store := NewAutoSecretStore(AutoSecretStoreOptions{
		GOOS:           "linux",
		LinuxAvailable: boolPtr(false),
	})

	status := store.Status()
	if status.Backend != BackendUnavailable {
		t.Fatalf("expected unavailable backend, got %s", status.Backend)
	}
	if status.Available || !status.Locked {
		t.Fatalf("expected locked unavailable backend, got %+v", status)
	}
	if _, err := store.Get(SecretKeyOpenAIAPIKey); !errors.Is(err, ErrSecretStoreUnavailable) {
		t.Fatalf("expected ErrSecretStoreUnavailable, got %v", err)
	}
}

func TestNewAutoSecretStore_DoesNotSilentlyCreatePlaintextFallback(t *testing.T) {
	fallbackPath := filepath.Join(t.TempDir(), "vault_secrets.json")
	store := NewAutoSecretStore(AutoSecretStoreOptions{
		GOOS:           "linux",
		LinuxAvailable: boolPtr(false),
		FallbackPath:   fallbackPath,
	})

	if err := store.Set(SecretKeyOpenAIAPIKey, "sk-test"); !errors.Is(err, ErrSecretStoreUnavailable) {
		t.Fatalf("expected ErrSecretStoreUnavailable, got %v", err)
	}

	if _, err := os.Stat(fallbackPath); !os.IsNotExist(err) {
		t.Fatalf("expected no fallback file to be created, stat err = %v", err)
	}
}

func TestDarwinKeychainStoreUsesExpectedCommands(t *testing.T) {
	runner := &fakeRunner{
		resp: map[string]fakeResponse{
			"security find-generic-password -s ori-agent -a default:openai_api_key -w": {
				output: []byte("sk-test\n"),
			},
		},
	}
	store := newDarwinKeychainStore(runner, "default")

	value, err := store.Get(SecretKeyOpenAIAPIKey)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "sk-test" {
		t.Fatalf("expected trimmed key, got %q", value)
	}
	if err := store.Set(SecretKeyOpenAIAPIKey, "sk-next"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := store.Delete(SecretKeyOpenAIAPIKey); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 runner calls, got %d", len(runner.calls))
	}
	if runner.calls[1].name != "security" || runner.calls[2].name != "security" {
		t.Fatalf("unexpected command sequence: %+v", runner.calls)
	}
}

func TestLinuxSecretServiceStoreUsesExpectedCommands(t *testing.T) {
	runner := &fakeRunner{
		resp: map[string]fakeResponse{
			"secret-tool lookup service ori-agent namespace default account brave_api_key": {
				output: []byte("brave-token\n"),
			},
		},
	}
	store := newLinuxSecretServiceStore(runner, "default")

	value, err := store.Get(SecretKeyBraveAPIKey)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "brave-token" {
		t.Fatalf("expected trimmed secret, got %q", value)
	}
	if err := store.Set(SecretKeyBraveAPIKey, "next-token"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if runner.calls[1].stdin != "next-token" {
		t.Fatalf("expected secret value on stdin, got %q", runner.calls[1].stdin)
	}
}

func TestPassphraseSecretStoreRoundTrip(t *testing.T) {
	storeAny, err := NewPassphraseSecretStore(filepath.Join(t.TempDir(), "vault_secrets.json"), "correct horse battery staple")
	if err != nil {
		t.Fatalf("NewPassphraseSecretStore() error = %v", err)
	}
	store := storeAny.(*passphraseSecretStore)

	if err := store.Set(SecretKeyOpenAIAPIKey, "sk-test-secret"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, err := store.Get(SecretKeyOpenAIAPIKey)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != "sk-test-secret" {
		t.Fatalf("expected round-trip secret, got %q", got)
	}

	if err := store.Delete(SecretKeyOpenAIAPIKey); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Get(SecretKeyOpenAIAPIKey); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound after delete, got %v", err)
	}
}

func TestPassphraseSecretStoreRejectsWrongPassphrase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault_secrets.json")

	storeAny, err := NewPassphraseSecretStore(path, "correct passphrase")
	if err != nil {
		t.Fatalf("NewPassphraseSecretStore() error = %v", err)
	}
	store := storeAny.(*passphraseSecretStore)
	if err := store.Set(SecretKeyGeminiAPIKey, "gem-secret"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	wrongAny, err := NewPassphraseSecretStore(path, "wrong passphrase")
	if err != nil {
		t.Fatalf("NewPassphraseSecretStore() wrong store error = %v", err)
	}
	if _, err := wrongAny.Get(SecretKeyGeminiAPIKey); err == nil {
		t.Fatal("expected decryption error with wrong passphrase")
	}
}

func TestMemorySecretStoreRoundTrip(t *testing.T) {
	store := NewMemorySecretStore()
	if err := store.Set(SecretKeyAnthropicAPIKey, "sk-ant-123"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	got, err := store.Get(SecretKeyAnthropicAPIKey)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != "sk-ant-123" {
		t.Fatalf("expected stored value, got %q", got)
	}
}

func boolPtr(v bool) *bool {
	return &v
}
