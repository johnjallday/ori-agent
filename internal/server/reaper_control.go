package server

import (
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/reaper"
	"github.com/johnjallday/ori-agent/internal/reaperhttp"
	"github.com/johnjallday/ori-agent/internal/reapersetup"
)

// wireReaperControl constructs the live REAPER state and action surface after
// the folder-backed workspace store exists. The client owns all loopback target
// resolution; the HTTP handler receives no endpoint or port from the browser.
func (b *ServerBuilder) wireReaperControl() {
	if b == nil || b.workspaceStore == nil {
		return
	}
	store, ok := b.workspaceStore.(reaperhttp.WorkspaceStore)
	if !ok {
		logger.Warn("Live REAPER state not wired: workspace store lacks canonical folder access", logger.Fields{})
		return
	}
	roots := reapersetup.NewRunnerRootResolver()
	probes := reapersetup.NewPlatformProbeSet(roots)
	client := reaper.NewClient(probes)
	library := reaper.NewLibrary()
	catalog := reaper.NewCatalog()
	catalog.SetLibrary(library)
	runner := reaper.NewRunner(roots, probes, client)
	handler := reaperhttp.NewHandler(store, b.userProvider, client, catalog)
	handler.SetScriptServices(library, runner)
	// The same runner performs the guarded single-track edits and applied bulk
	// plans; it owns the receipt path so no filesystem detail reaches the
	// HTTP layer.
	handler.SetTrackEditRunner(runner)
	handler.SetBulkEditRunner(runner)
	b.reaperHandler = handler
}
