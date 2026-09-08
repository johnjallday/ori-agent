package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/settingsreset"
)

// Resolve through actual runtime owners after all builder phases have finished.
// No new store is constructed for preview, and no provider/secret values are
// read. Missing owners remain nil rather than falling back to conventional paths.
func (b *ServerBuilder) resetPreviewOwners() settingsreset.Owners {
	owners := settingsreset.Owners{
		Config: b.configManager, Agents: b.st,
		Setup: b.onboardingMgr, Uploads: b.sessionFilesStore,
		Workspaces: b.workspaceFileStore, Allowlist: b.workspaceAllowlist, Vaults: b.vaultStore,
	}
	if b.resetHandler != nil {
		owners.DataDir = b.resetHandler.DataDir()
	}
	if b.sessionStore != nil {
		owners.Database = b.sessionStore.DB()
	}
	// Readiness is a capability declaration, not sampled permission to reset.
	// TryFence remains the atomic active-work decision at execution time.
	if b.resetLease != nil && b.resetWork == b.resetLease.WorkGate() {
		owners.CheckLifecycle = func(context.Context) []settingsreset.Blocker {
			snapshot := b.resetWork.Snapshot()
			if !snapshot.Known {
				return []settingsreset.Blocker{{Code: "lifecycle_unavailable", Message: "Runtime work ownership is unavailable.", Recovery: "Fully relaunch Ori using the owned installation."}}
			}
			// Fenced is intentionally not a preview fact: the coordinator
			// revalidates the same plan immediately after its own atomic fence.
			// Competing operations are rejected by the durable journal.
			return nil
		}
	}
	return owners
}

func (b *ServerBuilder) initializeResetCoordinator() {
	if b.resetLease == nil || b.resetPlanner == nil || b.resetHandler == nil || b.resetWork != b.resetLease.WorkGate() {
		return
	}
	// The lifecycle can become destructive only through the coordinator, after
	// a bounded reviewed plan, atomic non-cancelling fence, and verified drain.
	b.resetHandler.SetCoordinator(settingsreset.NewCoordinator(
		b.resetLease,
		b.resetPlanner,
		newServerResetLifecycle(b.server),
	))
}
