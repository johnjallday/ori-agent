package vault

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

func (s *Store) ensureDataEncryptionKey(ctx context.Context, vaultID string, allowCreate bool) ([]byte, error) {
	vaultID = normalizeVaultID(vaultID)
	_ = allowCreate

	s.mu.RLock()
	if cached := s.cachedDEKs[vaultID]; len(cached) > 0 {
		keyCopy := append([]byte(nil), cached...)
		s.mu.RUnlock()
		return keyCopy, nil
	}
	s.mu.RUnlock()

	keyMaterial, err := s.getVaultKeyMaterial(ctx, vaultID)
	if err != nil {
		return nil, err
	}
	if keyMaterial.PasswordProtected {
		return nil, ErrVaultLocked
	}
	if allowCreate {
		return nil, ErrVaultKeyUnavailable
	}
	return nil, ErrVaultKeyUnavailable
}

func encryptRecordMetadata(dek []byte, metadata encryptedRecordMetadata) (string, string, error) {
	normalizedFolderPath, err := normalizeFolderPath(metadata.FolderPath)
	if err != nil {
		return "", "", err
	}
	metadata.FolderPath = normalizedFolderPath
	metadata.Tags = normalizeTags(metadata.Tags)
	return encryptJSON(dek, metadata)
}

func decryptRecordMetadata(dek []byte, nonceB64 string, ciphertextB64 string) (encryptedRecordMetadata, error) {
	var metadata encryptedRecordMetadata
	if err := decryptJSON(dek, nonceB64, ciphertextB64, &metadata); err != nil {
		return encryptedRecordMetadata{}, ErrMalformedRecord
	}
	normalizedFolderPath, err := normalizeFolderPath(metadata.FolderPath)
	if err != nil {
		return encryptedRecordMetadata{}, ErrMalformedRecord
	}
	metadata.FolderPath = normalizedFolderPath
	metadata.Tags = normalizeTags(metadata.Tags)
	return metadata, nil
}

func (s *Store) hasCachedDEK(vaultID string) bool {
	vaultID = normalizeVaultID(vaultID)
	if vaultID == "" {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.cachedDEKs[vaultID]) > 0
}

func (s *Store) getVaultKeyMaterial(ctx context.Context, vaultID string) (vaultKeyMaterial, error) {
	selectedVault, err := s.getVault(ctx, vaultID)
	if err != nil {
		return vaultKeyMaterial{}, err
	}
	if strings.TrimSpace(selectedVault.FilePath) == "" {
		return vaultKeyMaterial{}, ErrVaultKeyUnavailable
	}

	vaultFileDB, err := openExistingVaultFile(ctx, s.resolveVaultFileAbsolutePath(selectedVault.FilePath))
	if err != nil {
		return vaultKeyMaterial{}, fmt.Errorf("open vault file: %w", err)
	}
	defer func() { _ = vaultFileDB.Close() }()

	metadata, err := loadVaultFileMetadata(ctx, vaultFileDB, selectedVault.ID)
	if err != nil {
		return vaultKeyMaterial{}, err
	}

	material := vaultKeyMaterial{
		Salt:              metadata.KeySalt,
		Nonce:             metadata.KeyNonce,
		Ciphertext:        metadata.KeyCiphertext,
		PasswordProtected: strings.TrimSpace(metadata.KeyCiphertext) != "",
	}
	if !material.PasswordProtected {
		return vaultKeyMaterial{}, ErrVaultKeyUnavailable
	}
	return material, nil
}

func passwordProtectedStoreStatus(locked bool) StoreStatus {
	return StoreStatus{
		Backend:   BackendVaultPassword,
		Available: true,
		Writable:  true,
		Locked:    locked,
		Message:   "using this vault's password-protected encryption key",
	}
}

func validateVaultPassword(password string) error {
	if strings.TrimSpace(password) == "" {
		return ErrVaultPasswordRequired
	}
	return nil
}

func wrapVaultDataEncryptionKey(password string, dek []byte) (string, string, string, error) {
	salt, err := randomBytes(16)
	if err != nil {
		return "", "", "", fmt.Errorf("generate vault password salt: %w", err)
	}

	derivedKey := derivePassphraseKey(password, salt)
	nonce, ciphertext, err := encryptBytes(derivedKey, dek)
	if err != nil {
		return "", "", "", fmt.Errorf("encrypt vault data encryption key: %w", err)
	}

	return base64.StdEncoding.EncodeToString(salt), nonce, ciphertext, nil
}

func unwrapVaultDataEncryptionKey(password string, material vaultKeyMaterial) ([]byte, error) {
	if !material.PasswordProtected {
		return nil, ErrVaultKeyUnavailable
	}

	salt, err := base64.StdEncoding.DecodeString(material.Salt)
	if err != nil {
		return nil, ErrVaultKeyUnavailable
	}
	derivedKey := derivePassphraseKey(password, salt)
	dek, err := decryptBytes(derivedKey, material.Nonce, material.Ciphertext)
	if err != nil {
		return nil, ErrVaultPasswordInvalid
	}
	if len(dek) != int(argonKeyLength) {
		return nil, ErrVaultKeyUnavailable
	}
	return dek, nil
}
