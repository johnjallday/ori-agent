package connections

import "strings"

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
