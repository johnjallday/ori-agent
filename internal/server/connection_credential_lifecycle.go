package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// One authoritative Gmail credential, many references.
//
// Earlier builds gave each linked workspace its own copy of the same refresh
// token. That left duplicates behind after disconnect, made reauthorization
// ambiguous (which copy is current?), and multiplied the places a token could
// leak from. Linking now references the connection's single authoritative
// credential — but installations upgraded from those builds still hold the old
// copies, and they have to be consolidated.
//
// Consolidation deletes a credential. That is irreversible and, done wrong,
// destroys the user's access. So it runs only on PROOF, never on a heuristic:
//
//   - the duplicate must be provably the same account (provider, Google
//     identity, and vault all match) and provably Ori-created;
//   - every reference to it must be repointed and the repoint verified by
//     re-reading from the store;
//   - a full reverse scan must find zero remaining references;
//   - only then is the duplicate deleted, and it is deleted LAST.
//
// Anything ambiguous is PRESERVED with a token-free diagnostic. An orphaned
// credential wastes bytes; a deleted one the user still needed costs them their
// mailbox access.

// googleConnectionEmailSource marks vault email accounts Ori created from the
// unified Google connection. A record without it was set up some other way and
// is never a consolidation candidate.
//
// (Declared in connection_impact.go; referenced here for the proof rules.)

// consolidationFailure is the stable, token-free code for why a consolidation
// was refused. Each names the specific proof condition that did not hold.
type consolidationFailure string

const (
	consolidationOK consolidationFailure = ""
	// failureSameRecord: the "duplicate" IS the authoritative record.
	failureSameRecord consolidationFailure = "same_record"
	// failureProviderMismatch: not both Gmail.
	failureProviderMismatch consolidationFailure = "provider_mismatch"
	// failureIdentityUnknown: one side has no email address to compare, so the
	// records cannot be proven to be the same account.
	failureIdentityUnknown consolidationFailure = "identity_unknown"
	// failureIdentityMismatch: different Google accounts.
	failureIdentityMismatch consolidationFailure = "identity_mismatch"
	// failureVaultMismatch: stored in different vaults, so consolidating would
	// move a credential across a boundary the user drew.
	failureVaultMismatch consolidationFailure = "vault_mismatch"
	// failureNotWorkspaceOwned: the candidate is not a workspace-scoped copy.
	failureNotWorkspaceOwned consolidationFailure = "not_workspace_owned"
	// failureForeignSource: Ori did not create this record from the connection.
	failureForeignSource consolidationFailure = "foreign_source"
	// failureReferencesRemain: something still points at the candidate after the
	// repoint, so deleting it would orphan that reference.
	failureReferencesRemain consolidationFailure = "references_remain"
	// failureRepointUnverified: the repoint did not survive a re-read.
	failureRepointUnverified consolidationFailure = "repoint_unverified"
)

// consolidationVerdict is the outcome of proving a duplicate.
type consolidationVerdict struct {
	// Proven is true only when every condition held.
	Proven bool
	// Failure names the first condition that did not (empty when Proven).
	Failure consolidationFailure
	// Detail is a short, token-free explanation for diagnostics.
	Detail string
}

