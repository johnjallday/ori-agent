package server

import (
	"context"
	"errors"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

var (
	errResetDrainNotFenced = errors.New("reset drain requires an idle fenced runtime")
	errResetOwnersRemain   = errors.New("reset drain could not verify every lifetime owner stopped")
)

// serverResetLifecycle is the concrete non-cancelling fence/drain mechanism.
// Production reset remains intentionally unwired until pre-start apply and
// recovery can consume the resulting receipt; tests construct this directly.
const resetDrainTimeout = 30 * time.Second

type serverResetLifecycle struct{ server *Server }

func newServerResetLifecycle(server *Server) *serverResetLifecycle {
	return &serverResetLifecycle{server: server}
}

func (l *serverResetLifecycle) TryFence(ctx context.Context) error {
	if l == nil || l.server == nil || l.server.resetWork == nil {
		return resetstate.ErrWorkUntracked
	}
	return l.server.resetWork.TryFence(ctx)
}

// Drain runs only after TryFence atomically excluded new finite work. Background
// loops can therefore stop without cancelling a user operation. Long-lived
// processes/channels are stopped explicitly; an ambiguous stop leaves its owner
// count and fails verification. Session/cache closure is reset-only because a
// normal menubar Stop must keep those stores reusable.
func (l *serverResetLifecycle) Drain(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, resetDrainTimeout)
	defer cancel()
	if l == nil || l.server == nil || l.server.resetWork == nil {
		return resetstate.ErrWorkUntracked
	}
	snapshot := l.server.resetWork.Snapshot()
	if !snapshot.Fenced || snapshot.Active != 0 {
		return errResetDrainNotFenced
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	var drainErr error
	drainErr = errors.Join(drainErr, l.server.shutdownBackground(ctx))
	if l.server.Integration != nil && l.server.Integration.MCPRegistry != nil {
		drainErr = errors.Join(drainErr, l.server.Integration.MCPRegistry.StopAll())
	}

	l.server.resetCloseOnce.Do(func() {
		// Stop the folder workspace owner before closing the shared SQLite
		// session store: its final index flush may still consult composed state.
		if l.server.workspaceFileStore != nil {
			l.server.resetCloseErr = errors.Join(l.server.resetCloseErr, l.server.workspaceFileStore.Close())
		}
		if l.server.Core != nil && l.server.Core.CostTracker != nil {
			l.server.Core.CostTracker.Close()
		}
		if l.server.Storage != nil && l.server.Storage.SessionStore != nil {
			l.server.resetCloseErr = errors.Join(l.server.resetCloseErr, l.server.Storage.SessionStore.Close())
		}
	})
	drainErr = errors.Join(drainErr, l.server.resetCloseErr)

	// A channel's Stop may return just before its Start loop unwinds and drops
	// the lifetime permit. Wait boundedly for that ownership proof; no data is
	// deleted while it remains ambiguous.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for l.server.resetWork.Snapshot().Owners != 0 {
		select {
		case <-ctx.Done():
			return errors.Join(drainErr, ctx.Err(), errResetOwnersRemain)
		case <-ticker.C:
		}
	}
	return drainErr
}
