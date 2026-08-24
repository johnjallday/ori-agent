package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
	"github.com/johnjallday/ori-agent/internal/workspacesurfacedemo"
	"github.com/johnjallday/ori-agent/internal/workspacesurfacehttp"
)

const (
	workspaceSurfaceDemoEnv     = "ORI_WORKSPACE_SURFACE_DEMO"
	workspaceSurfaceDemoRootEnv = "ORI_WORKSPACE_SURFACE_DEMO_ROOT"
)

// wireWorkspaceSurfaces constructs one process-wide registry and one generic
// authenticated HTTP boundary. Production plugin lifecycle registration lands
// in Group 2; the opt-in demo contribution exercises these same seams without a
// plugin-specific route or router branch.
func (b *ServerBuilder) wireWorkspaceSurfaces() {
	if b == nil || b.workspaceStore == nil {
		return
	}
	registry := workspacesurface.NewRegistry()
	demoEnabled := strings.TrimSpace(os.Getenv(workspaceSurfaceDemoEnv)) == "1"
	if demoEnabled {
		root := strings.TrimSpace(os.Getenv(workspaceSurfaceDemoRootEnv))
		absolute, err := filepath.Abs(root)
		if err != nil || root == "" {
			logger.Warn("Workspace Surface demo fixture was not registered: asset root is invalid", logger.Fields{})
			demoEnabled = false
		} else {
			runtime := &workspacesurfacedemo.Runtime{}
			if err := registry.RegisterTrusted(workspacesurfacedemo.Registration(filepath.Clean(absolute), runtime)); err != nil {
				logger.Warn("Workspace Surface demo fixture was not registered", logger.Fields{"error": err.Error()})
				demoEnabled = false
			}
		}
	}

	attachments := workspacesurfacehttp.AttachmentCheckerFunc(func(_ context.Context, _ string, surface workspacesurface.RegisteredSurface) bool {
		return demoEnabled && surface.Owner.Kind == workspacesurface.OwnerPlugin && surface.Owner.ID == workspacesurfacedemo.PluginID
	})
	contexts := workspacesurfacehttp.ContextResolverFunc(func(_ context.Context, workspaceID string, _ workspacesurface.RegisteredSurface) (workspacesurface.WorkspaceContext, error) {
		root := ""
		if b.workspaceFileStore != nil {
			if resolved, err := b.workspaceFileStore.GetFolderPath(workspaceID); err == nil {
				root = filepath.Clean(resolved)
			}
		}
		return workspacesurface.WorkspaceContext{
			WorkspaceID:    workspaceID,
			WorkspaceRoot:  root,
			PluginDataRoot: filepath.Join(config.DefaultDataDir(), "plugins", "state", workspacesurfacedemo.PluginID),
		}, nil
	})
	b.workspaceSurfaceRegistry = registry
	b.workspaceSurfaceHandler = workspacesurfacehttp.NewHandler(registry, b.workspaceStore, b.userProvider, attachments, contexts)
}
