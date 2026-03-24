package vault

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type encryptedSecret struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type passphraseSecretsFile struct {
	Version int                        `json:"version"`
	Salt    string                     `json:"salt"`
	Secrets map[string]encryptedSecret `json:"secrets"`
}

type passphraseSecretStore struct {
	mu         sync.Mutex
	path       string
	passphrase string
}

func NewPassphraseSecretStore(path string, passphrase string) (SecretStore, error) {
	path = strings.TrimSpace(path)
	passphrase = strings.TrimSpace(passphrase)
	if path == "" {
		return nil, fmt.Errorf("vault: fallback secrets path is required")
	}
	if passphrase == "" {
		return nil, fmt.Errorf("vault: passphrase is required")
	}
	return &passphraseSecretStore{
		path:       path,
		passphrase: passphrase,
	}, nil
}

func (s *passphraseSecretStore) Get(key SecretKey) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := s.loadFile(false)
	if err != nil {
		return "", err
	}
	record, ok := file.Secrets[string(key)]
	if !ok {
		return "", ErrSecretNotFound
	}

	salt, err := base64.StdEncoding.DecodeString(file.Salt)
	if err != nil {
		return "", fmt.Errorf("decode salt: %w", err)
	}
	derivedKey := derivePassphraseKey(s.passphrase, salt)
	return decryptString(derivedKey, record.Nonce, record.Ciphertext)
}

func (s *passphraseSecretStore) Set(key SecretKey, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	value = strings.TrimSpace(value)
	if value == "" {
		return s.deleteLocked(key)
	}

	file, err := s.loadFile(true)
	if err != nil {
		return err
	}
	salt, err := base64.StdEncoding.DecodeString(file.Salt)
	if err != nil {
		return fmt.Errorf("decode salt: %w", err)
	}
	derivedKey := derivePassphraseKey(s.passphrase, salt)
	nonce, ciphertext, err := encryptString(derivedKey, value)
	if err != nil {
		return err
	}
	file.Secrets[string(key)] = encryptedSecret{
		Nonce:      nonce,
		Ciphertext: ciphertext,
	}
	return s.writeFile(file)
}

func (s *passphraseSecretStore) Delete(key SecretKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteLocked(key)
}

func (s *passphraseSecretStore) deleteLocked(key SecretKey) error {
	file, err := s.loadFile(false)
	if err != nil {
		if err == ErrSecretNotFound {
			return nil
		}
		return err
	}
	delete(file.Secrets, string(key))
	return s.writeFile(file)
}

func (s *passphraseSecretStore) Status() StoreStatus {
	return StoreStatus{
		Backend:   BackendPassphraseFallback,
		Available: true,
		Writable:  true,
		Locked:    false,
		Message:   "using passphrase-protected fallback secret store",
	}
}

func (s *passphraseSecretStore) loadFile(create bool) (*passphraseSecretsFile, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			if !create {
				return nil, ErrSecretNotFound
			}
			salt, saltErr := randomBytes(16)
			if saltErr != nil {
				return nil, fmt.Errorf("generate salt: %w", saltErr)
			}
			return &passphraseSecretsFile{
				Version: 1,
				Salt:    base64.StdEncoding.EncodeToString(salt),
				Secrets: make(map[string]encryptedSecret),
			}, nil
		}
		return nil, err
	}

	var file passphraseSecretsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode fallback secrets file: %w", err)
	}
	if file.Secrets == nil {
		file.Secrets = make(map[string]encryptedSecret)
	}
	if file.Salt == "" {
		return nil, fmt.Errorf("vault: fallback secrets file is missing salt")
	}
	if file.Version == 0 {
		file.Version = 1
	}
	return &file, nil
}

func (s *passphraseSecretStore) writeFile(file *passphraseSecretsFile) error {
	if file == nil {
		return fmt.Errorf("vault: fallback secrets file is nil")
	}
	if file.Secrets == nil {
		file.Secrets = make(map[string]encryptedSecret)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(s.path), "vault-secrets-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}
