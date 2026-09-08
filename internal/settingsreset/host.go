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
