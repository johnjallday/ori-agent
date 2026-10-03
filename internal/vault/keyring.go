package vault

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// SecretKeyRememberedVault names the installation secret holding the one vault
// Ori unlocks by itself: its ID and its generated password.
const SecretKeyRememberedVault SecretKey = "remembered_vault" // #nosec G101 -- key name, not a credential

// rememberedVaultPasswordBytes is the entropy of a generated vault password.
const rememberedVaultPasswordBytes = 32

// ErrKeyringUnavailable means the installation secret store cannot hold the
// remembered vault's password, so Ori could not unlock it after a restart.
var ErrKeyringUnavailable = errors.New("vault: secret store cannot remember a vault password")

// Keyring keeps one vault that Ori opens without asking anyone for a password.
//
// Every vault is encrypted with a password and locks when Ori restarts. That is
// right for a vault the user made, but credentials Ori needs unattended — the
// mail login the morning brief reads with — would then be unreadable until the
// user typed that password. The remembered vault is an ordinary vault whose
// random password lives in the installation secret store (the macOS Keychain on
// a Mac), the same place Ori keeps provider API keys. The vault file stays
// encrypted and portable; the Keychain is what protects it on this machine.
type Keyring struct {
	vaults  *Store
	secrets SecretStore
}

// NewKeyring builds the keyring. Either argument may be nil, in which case
// every call reports ErrKeyringUnavailable.
func NewKeyring(vaults *Store, secrets SecretStore) *Keyring {
	return &Keyring{vaults: vaults, secrets: secrets}
}

type rememberedVault struct {
	VaultID  string `json:"vault_id"`
	Password string `json:"password"`
}

// EnsureVault returns the remembered vault's ID, unlocked, creating the vault
// on first use. name is the display name for a new vault; a taken name gets a
// number appended.
func (k *Keyring) EnsureVault(ctx context.Context, name string) (string, error) {
	if !k.available() {
		return "", ErrKeyringUnavailable
	}
	if id, err := k.unlockRemembered(ctx); err == nil {
		return id, nil
	} else if !errors.Is(err, ErrSecretNotFound) && !errors.Is(err, ErrVaultNotFound) {
		return "", err
	}
	return k.create(ctx, name)
}

// UnlockRemembered unlocks the remembered vault if there is one. It is what
// keeps stored mail logins readable after a restart. No remembered vault is
// not an error.
func (k *Keyring) UnlockRemembered(ctx context.Context) error {
	if !k.available() {
		return nil
	}
	_, err := k.unlockRemembered(ctx)
	switch {
	case errors.Is(err, ErrSecretNotFound), errors.Is(err, ErrVaultNotFound):
		return nil
	case errors.Is(err, ErrSecretStoreUnavailable), errors.Is(err, ErrSecretStoreUnsupported):
		// No secret store on this machine, so nothing was ever remembered.
		return nil
	}
	return err
}

// RememberedVaultID reports the remembered vault, or "" when there is none.
func (k *Keyring) RememberedVaultID() string {
	if !k.available() {
		return ""
	}
	remembered, err := k.read()
	if err != nil {
		return ""
	}
	return remembered.VaultID
}

// Forget drops the remembered password when vaultID is the remembered vault,
// so a deleted vault leaves nothing behind in the Keychain.
func (k *Keyring) Forget(vaultID string) error {
	if !k.available() {
		return nil
	}
	remembered, err := k.read()
	if errors.Is(err, ErrSecretNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if normalizeVaultID(remembered.VaultID) != normalizeVaultID(vaultID) {
		return nil
	}
	if err := k.secrets.Delete(SecretKeyRememberedVault); err != nil && !errors.Is(err, ErrSecretNotFound) {
		return err
	}
	return nil
}

func (k *Keyring) available() bool {
	return k != nil && k.vaults != nil && k.secrets != nil
}

// unlockRemembered unlocks the recorded vault. A recorded vault that no longer
// exists is forgotten and reported as ErrVaultNotFound.
func (k *Keyring) unlockRemembered(ctx context.Context) (string, error) {
	remembered, err := k.read()
	if err != nil {
		return "", err
	}
	if _, err := k.vaults.getVault(ctx, remembered.VaultID); err != nil {
		if errors.Is(err, ErrVaultNotFound) {
			_ = k.secrets.Delete(SecretKeyRememberedVault)
		}
		return "", err
	}
	if k.vaults.hasCachedDEK(remembered.VaultID) {
		return remembered.VaultID, nil
	}
	if err := k.vaults.Unlock(ctx, remembered.VaultID, remembered.Password); err != nil {
		return "", err
	}
	return remembered.VaultID, nil
}

// create makes the vault and records its password. The password is recorded
// first, so there is never a remembered vault whose password was lost.
func (k *Keyring) create(ctx context.Context, name string) (string, error) {
	password, err := generateVaultPassword()
	if err != nil {
		return "", err
	}
	id := normalizeVaultID(uuid.New().String())
	if err := k.write(rememberedVault{VaultID: id, Password: password}); err != nil {
		return "", err
	}

	base := strings.TrimSpace(name)
	if base == "" {
		base = "Ori"
	}
	for attempt := 1; attempt <= 20; attempt++ {
		candidate := base
		if attempt > 1 {
			candidate = fmt.Sprintf("%s %d", base, attempt)
		}
		item := &Vault{ID: id, Name: candidate, Description: "Created by Ori. Its password is kept in this computer's keychain so Ori can open it after a restart."}
		err = k.vaults.CreateVault(ctx, item, password)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, ErrVaultAlreadyExists) {
			break
		}
	}
	_ = k.secrets.Delete(SecretKeyRememberedVault)
	return "", err
}

func (k *Keyring) read() (rememberedVault, error) {
	raw, err := k.secrets.Get(SecretKeyRememberedVault)
	if errors.Is(err, ErrSecretNotFound) {
		return rememberedVault{}, err
	}
	if err != nil {
		// A store that cannot be read (none on this machine, or a locked
		// keychain) cannot hold the remembered password either.
		return rememberedVault{}, fmt.Errorf("%w: %w", ErrKeyringUnavailable, err)
	}
	var remembered rememberedVault
	if err := json.Unmarshal([]byte(raw), &remembered); err != nil || remembered.VaultID == "" || remembered.Password == "" {
		// An unreadable entry cannot open anything; treat it as absent.
		return rememberedVault{}, ErrSecretNotFound
	}
	return remembered, nil
}

func (k *Keyring) write(remembered rememberedVault) error {
	data, err := json.Marshal(remembered)
	if err != nil {
		return err
	}
	if err := k.secrets.Set(SecretKeyRememberedVault, string(data)); err != nil {
		return fmt.Errorf("%w: %w", ErrKeyringUnavailable, err)
	}
	return nil
}

func generateVaultPassword() (string, error) {
	buf := make([]byte, rememberedVaultPasswordBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate vault password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
