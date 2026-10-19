package vault

import (
	"bytes"
	"crypto/cipher"
	"sync"
	"sync/atomic"
	"testing"
)

type countedConfigSecrets struct {
	*MemorySecretStore
	sets atomic.Int32
}

func (s *countedConfigSecrets) Set(key SecretKey, value string) error {
	if key == SecretKeyWorkspaceConfigDEK {
		s.sets.Add(1)
	}
	return s.MemorySecretStore.Set(key, value)
}

func TestWorkspaceConfigCipherSharesOneCreationLockAcrossOwners(t *testing.T) {
	secrets := &countedConfigSecrets{MemorySecretStore: NewMemorySecretStore()}
	var group sync.WaitGroup
	ciphers := make([]cipher.AEAD, 8)
	for i := range ciphers {
		group.Go(func() {
			var err error
			ciphers[i], err = WorkspaceConfigCipher(secrets, true)
			if err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if secrets.sets.Load() != 1 {
		t.Fatal("concurrent owners overwrote the configuration key")
	}
	for _, aead := range ciphers {
		if aead == nil {
			t.Fatal("cipher unavailable")
		}
	}
	aad := []byte("invented-domain/owner/configuration")
	sealed := ciphers[0].Seal(nil, nil, []byte("invented private config"), aad)
	for _, aead := range ciphers {
		plain, err := aead.Open(nil, nil, sealed, aad)
		if err != nil || string(plain) != "invented private config" {
			t.Fatal("owners used different keys")
		}
		if _, err := aead.Open(nil, nil, sealed, []byte("other-domain/owner/configuration")); err == nil {
			t.Fatal("authenticated identity ignored")
		}
	}
	other := ciphers[0].Seal(nil, nil, []byte("invented private config"), aad)
	if bytes.Equal(other, sealed) {
		t.Fatal("random nonce was reused")
	}
}

func TestWorkspaceConfigCipherNeverRepairsOrCreatesOnRead(t *testing.T) {
	secrets := &countedConfigSecrets{MemorySecretStore: NewMemorySecretStore()}
	if _, err := WorkspaceConfigCipher(secrets, false); err != ErrWorkspaceConfigKeyUnavailable || secrets.sets.Load() != 0 {
		t.Fatal("read minted a key")
	}
	if err := secrets.Set(SecretKeyWorkspaceConfigDEK, "invented-invalid-key"); err != nil {
		t.Fatal(err)
	}
	for _, create := range []bool{false, true} {
		if _, err := WorkspaceConfigCipher(secrets, create); err != ErrWorkspaceConfigKeyInvalid {
			t.Fatal("corrupt key accepted/repaired")
		}
	}
	if secrets.sets.Load() != 1 {
		t.Fatal("existing corrupt key was overwritten")
	}
	if _, err := WorkspaceConfigCipher(nil, true); err != ErrWorkspaceConfigKeyUnavailable {
		t.Fatal("nil backend accepted")
	}
}