// proveDuplicate decides whether candidate is provably a redundant copy of
// authoritative.
//
// Email address alone is deliberately NOT sufficient (FR 71, 78): two records
// can share an address and hold credentials for different clients, scopes, or
// vaults. Every condition below must hold.
func proveDuplicate(authoritative, candidate *vault.EmailAccount) consolidationVerdict {
	if authoritative == nil || candidate == nil {
		return consolidationVerdict{Failure: failureIdentityUnknown, Detail: "a record could not be loaded"}
	}
	if strings.EqualFold(strings.TrimSpace(authoritative.ID), strings.TrimSpace(candidate.ID)) {
		return consolidationVerdict{Failure: failureSameRecord, Detail: "the candidate is the authoritative record"}
	}
	if authoritative.Provider != vault.EmailProviderGmail || candidate.Provider != vault.EmailProviderGmail {
		return consolidationVerdict{Failure: failureProviderMismatch, Detail: "both records must be Gmail"}
	}

	authEmail := strings.ToLower(strings.TrimSpace(authoritative.EmailAddress))
	candEmail := strings.ToLower(strings.TrimSpace(candidate.EmailAddress))
	if authEmail == "" || candEmail == "" {
		return consolidationVerdict{Failure: failureIdentityUnknown, Detail: "a record has no account identity to compare"}
	}
	if authEmail != candEmail {
		return consolidationVerdict{Failure: failureIdentityMismatch, Detail: "the records belong to different accounts"}
	}

	if strings.TrimSpace(authoritative.VaultID) != strings.TrimSpace(candidate.VaultID) {
		return consolidationVerdict{Failure: failureVaultMismatch, Detail: "the records live in different vaults"}
	}
	if strings.TrimSpace(candidate.WorkspaceID) == "" {
		return consolidationVerdict{Failure: failureNotWorkspaceOwned, Detail: "the candidate is not a workspace-scoped copy"}
	}
	if candidate.Source != googleConnectionEmailSource {
		return consolidationVerdict{Failure: failureForeignSource, Detail: "the candidate was not created by the Google connection"}
	}
	return consolidationVerdict{Proven: true}
}

// credentialReference is one place an email account id is used.
type credentialReference struct {
	// WorkspaceID is the workspace whose binding holds the reference.
	WorkspaceID string
	// BindingID is the native email binding.
	BindingID string
	// IsGrant marks the connection grant itself rather than a workspace binding.
	IsGrant bool
}

// credentialLifecycle owns the reverse-reference scan and proven consolidation.
type credentialLifecycle struct {
	vaults      *vault.Store
	workspaces  workspace.Store
	invalidator accountInvalidator
}

func newCredentialLifecycle(vaults *vault.Store, workspaces workspace.Store, invalidator accountInvalidator) *credentialLifecycle {
	return &credentialLifecycle{vaults: vaults, workspaces: workspaces, invalidator: invalidator}
}

// referencesTo scans EVERY workspace's native email bindings for uses of
// accountID, plus the active grant when grantRef matches.
//
// The scan is complete by construction: a partial scan would let a deletion
// orphan the binding it missed, and the whole point of the proof is that no
// such binding exists (FR 71, 73, 74, 78).
func (c *credentialLifecycle) referencesTo(accountID, grantRef string) ([]credentialReference, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, errors.New("server: an account id is required for a reference scan")
	}
	refs := make([]credentialReference, 0, 4)
	if strings.EqualFold(strings.TrimSpace(grantRef), accountID) {
		refs = append(refs, credentialReference{IsGrant: true})
	}
	if c.workspaces == nil {
		return refs, nil
	}

	ids, err := c.workspaces.List()
	if err != nil {
		return nil, fmt.Errorf("list workspaces for reference scan: %w", err)
	}
	for _, id := range ids {
		ws, err := c.workspaces.Get(id)
		if err != nil || ws == nil {
			// A workspace we cannot read might hold a reference. Refuse to
			// conclude "zero references" on incomplete information.
			return nil, fmt.Errorf("read workspace %s for reference scan: %w", id, err)
		}
		for _, binding := range ws.GetMCPBindings() {
			if !binding.IsNativeEmail() {
				continue
			}
			if strings.EqualFold(stringFromConfig(binding.Config, "account_id"), accountID) {
				refs = append(refs, credentialReference{WorkspaceID: ws.ID, BindingID: binding.ID})
			}
		}
	}
	return refs, nil
}

