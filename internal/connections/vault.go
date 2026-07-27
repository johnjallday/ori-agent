package connections

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Credential-vault resolution for a Google connection. FR 9 fixes the selection
// matrix; FR 10 governs whether the resolved vault is usable right now. Both are
// pure decisions here — the actual vault store is wired in a later group.

// VaultAction is the resolved next step for choosing the connection's vault.
type VaultAction string

const (
	// VaultUseSaved: the connection already remembers a vault; reuse it.
	VaultUseSaved VaultAction = "use_saved"
	// VaultAutoSelect: no saved vault but exactly one exists; select it silently.
	VaultAutoSelect VaultAction = "auto_select"
	// VaultCreate: no vault exists; run the normal inline creation flow.
	VaultCreate VaultAction = "create"
	// VaultPrompt: several vaults exist and none is saved; ask once.
	VaultPrompt VaultAction = "prompt"
)

// VaultResolution is the outcome of ResolveVault. VaultID is set only for
// VaultUseSaved and VaultAutoSelect.
type VaultResolution struct {
	Action  VaultAction
	VaultID string
}

// ResolveVault implements FR 9: reuse a saved vault; else auto-select the sole
// vault; else create when none exist; else prompt once when several exist.
// Product enablement must never re-run this once a choice is stored, so callers
// pass the connection's saved vault id (empty if none chosen yet).
func ResolveVault(savedID string, available []string) VaultResolution {
	if strings.TrimSpace(savedID) != "" {
		return VaultResolution{Action: VaultUseSaved, VaultID: savedID}
	}
	switch len(available) {
	case 0:
		return VaultResolution{Action: VaultCreate}
	case 1:
		return VaultResolution{Action: VaultAutoSelect, VaultID: available[0]}
	default:
		return VaultResolution{Action: VaultPrompt}
	}
}

// VaultAvailability is the runtime status of the resolved vault (FR 10).
type VaultAvailability string

const (
	VaultAvailable VaultAvailability = "available"
	VaultLocked    VaultAvailability = "locked"
	VaultMissing   VaultAvailability = "missing"
)

// RequiresRepair reports whether authorization must pause for an unlock/repair
// step before it can begin, and resume the intended action afterward (FR 10).
func (a VaultAvailability) RequiresRepair() bool {
	return a == VaultLocked || a == VaultMissing
}

// --- Preflight ---------------------------------------------------------------
//
// Gmail authorization must not send the browser to Google until Ori knows which
// vault will receive the credential and that the vault can be written right now
// (stabilization FR 1, 3-9). Preflight combines the pure selection matrix above
// with live vault availability and yields exactly one next step for the UI.

// VaultRef is one vault as the connection domain sees it: an id, a display name,
// and whether it is usable right now. Nothing secret crosses this boundary — no
// path, password, key, or record content.
type VaultRef struct {
	ID           string
	Name         string
	Availability VaultAvailability
}

// VaultCatalog is the connection domain's read-only window onto Ori's vault
// store. It is implemented by an adapter in internal/server so this package
// never imports internal/vault (Technical Considerations).
type VaultCatalog interface {
	// ListVaults returns every vault with its current availability.
	ListVaults(ctx context.Context) ([]VaultRef, error)
	// VaultAvailability reports one vault's current availability. A vault that no
	// longer exists must report VaultMissing rather than an error.
	VaultAvailability(ctx context.Context, vaultID string) (VaultAvailability, error)
}

// VaultOutcome is preflight's single next step.
type VaultOutcome string

const (
	// VaultOutcomeReady: a writable vault is resolved; authorization may start.
	VaultOutcomeReady VaultOutcome = "ready"
	// VaultOutcomeCreate: no vault exists; run the inline creation flow (FR 5).
	VaultOutcomeCreate VaultOutcome = "create"
	// VaultOutcomeChoose: several vaults exist and none is recorded (FR 6).
	VaultOutcomeChoose VaultOutcome = "choose"
	// VaultOutcomeUnlock: the resolved vault is password-protected and locked (FR 8).
	VaultOutcomeUnlock VaultOutcome = "unlock"
	// VaultOutcomeRepair: the recorded vault is missing or unavailable (FR 9).
	VaultOutcomeRepair VaultOutcome = "repair"
)

