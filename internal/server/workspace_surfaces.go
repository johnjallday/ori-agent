package server

import (
	"context"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
	"github.com/johnjallday/ori-agent/internal/workspacesurfacehttp"
)

// wireWorkspaceSurfaces constructs one process-wide registry and one generic
// authenticated HTTP boundary. Contributions enter only through the installed
// plugin lifecycle; the server has no plugin-specific fixture or route branch.
func (b *ServerBuilder) wireWorkspaceSurfaces() {
	if b == nil || b.workspaceStore == nil {
		return
	}
	registry := workspacesurface.NewRegistry()
	services := workspacesurface.NewServiceManager(nil)

	attachments := workspacesurfacehttp.AttachmentCheckerFunc(func(_ context.Context, workspaceID string, surface workspacesurface.RegisteredSurface) bool {
		if b.pluginHandler == nil || surface.Owner.Kind != workspacesurface.OwnerPlugin {
			return false
		}
		ws, err := b.workspaceStore.Get(workspaceID)
		if err != nil || ws == nil {
			return false
		}
		record, attached := ws.GetInstalledCapability(surface.Capability.ID)
		if !attached || record.Owner == nil || !record.Owner.MatchesPlugin(surface.Owner.ID) {
			return false
		}
		installed, err := b.pluginHandler.Manager().List()
		if err != nil {
			return false
		}
		for _, candidate := range installed {
			if candidate.Name == surface.Owner.ID && candidate.Enabled && candidate.Generation == surface.Owner.Generation {
				return true
			}
		}
		return false
	})
	contextForOwner := func(workspaceID, pluginID string) workspacesurface.WorkspaceContext {
		root := ""
		if b.workspaceFileStore != nil {
			if resolved, err := b.workspaceFileStore.GetFolderPath(workspaceID); err == nil {
				root = filepath.Clean(resolved)
			}
		}
		return workspacesurface.WorkspaceContext{
			WorkspaceID:    workspaceID,
			WorkspaceRoot:  root,
			PluginDataRoot: filepath.Join(config.DefaultDataDir(), "plugins", "state", pluginID),
		}
	}
	contexts := workspacesurfacehttp.ContextResolverFunc(func(_ context.Context, workspaceID string, surface workspacesurface.RegisteredSurface) (workspacesurface.WorkspaceContext, error) {
		return contextForOwner(workspaceID, surface.Owner.ID), nil
	})
	b.workspaceSurfaceRegistry = registry
	b.workspaceSurfaceServices = services
	b.workspaceSurfaceHandler = workspacesurfacehttp.NewHandler(registry, b.workspaceStore, b.userProvider, attachments, contexts)
	b.workspaceSurfaceHandler.SetAgentRuntimeService(b.runtimeCapabilityService)
	state := workspacesurface.NewStateStore(filepath.Join(config.DefaultDataDir(), "plugins", "state"))
	b.workspaceSurfaceHandler.SetStateStore(state)

	// Plugin install/update/disable/uninstall and request execution share one
	// registry and process manager. Restore trusted inert projections on startup.
	if b.pluginHandler != nil {
		lifecycle := plugin.NewSurfaceLifecycle(registry, services)
		lifecycle.SetCapabilityRegistry(b.workspaceCapabilityRegistry)
		lifecycle.SetRuntimeRegistry(b.runtimeCapabilityRegistry)
		lifecycle.SetRuntimeContextResolver(func(_ context.Context, workspaceID string, owner workspacesurface.Owner) (workspacesurface.WorkspaceContext, error) {
			return contextForOwner(workspaceID, owner.ID), nil
		})
		lifecycle.SetStateStore(state)
		lifecycle.SetSessionInvalidator(func(pluginID string, generation uint64) {
			b.workspaceSurfaceHandler.InvalidateOwner(pluginID, generation)
		})
		b.pluginHandler.Manager().SetSurfaceLifecycle(lifecycle)
		installed, err := b.pluginHandler.Manager().List()
		if err != nil {
			logger.Warn("Workspace Surface plugins could not be listed during restore", logger.Fields{"error": err.Error()})
		} else if err := lifecycle.Restore(installed); err != nil {
			logger.Warn("Workspace Surface plugin restore failed closed", logger.Fields{"error": err.Error()})
		}
	}
}
