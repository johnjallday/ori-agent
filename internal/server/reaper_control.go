package server

import (
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/reaper"
	"github.com/johnjallday/ori-agent/internal/reaperhttp"
	"github.com/johnjallday/ori-agent/internal/reapersetup"
)

// wireReaperControl constructs the live, read-only REAPER state surface after
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
	client := reaper.NewClient(reapersetup.NewPlatformProbeSet(roots))
	b.reaperHandler = reaperhttp.NewHandler(store, b.userProvider, client)
}
