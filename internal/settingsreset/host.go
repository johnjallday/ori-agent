package settingsreset

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

// BeforeStores owns one installation, completes any validated pre-start reset,
// and pins the lease before a shell, database, settings manager, credential
// consumer, migration, importer, listener, or background service is created.
// Unsupported hosts retain resetstate's clean-install-only behavior.
func BeforeStores(ctx context.Context, dataDir string) (*resetstate.Lease, error) {
	return beforeStores(ctx, dataDir, "", false)
}

// BeforeStoresForRestart also checks that this exact operation still awaits a
// full process restart under the newly acquired lease. A stale launcher may
// neither apply a different receipt nor open a clean installation by mistake.
func BeforeStoresForRestart(ctx context.Context, dataDir, expectedOperation string) (*resetstate.Lease, error) {
	if !ValidOperationID(expectedOperation) || !resetstate.Supported() {
		return nil, ErrLifecycleUnavailable
	}
	return beforeStores(ctx, dataDir, expectedOperation, false)
}

// BeforeStoresAfterCancel is the restart counterpart for a reset a recovery
// host canceled. It requires that exact operation to be a durably canceled,
// strictly validated receipt, so a stale launcher can neither apply a different
// receipt nor open a clean installation by mistake. It applies nothing: a
// canceled receipt is recognised and retained, exactly like a completed one.
func BeforeStoresAfterCancel(ctx context.Context, dataDir, expectedOperation string) (*resetstate.Lease, error) {
	if !ValidOperationID(expectedOperation) || !resetstate.Supported() {
		return nil, ErrLifecycleUnavailable
	}
	return beforeStores(ctx, dataDir, expectedOperation, true)
}

func beforeStores(ctx context.Context, dataDir, expectedOperation string, afterCancel bool) (*resetstate.Lease, error) {
	if !resetstate.Supported() {
		return resetstate.BeforeStores(dataDir)
	}
	lease, err := resetstate.Acquire(dataDir)
	if err != nil {
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			_ = lease.Close()
		}
	}()
	if expectedOperation != "" {
		j, err := NewCoordinator(lease, nil, nil).load()
		switch {
		case err != nil || j == nil || j.Operation.ID != expectedOperation:
			return nil, ErrOperationNotFound
		case afterCancel && (j.Operation.State != StateCanceled || j.Operation.Cancellation == nil):
			return nil, ErrOperationNotFound
		case !afterCancel && (j.Operation.State != StateAwaitingRestart || j.AcceptedBy == lease.Identity()):
			return nil, ErrOperationNotFound
		}
	}
	if err := RecoverBeforeStores(ctx, lease, ProductionRecoveryOptions(dataDir)); err != nil {
		if !errors.Is(err, ErrRecoveryIncomplete) {
			return nil, err
		}
		// Keep exclusive ownership while a minimal read-only recovery host is
		// served. No application constructor may run on this path.
		if holdErr := lease.HoldForProcess(); holdErr != nil {
			return nil, errors.Join(err, holdErr)
		}
		owned = true
		return lease, err
	}
	if !lease.ExpectsOperation() {
		if err := lease.RequireCleanStart(); err != nil {
			return nil, err
		}
	}
	if err := lease.HoldForProcess(); err != nil {
		return nil, err
	}
	owned = true
	return lease, nil
}
