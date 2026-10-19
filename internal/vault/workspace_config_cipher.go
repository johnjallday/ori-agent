package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
)

var (
	ErrWorkspaceConfigKeyUnavailable = errors.New("installation configuration key is unavailable")
	ErrWorkspaceConfigKeyInvalid     = errors.New("installation configuration key is invalid")
	workspaceConfigKeyMu             sync.Mutex
)

// WorkspaceConfigCipher is shared by installation-local encrypted state owners.
// All writers of SecretKeyWorkspaceConfigDEK must use this creation lock: two
// independent package locks could overwrite each other's newly generated key.
// Callers must use distinct authenticated domain/owner/item identity data. It
// never returns the key, overwrites an existing value, or supplies a plaintext
// fallback. An installation lease still excludes other processes.
func WorkspaceConfigCipher(secrets SecretStore, create bool) (cipher.AEAD, error) {
	if secrets == nil {
		return nil, ErrWorkspaceConfigKeyUnavailable
	}
	workspaceConfigKeyMu.Lock()
	defer workspaceConfigKeyMu.Unlock()
	encoded, err := secrets.Get(SecretKeyWorkspaceConfigDEK)
	if errors.Is(err, ErrSecretNotFound) && create {
		key := make([]byte, 32)
		defer clear(key)
		if _, err := rand.Read(key); err != nil {
			return nil, ErrWorkspaceConfigKeyUnavailable
		}
		encoded = base64.StdEncoding.EncodeToString(key)
		err = secrets.Set(SecretKeyWorkspaceConfigDEK, encoded)
	}
	if err != nil {
		return nil, ErrWorkspaceConfigKeyUnavailable
	}
	if len(encoded) != base64.StdEncoding.EncodedLen(32) {
		return nil, ErrWorkspaceConfigKeyInvalid
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	defer clear(key)
	if err != nil || len(key) != 32 {
		return nil, ErrWorkspaceConfigKeyInvalid
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrWorkspaceConfigKeyInvalid
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, ErrWorkspaceConfigKeyInvalid
	}
	return aead, nil
}
