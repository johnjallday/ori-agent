package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/vault"
)

var (
	ErrResetCredentialStoreUnavailable = errors.New("reset credential store is unavailable")
	ErrResetCredentialStoreLocked      = errors.New("reset credential store is locked")
	ErrResetCredentialStoreReadOnly    = errors.New("reset credential store is not writable")
	ErrResetCredentialNamespace        = errors.New("reset credential namespace ownership is ambiguous")
	ErrResetCredentialInspection       = errors.New("reset credential presence could not be inspected")
	ErrResetCredentialVerification     = errors.New("reset credential deletion could not be verified")
)

var resetCredentialKeys = [...]vault.SecretKey{
	vault.SecretKeyOpenAIAPIKey,
	vault.SecretKeyAnthropicAPIKey,
	vault.SecretKeyGeminiAPIKey,
	vault.SecretKeyBraveAPIKey,
}

var installationSecretKeys = [...]vault.SecretKey{
	vault.SecretKeyOpenAIAPIKey,
	vault.SecretKeyAnthropicAPIKey,
	vault.SecretKeyGeminiAPIKey,
	vault.SecretKeyBraveAPIKey,
	vault.SecretKeyVaultDEK,
}

// ResetCredentialSlot describes presence without exposing a credential value.
// SecurePresent is nil when the exact Ori-owned slot could not be inspected.
type ResetCredentialSlot struct {
	Key             vault.SecretKey
	SecurePresent   *bool
	LegacyPlaintext bool
}

// ResetCredentialInspection is a read-only snapshot of the four provider and
// search key slots owned by Settings reset. It deliberately excludes vault_dek,
// connection credentials, external CLI auth and environment variables.
type ResetCredentialInspection struct {
	Store vault.StoreStatus
	Slots []ResetCredentialSlot
}

// ResetCredentialDeletion records exact-key outcomes without secret values.
type ResetCredentialDeletion struct {
	Key           vault.SecretKey
	Deleted       bool
	AlreadyAbsent bool
	Err           error
}

// ResetCredentialsResult reports secure-slot and legacy-settings persistence.
// A caller must treat any Err or SettingsSaved=false as partial/failed work.
type ResetCredentialsResult struct {
	Credentials            []ResetCredentialDeletion
	LegacyPlaintextCleared bool
	SettingsSaved          bool
}

// MigrateInstallationSecretNamespace copies known Ori-owned slots from the
// historical shared namespace into one installation-specific namespace. It
// never overwrites an existing destination and never deletes the source: old
// relative namespaces may have been shared by more than one installation.
func MigrateInstallationSecretNamespace(source, destination vault.SecretStore) error {
	if source == nil || destination == nil || source == destination {
		return nil
	}
	sourceStatus, destinationStatus := source.Status(), destination.Status()
	if !sourceStatus.Available || sourceStatus.Locked || !destinationStatus.Available || destinationStatus.Locked || !destinationStatus.Writable {
		return nil
	}
	var migrationErrs []error
	for _, key := range installationSecretKeys {
		if _, err := destination.Get(key); err == nil {
			continue
		} else if !errors.Is(err, vault.ErrSecretNotFound) {
			migrationErrs = append(migrationErrs, fmt.Errorf("inspect destination %s: %w", key, err))
			continue
		}
		value, err := source.Get(key)
		if errors.Is(err, vault.ErrSecretNotFound) {
			continue
		}
		if err != nil {
			migrationErrs = append(migrationErrs, fmt.Errorf("inspect source %s: %w", key, err))
			continue
		}
		if err := destination.Set(key, value); err != nil {
			migrationErrs = append(migrationErrs, fmt.Errorf("copy %s: %w", key, err))
			continue
		}
		verified, err := destination.Get(key)
		if err != nil || verified != value {
			migrationErrs = append(migrationErrs, fmt.Errorf("verify %s migration", key))
		}
	}
	return errors.Join(migrationErrs...)
}