// consolidateDuplicate repoints every reference to duplicateID onto
// authoritativeID and then deletes the duplicate.
//
// The ordering is crash-safe on purpose: at every point between steps, a crash
// leaves a state where the binding still resolves to a real credential. The
// duplicate is deleted LAST, only after a fresh scan proves nothing points at
// it any more. Re-running after a partial completion is safe — each step is
// idempotent.
//
// It returns whether the duplicate was removed, and never returns an error for
// "not provable": that is a normal outcome that preserves the record.
func (c *credentialLifecycle) consolidateDuplicate(ctx context.Context, authoritativeID, duplicateID, grantRef string) (bool, error) {
	if c == nil || c.vaults == nil {
		return false, errors.New("server: credential consolidation is unavailable")
	}
	authoritativeID = strings.TrimSpace(authoritativeID)
	duplicateID = strings.TrimSpace(duplicateID)
	if authoritativeID == "" || duplicateID == "" {
		return false, errors.New("server: both credential ids are required")
	}

	authoritative, err := c.vaults.GetEmailAccount(ctx, authoritativeID)
	if err != nil {
		return false, err
	}
	duplicate, err := c.vaults.GetEmailAccount(ctx, duplicateID)
	if err != nil {
		// A record that no longer exists is a completed consolidation, not a
		// fault. Re-running after a partial completion must be safe.
		if errors.Is(err, vault.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if duplicate == nil {
		return false, nil // already gone: nothing to do, and that is success
	}

	// Step 1: prove it. An unprovable candidate is preserved, not deleted.
	if verdict := proveDuplicate(authoritative, duplicate); !verdict.Proven {
		c.logPreserved(duplicateID, verdict)
		return false, nil
	}

	// Step 2: repoint every reference. Until this succeeds the duplicate still
	// exists, so a crash here leaves every binding resolvable.
	refs, err := c.referencesTo(duplicateID, grantRef)
	if err != nil {
		return false, err
	}
	for _, ref := range refs {
		if ref.IsGrant {
			// The grant points at the duplicate, which means the duplicate is
			// authoritative after all. Refuse rather than delete the live one.
			c.logPreserved(duplicateID, consolidationVerdict{
				Failure: failureReferencesRemain,
				Detail:  "the active Google grant references this record",
			})
			return false, nil
		}
		if err := c.repointBinding(ref, authoritativeID); err != nil {
			return false, err
		}
	}

	// Step 3: verify the repoint survived a re-read. Trusting the write would
	// let a silently-dropped save orphan the binding at step 5.
	if err := c.verifyRepointed(refs, authoritativeID); err != nil {
		c.logPreserved(duplicateID, consolidationVerdict{
			Failure: failureRepointUnverified,
			Detail:  "the repointed bindings did not survive a re-read",
		})
		return false, err
	}

	// Step 4: a FRESH scan must find nothing. Anything left means a reference
	// appeared concurrently, and deleting now would orphan it.
	remaining, err := c.referencesTo(duplicateID, grantRef)
	if err != nil {
		return false, err
	}
	if len(remaining) > 0 {
		c.logPreserved(duplicateID, consolidationVerdict{
			Failure: failureReferencesRemain,
			Detail:  fmt.Sprintf("%d reference(s) still point at this record", len(remaining)),
		})
		return false, nil
	}

	// Step 5: delete last.
	if err := c.vaults.DeleteEmailAccount(ctx, duplicateID); err != nil {
		return false, err
	}
	if c.invalidator != nil {
		c.invalidator.InvalidateAccount(duplicateID)
	}
	logger.Info("Consolidated duplicate Gmail credential", logger.Fields{
		"repointed_references": len(refs),
	})
	return true, nil
}

// repointBinding moves one workspace binding onto the authoritative credential.
func (c *credentialLifecycle) repointBinding(ref credentialReference, authoritativeID string) error {
	if c.workspaces == nil {
		return errors.New("server: no workspace store for repointing")
	}
	return workspace.CanonicalUpdate(c.workspaces, ref.WorkspaceID, func(ws *workspace.Workspace) error {
		binding, ok := ws.GetMCPBinding(ref.BindingID)
		if !ok || binding == nil {
			return nil // vanished concurrently; the fresh scan below will confirm
		}
		if binding.Config == nil {
			binding.Config = map[string]any{}
		}
		binding.Config["account_id"] = authoritativeID
		// Relinking is also the moment to record the runtime kind explicitly, so
		// an upgraded binding stops relying on name-based classification.
		binding.RuntimeKind = workspace.RuntimeKindNativeEmail
		return ws.UpsertMCPBinding(*binding)
	})
}

// verifyRepointed re-reads each repointed binding from the store and confirms it
// now references the authoritative credential.
func (c *credentialLifecycle) verifyRepointed(refs []credentialReference, authoritativeID string) error {
	if c.workspaces == nil {
		return nil
	}
	for _, ref := range refs {
		if ref.IsGrant {
			continue
		}
		ws, err := c.workspaces.Get(ref.WorkspaceID)
		if err != nil || ws == nil {
			return fmt.Errorf("re-read workspace %s after repointing: %w", ref.WorkspaceID, err)
		}
		binding, ok := ws.GetMCPBinding(ref.BindingID)
		if !ok || binding == nil {
			continue // deleted concurrently: no longer a reference
		}
		if !strings.EqualFold(stringFromConfig(binding.Config, "account_id"), authoritativeID) {
			return fmt.Errorf("binding %s in workspace %s did not repoint", ref.BindingID, ref.WorkspaceID)
		}
	}
	return nil
}

// logPreserved records WHY a record was kept. The diagnostic names the failed
// proof condition and nothing else — no address, token, or vault name (FR 78).
func (c *credentialLifecycle) logPreserved(accountID string, verdict consolidationVerdict) {
	logger.Info("Preserved a Gmail credential rather than consolidating it", logger.Fields{
		"account_id": accountID, // an opaque local id, not a secret
		"condition":  string(verdict.Failure),
		"detail":     verdict.Detail,
	})
}

// deleteWorkspaceCredentialIfUnreferenced removes a workspace-only legacy
// credential after proving nothing references it (FR 73, 74).
//
// It refuses to touch the authoritative credential, so unlinking one workspace
// can never disconnect another — or the account itself.
func (c *credentialLifecycle) deleteWorkspaceCredentialIfUnreferenced(ctx context.Context, accountID, grantRef string) (bool, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return false, nil
	}
	if strings.EqualFold(accountID, strings.TrimSpace(grantRef)) {
		// The authoritative credential: unlinking a workspace must never delete it.
		return false, nil
	}
	account, err := c.vaults.GetEmailAccount(ctx, accountID)
	if err != nil {
		if errors.Is(err, vault.ErrRecordNotFound) {
			return false, nil // already gone
		}
		return false, err
	}
	if account == nil {
		return false, nil
	}
	if strings.TrimSpace(account.WorkspaceID) == "" {
		// A global record; not this workspace's to delete.
		c.logPreserved(accountID, consolidationVerdict{
			Failure: failureNotWorkspaceOwned,
			Detail:  "the record is not workspace-scoped",
		})
		return false, nil
	}
	if account.Source != googleConnectionEmailSource {
		c.logPreserved(accountID, consolidationVerdict{
			Failure: failureForeignSource,
			Detail:  "the record was not created by the Google connection",
		})
		return false, nil
	}

	refs, err := c.referencesTo(accountID, grantRef)
	if err != nil {
		return false, err
	}
	if len(refs) > 0 {
		c.logPreserved(accountID, consolidationVerdict{
			Failure: failureReferencesRemain,
			Detail:  fmt.Sprintf("%d reference(s) still point at this record", len(refs)),
		})
		return false, nil
	}

	if err := c.vaults.DeleteEmailAccount(ctx, accountID); err != nil {
		return false, err
	}
	if c.invalidator != nil {
		c.invalidator.InvalidateAccount(accountID)
	}
	return true, nil
}
