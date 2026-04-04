package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type SecretKey string

const (
	SecretKeyOpenAIAPIKey    SecretKey = "openai_api_key"
	SecretKeyAnthropicAPIKey SecretKey = "anthropic_api_key"
	SecretKeyGeminiAPIKey    SecretKey = "gemini_api_key"
	SecretKeyBraveAPIKey     SecretKey = "brave_api_key"
	SecretKeyVaultDEK        SecretKey = "vault_dek"
)

type BackendKind string

const (
	BackendVaultPassword      BackendKind = "vault_password"
	BackendDarwinKeychain     BackendKind = "darwin_keychain"
	BackendLinuxSecretService BackendKind = "linux_secret_service"
	BackendWindowsSecureStore BackendKind = "windows_secure_store"
	BackendPassphraseFallback BackendKind = "passphrase_fallback"
	BackendUnavailable        BackendKind = "unavailable"
)

var (
	ErrSecretNotFound         = errors.New("vault: secret not found")
	ErrSecretStoreLocked      = errors.New("vault: secret store locked")
	ErrSecretStoreUnavailable = errors.New("vault: secret store unavailable")
	ErrSecretStoreUnsupported = errors.New("vault: secret store unsupported")
)

type StoreStatus struct {
	Backend   BackendKind `json:"backend"`
	Available bool        `json:"available"`
	Writable  bool        `json:"writable"`
	Locked    bool        `json:"locked"`
	Message   string      `json:"message,omitempty"`
}

type SecretStore interface {
	Get(key SecretKey) (string, error)
	Set(key SecretKey, value string) error
	Delete(key SecretKey) error
	Status() StoreStatus
}

type commandRunner interface {
	Run(stdin string, name string, args ...string) ([]byte, error)
}

type execCommandRunner struct{}

func (execCommandRunner) Run(stdin string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			return output, err
		}
		return output, fmt.Errorf("%w: %s", err, trimmed)
	}
	return output, nil
}

type AutoSecretStoreOptions struct {
	GOOS               string
	Namespace          string
	FallbackPassphrase string
	FallbackPath       string
	DarwinAvailable    *bool
	LinuxAvailable     *bool
	WindowsAvailable   *bool
	Runner             commandRunner
}

func NewDefaultSecretStore() SecretStore {
	return NewAutoSecretStore(AutoSecretStoreOptions{
		FallbackPassphrase: strings.TrimSpace(os.Getenv("ORI_VAULT_PASSPHRASE")),
	})
}

func NewDefaultSecretStoreForNamespace(namespace string) SecretStore {
	return NewAutoSecretStore(AutoSecretStoreOptions{
		Namespace:          namespace,
		FallbackPassphrase: strings.TrimSpace(os.Getenv("ORI_VAULT_PASSPHRASE")),
	})
}

func NewAutoSecretStore(opts AutoSecretStoreOptions) SecretStore {
	goos := strings.TrimSpace(opts.GOOS)
	if goos == "" {
		goos = runtime.GOOS
	}
	namespace := normalizeNamespace(opts.Namespace)

	runner := opts.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}

	switch goos {
	case "darwin":
		if isDarwinStoreAvailable(opts) {
			return newDarwinKeychainStore(runner, namespace)
		}
	case "linux":
		if isLinuxStoreAvailable(opts) {
			return newLinuxSecretServiceStore(runner, namespace)
		}
	case "windows":
		if isWindowsStoreAvailable(opts) {
			return newWindowsSecretStore(namespace)
		}
	}

	if strings.TrimSpace(opts.FallbackPassphrase) != "" {
		store, err := NewPassphraseSecretStore(resolveFallbackPath(opts.FallbackPath), opts.FallbackPassphrase)
		if err == nil {
			return store
		}
		return unavailableStore{
			status: StoreStatus{
				Backend:   BackendPassphraseFallback,
				Available: false,
				Writable:  false,
				Locked:    true,
				Message:   err.Error(),
			},
			err: err,
		}
	}

	return unavailableStore{
		status: StoreStatus{
			Backend:   BackendUnavailable,
			Available: false,
			Writable:  false,
			Locked:    true,
			Message:   unavailableMessageForOS(goos),
		},
		err: ErrSecretStoreUnavailable,
	}
}

func resolveFallbackPath(path string) string {
	path = strings.TrimSpace(path)
	if path != "" {
		return path
	}

	dataDir := strings.TrimSpace(os.Getenv("ORI_DATA_DIR"))
	if dataDir == "" {
		cwd, err := os.Getwd()
		if err == nil {
			dataDir = cwd
		} else {
			dataDir = "."
		}
	}
	return filepath.Join(dataDir, "vault_secrets.json")
}

func unavailableMessageForOS(goos string) string {
	switch goos {
	case "darwin":
		return "macOS Keychain is unavailable and no vault passphrase fallback is configured"
	case "linux":
		return "Linux Secret Service is unavailable and no vault passphrase fallback is configured"
	case "windows":
		return "Windows secure storage is unavailable and no vault passphrase fallback is configured"
	default:
		return "no supported secret store is available and no vault passphrase fallback is configured"
	}
}

func normalizeNamespace(namespace string) string {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return "default"
	}
	hash := sha256.Sum256([]byte(namespace))
	return hex.EncodeToString(hash[:8])
}

func isDarwinStoreAvailable(opts AutoSecretStoreOptions) bool {
	if opts.DarwinAvailable != nil {
		return *opts.DarwinAvailable
	}
	_, err := exec.LookPath("security")
	return err == nil
}

func isLinuxStoreAvailable(opts AutoSecretStoreOptions) bool {
	if opts.LinuxAvailable != nil {
		return *opts.LinuxAvailable
	}
	_, err := exec.LookPath("secret-tool")
	return err == nil
}

func isWindowsStoreAvailable(opts AutoSecretStoreOptions) bool {
	if opts.WindowsAvailable != nil {
		return *opts.WindowsAvailable
	}
	return false
}

type unavailableStore struct {
	status StoreStatus
	err    error
}

func (s unavailableStore) Get(key SecretKey) (string, error) {
	return "", s.err
}

func (s unavailableStore) Set(key SecretKey, value string) error {
	return s.err
}

func (s unavailableStore) Delete(key SecretKey) error {
	return s.err
}

func (s unavailableStore) Status() StoreStatus {
	return s.status
}

type MemorySecretStore struct {
	mu      sync.RWMutex
	secrets map[SecretKey]string
	status  StoreStatus
}

func NewMemorySecretStore() *MemorySecretStore {
	return &MemorySecretStore{
		secrets: make(map[SecretKey]string),
		status: StoreStatus{
			Backend:   BackendPassphraseFallback,
			Available: true,
			Writable:  true,
			Locked:    false,
			Message:   "in-memory test secret store",
		},
	}
}

func (s *MemorySecretStore) Get(key SecretKey) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.secrets[key]
	if !ok {
		return "", ErrSecretNotFound
	}
	return value, nil
}

func (s *MemorySecretStore) Set(key SecretKey, value string) error {
	value = strings.TrimSpace(value)
	s.mu.Lock()
	defer s.mu.Unlock()
	if value == "" {
		delete(s.secrets, key)
		return nil
	}
	s.secrets[key] = value
	return nil
}

func (s *MemorySecretStore) Delete(key SecretKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.secrets, key)
	return nil
}

func (s *MemorySecretStore) Status() StoreStatus {
	return s.status
}