// InspectResetCredentials checks only exact, manager-owned provider/search
// slots. It never reads environment variables and never returns secret values.
func (m *Manager) InspectResetCredentials() (ResetCredentialInspection, error) {
	m.mu.RLock()
	secretStore := m.secretStore
	settings := m.settings
	m.mu.RUnlock()

	result := ResetCredentialInspection{Slots: legacyResetCredentialSlots(settings)}
	// The default secure-store namespace is derived from this exact string. A
	// relative name such as settings.json is shared by every installation and
	// therefore cannot authorize deleting any matching native credential.
	if !filepath.IsAbs(m.filePath) || filepath.Clean(m.filePath) != m.filePath {
		return result, ErrResetCredentialNamespace
	}
	if secretStore == nil {
		result.Store = vault.StoreStatus{Backend: vault.BackendUnavailable, Locked: true}
		return result, ErrResetCredentialStoreUnavailable
	}
	result.Store = secretStore.Status()
	if result.Store.Locked {
		return result, ErrResetCredentialStoreLocked
	}
	if !result.Store.Available {
		return result, ErrResetCredentialStoreUnavailable
	}
	if !result.Store.Writable {
		return result, ErrResetCredentialStoreReadOnly
	}

	var inspectionErrs []error
	for i := range result.Slots {
		value, err := secretStore.Get(result.Slots[i].Key)
		switch {
		case err == nil:
			present := strings.TrimSpace(value) != ""
			result.Slots[i].SecurePresent = &present
		case errors.Is(err, vault.ErrSecretNotFound):
			present := false
			result.Slots[i].SecurePresent = &present
		default:
			inspectionErrs = append(inspectionErrs, fmt.Errorf("%s: %w", result.Slots[i].Key, err))
		}
	}
	if len(inspectionErrs) != 0 {
		return result, fmt.Errorf("%w: %w", ErrResetCredentialInspection, errors.Join(inspectionErrs...))
	}
	return result, nil
}

// DeleteResetCredentials removes only the four reviewed provider/search slots
// and their legacy plaintext fields. It first requires complete inspection, so
// a locked or ambiguous backend cannot be reported as an empty credential set.
// Per-key failures are retained while the remaining exact keys are attempted;
// vault_dek and all unrelated namespaces are never addressed.
func (m *Manager) DeleteResetCredentials() (ResetCredentialsResult, error) {
	inspection, err := m.InspectResetCredentials()
	if err != nil {
		return ResetCredentialsResult{}, err
	}
	secretStore := m.SecretStore()
	result := ResetCredentialsResult{Credentials: make([]ResetCredentialDeletion, 0, len(inspection.Slots))}
	var deleteErrs []error
	for _, slot := range inspection.Slots {
		item := ResetCredentialDeletion{Key: slot.Key, AlreadyAbsent: slot.SecurePresent != nil && !*slot.SecurePresent}
		if err := secretStore.Delete(slot.Key); err != nil {
			item.Err = err
			deleteErrs = append(deleteErrs, fmt.Errorf("delete %s: %w", slot.Key, err))
		} else {
			item.Deleted = slot.SecurePresent != nil && *slot.SecurePresent
		}
		result.Credentials = append(result.Credentials, item)
	}

	m.mu.Lock()
	m.settings.OpenAIAPIKey = ""
	m.settings.AnthropicAPIKey = ""
	m.settings.GeminiAPIKey = ""
	m.settings.Utility.BraveAPIKey = ""
	m.mu.Unlock()
	result.LegacyPlaintextCleared = true
	if err := m.Save(); err != nil {
		deleteErrs = append(deleteErrs, fmt.Errorf("save sanitized settings: %w", err))
	} else {
		result.SettingsSaved = true
	}
	verification, verifyErr := m.InspectResetCredentials()
	if verifyErr != nil {
		deleteErrs = append(deleteErrs, fmt.Errorf("%w: %w", ErrResetCredentialVerification, verifyErr))
	} else {
		for _, slot := range verification.Slots {
			if slot.SecurePresent == nil || *slot.SecurePresent || slot.LegacyPlaintext {
				deleteErrs = append(deleteErrs, fmt.Errorf("%w: %s remains", ErrResetCredentialVerification, slot.Key))
			}
		}
	}
	return result, errors.Join(deleteErrs...)
}

func legacyResetCredentialSlots(settings Settings) []ResetCredentialSlot {
	return []ResetCredentialSlot{
		{Key: vault.SecretKeyOpenAIAPIKey, LegacyPlaintext: strings.TrimSpace(settings.OpenAIAPIKey) != ""},
		{Key: vault.SecretKeyAnthropicAPIKey, LegacyPlaintext: strings.TrimSpace(settings.AnthropicAPIKey) != ""},
		{Key: vault.SecretKeyGeminiAPIKey, LegacyPlaintext: strings.TrimSpace(settings.GeminiAPIKey) != ""},
		{Key: vault.SecretKeyBraveAPIKey, LegacyPlaintext: strings.TrimSpace(settings.Utility.BraveAPIKey) != ""},
	}
}
