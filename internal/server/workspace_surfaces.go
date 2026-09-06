package server

import (
	"context"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacedashboard"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
	"github.com/johnjallday/ori-agent/internal/workspacesurfacehttp"
)

func trustedWorkspaceProjectEntry(root string, ws *workspace.Workspace) string {
	resolved, err := workspace.ResolveProjectEntry(ws, root)
	if err != nil {
		return ""
	}
	return resolved.AbsolutePath
}

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
		projectEntry := ""
		if b.workspaceFileStore != nil {
			if resolved, err := b.workspaceFileStore.GetFolderPath(workspaceID); err == nil {
				root = filepath.Clean(resolved)
			}
		}
		var projectWorkspace *workspace.Workspace
		if b.workspaceStore != nil {
			projectWorkspace, _ = b.workspaceStore.Get(workspaceID)
		}
		// Project files and their containing root are one authority. Prefer the
		// same FileStore snapshot that supplied root so an eventually consistent
		// catalog/cache cannot erase freshly-created template provenance from a
		// service invocation context.
		if b.workspaceFileStore != nil {
			if stored, err := b.workspaceFileStore.Get(workspaceID); err == nil && stored != nil {
				projectWorkspace = stored
			}
		}
		projectEntry = trustedWorkspaceProjectEntry(root, projectWorkspace)
		return workspacesurface.WorkspaceContext{
			WorkspaceID: workspaceID, WorkspaceRoot: root, ProjectEntry: projectEntry,
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

	// User-authored dashboards resolve from each workspace's own folder. They
	// never enter the registry above, so a workspace without one is unaffected.
	//
	// The runtime reads through the same narrow note/session summary adapter the
	// task prompt uses, which returns identity and titles rather than bodies or
	// transcripts. Nothing here can reach the vault or agent prompts.
	if b.workspaceFileStore != nil {
		var (
			notes    workspacedashboard.NoteReader
			sessions workspacedashboard.SessionReader
		)
		if b.sessionStore != nil {
			adapter := session.NewWorkspaceTaskContextAdapter(b.sessionStore)
			notes, sessions = adapter, adapter
		}
		b.workspaceSurfaceHandler.SetDashboardSource(workspacedashboard.NewSource(
			workspace.NewDashboardStore(b.workspaceFileStore),
			workspacedashboard.NewRuntime(b.workspaceStore, notes, sessions),
		))
	}

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