// VaultPreflight is preflight's outcome. VaultID/VaultName are set whenever a
// specific vault is implicated (ready, unlock, repair). Options carries the
// selectable vaults for the choose and repair states so the UI never has to
// re-list them.
type VaultPreflight struct {
	Outcome   VaultOutcome
	VaultID   string
	VaultName string
	Options   []VaultRef
}

// ErrVaultActionRequired marks every preflight outcome that blocks
// authorization. Callers match it with errors.Is and read the details from
// *VaultPreflightError.
var ErrVaultActionRequired = errors.New("connections: vault action required before authorization")

// VaultPreflightError halts an authorization that cannot safely start until the
// user creates, chooses, or unlocks a vault. It carries the full preflight so
// the HTTP layer can render the exact repair action (FR 5-9).
type VaultPreflightError struct {
	Preflight VaultPreflight
}

func (e *VaultPreflightError) Error() string {
	return fmt.Sprintf("%s: %s", ErrVaultActionRequired.Error(), e.Preflight.Outcome)
}

// Is makes errors.Is(err, ErrVaultActionRequired) true for any preflight block.
func (e *VaultPreflightError) Is(target error) bool { return target == ErrVaultActionRequired }

// PreflightVault resolves which vault a product credential will be stored in and
// whether it is writable right now. savedID is the vault already recorded on the
// connection (empty when none has been chosen).
//
// It never guesses among several vaults and never treats a locked or missing
// vault as usable — those are the two failures that previously surfaced only
// after the user had already authorized at Google.
func PreflightVault(ctx context.Context, catalog VaultCatalog, savedID string) (VaultPreflight, error) {
	if catalog == nil {
		return VaultPreflight{}, errors.New("connections: no vault catalog configured")
	}
	available, err := catalog.ListVaults(ctx)
	if err != nil {
		return VaultPreflight{}, err
	}
	ids := make([]string, 0, len(available))
	for _, v := range available {
		ids = append(ids, v.ID)
	}

	switch res := ResolveVault(savedID, ids); res.Action {
	case VaultUseSaved, VaultAutoSelect:
		return preflightSelected(ctx, catalog, available, res.VaultID)
	case VaultCreate:
		return VaultPreflight{Outcome: VaultOutcomeCreate}, nil
	default: // VaultPrompt
		return VaultPreflight{Outcome: VaultOutcomeChoose, Options: available}, nil
	}
}

// preflightSelected checks one resolved vault's live availability. A vault that
// has dropped out of the catalog entirely is a repair, not a lock: the user has
// to pick or create a replacement (FR 9).
func preflightSelected(ctx context.Context, catalog VaultCatalog, available []VaultRef, vaultID string) (VaultPreflight, error) {
	name := ""
	found := false
	for _, v := range available {
		if v.ID == vaultID {
			name, found = v.Name, true
			break
		}
	}
	if !found {
		return VaultPreflight{Outcome: VaultOutcomeRepair, VaultID: vaultID, Options: available}, nil
	}

	availability, err := catalog.VaultAvailability(ctx, vaultID)
	if err != nil {
		return VaultPreflight{}, err
	}
	switch availability {
	case VaultLocked:
		return VaultPreflight{Outcome: VaultOutcomeUnlock, VaultID: vaultID, VaultName: name}, nil
	case VaultMissing:
		return VaultPreflight{Outcome: VaultOutcomeRepair, VaultID: vaultID, VaultName: name, Options: available}, nil
	default:
		return VaultPreflight{Outcome: VaultOutcomeReady, VaultID: vaultID, VaultName: name}, nil
	}
}
