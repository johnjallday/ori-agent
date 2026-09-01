package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacedashboard"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
	"github.com/johnjallday/ori-agent/internal/workspacesurfacehttp"
)

func trustedWorkspaceProjectEntry(root string, ws *workspace.Workspace) string {
	if ws == nil || !filepath.IsAbs(root) {
		return ""
	}
	entry, err := workspace.GetProjectEntryPath(ws.SharedData)
	if err != nil || entry == "" {
		return ""
	}
	projectPath := filepath.Clean(strings.TrimSpace(ws.ProjectPath))
	if projectPath == "." || filepath.IsAbs(projectPath) || strings.HasPrefix(projectPath, ".."+string(filepath.Separator)) {
		return ""
	}
	candidates := []string{filepath.Join(root, projectPath, filepath.FromSlash(entry))}
	// Folder-capable stores may resolve a workspace either to its metadata
	// folder or directly to its selected project directory. The latter is
	// accepted only when the trusted root basename exactly matches ProjectPath;
	// arbitrary fallback searching remains forbidden.
	if filepath.Base(root) == filepath.Base(projectPath) {
		candidates = append(candidates, filepath.Join(root, filepath.FromSlash(entry)))
	}
	for _, candidate := range candidates {
		if trustedContainedRegularFile(root, candidate) {
			return filepath.Clean(candidate)
		}
	}
	return ""
}

func trustedContainedRegularFile(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	current := filepath.Clean(root)
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current) // #nosec G304 -- every validated relative component remains under the canonical workspace root
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	info, err := os.Lstat(candidate) // #nosec G304 -- exact contained project entry checked above
	return err == nil && info.Mode().IsRegular()
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
	if b.workspaceFileStore != nil {
		b.workspaceSurfaceHandler.SetDashboardSource(workspacedashboard.NewSource(
			workspace.NewDashboardStore(b.workspaceFileStore),
			workspacedashboard.NewRuntime(),
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
