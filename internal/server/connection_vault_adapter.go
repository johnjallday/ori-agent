package server

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/vault"
)

// connectionVaultCatalog adapts the vault store to connections.VaultCatalog so
// Gmail authorization can resolve — and verify — its destination vault BEFORE
// the browser leaves Ori for Google (stabilization FR 1, 3-9). It is the only
// place where the connection flow and internal/vault meet; the connections
// domain itself stays free of vault storage types.
//
// Nothing secret crosses this seam: only vault ids, display names, and an
// availability verdict.
type connectionVaultCatalog struct {
	store *vault.Store
}

func newConnectionVaultCatalog(store *vault.Store) *connectionVaultCatalog {
	return &connectionVaultCatalog{store: store}
}

// errVaultStoreUnavailable means the adapter was built without a vault store, so
// no vault decision can be made at all (distinct from "no vaults exist yet").
var errVaultStoreUnavailable = errors.New("server: vault store unavailable")

// ListVaults returns every vault with its live availability. A vault whose
// storage file has gone missing is reported as VaultMissing rather than being
// hidden, so the connection card can offer repair instead of silently dropping
// the user's remembered choice.
func (c *connectionVaultCatalog) ListVaults(ctx context.Context) ([]connections.VaultRef, error) {
	if c == nil || c.store == nil {
		return nil, errVaultStoreUnavailable
	}
	vaults, err := c.store.ListVaults(ctx)
	if err != nil {
		return nil, err
	}
	refs := make([]connections.VaultRef, 0, len(vaults))
	for _, v := range vaults {
		availability, err := c.availabilityOf(ctx, v)
		if err != nil {
			return nil, err
		}
		refs = append(refs, connections.VaultRef{ID: v.ID, Name: v.Name, Availability: availability})
	}
	return refs, nil
}

// VaultAvailability reports one vault's current availability. A vault that no
// longer exists reports VaultMissing rather than an error, because "the vault
// you chose is gone" is a repair the user can act on, not a server fault.
func (c *connectionVaultCatalog) VaultAvailability(ctx context.Context, vaultID string) (connections.VaultAvailability, error) {
	if c == nil || c.store == nil {
		return "", errVaultStoreUnavailable
	}
	if strings.TrimSpace(vaultID) == "" {
		return connections.VaultMissing, nil
	}
	status, err := c.store.Status(ctx, vaultID)
	if err != nil {
		if errors.Is(err, vault.ErrVaultNotFound) {
			return connections.VaultMissing, nil
		}
		return "", err
	}
	return availabilityFromStatus(status), nil
}

// availabilityOf derives a listed vault's availability. FileMissing is decided
// from the catalog row directly; everything else needs the live lock state,
// which only Status knows (it reflects whether the DEK is cached this process).
func (c *connectionVaultCatalog) availabilityOf(ctx context.Context, v vault.Vault) (connections.VaultAvailability, error) {
	if v.FileMissing {
		return connections.VaultMissing, nil
	}
	status, err := c.store.Status(ctx, v.ID)
	if err != nil {
		if errors.Is(err, vault.ErrVaultNotFound) {
			return connections.VaultMissing, nil
		}
		return "", err
	}
	return availabilityFromStatus(status), nil
}

// availabilityFromStatus maps a vault status onto the connection domain's
// three-state verdict. Order matters: missing storage outranks locked, because
// unlocking a vault whose file is gone cannot succeed.
func availabilityFromStatus(status vault.VaultStatus) connections.VaultAvailability {
	switch {
	case status.FileMissing || !status.Available:
		return connections.VaultMissing
	case status.Locked || !status.Writable:
		return connections.VaultLocked
	default:
		return connections.VaultAvailable
	}
}
