package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

const (
	argonIterations  uint32 = 3
	argonMemoryKiB   uint32 = 64 * 1024
	argonParallelism uint8  = 4
	argonKeyLength   uint32 = 32
)

func randomBytes(size int) ([]byte, error) {
	buf := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func derivePassphraseKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, argonIterations, argonMemoryKiB, argonParallelism, argonKeyLength)
}

func generateDataEncryptionKey() ([]byte, error) {
	return randomBytes(int(argonKeyLength))
}

func encryptBytes(key []byte, plaintext []byte) (nonceB64 string, ciphertextB64 string, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", fmt.Errorf("create gcm: %w", err)
	}
	nonce, err := randomBytes(gcm.NonceSize())
	if err != nil {
		return "", "", fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(nonce), base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decryptBytes(key []byte, nonceB64 string, ciphertextB64 string) ([]byte, error) {
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt secret: %w", err)
	}
	return plaintext, nil
}

func encryptString(key []byte, plaintext string) (nonceB64 string, ciphertextB64 string, err error) {
	return encryptBytes(key, []byte(plaintext))
}

func decryptString(key []byte, nonceB64 string, ciphertextB64 string) (string, error) {
	plaintext, err := decryptBytes(key, nonceB64, ciphertextB64)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func encryptJSON(key []byte, value interface{}) (nonceB64 string, ciphertextB64 string, err error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", "", fmt.Errorf("marshal json: %w", err)
	}
	return encryptBytes(key, data)
}

func decryptJSON(key []byte, nonceB64 string, ciphertextB64 string, value interface{}) error {
	data, err := decryptBytes(key, nonceB64, ciphertextB64)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("unmarshal json: %w", err)
	}
	return nil
}
