package plugin

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// This test is the repository-local validation command documented by the
// copyable example. It exercises the same strict parser and verified artifact
// installer used by POST /api/plugins/install without mutating a user profile.
func TestWorkspaceSurfaceExampleValidatesAndInstallsArtifact(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "examples", "plugins", "workspace-surface-demo"))
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := Load(root, "", "")
	if err != nil {
		t.Fatalf("validate example contribution: %v", err)
	}
	if descriptor.Name != "workspace-surface-demo" || descriptor.WorkspaceSurfaces == nil {
		t.Fatalf("descriptor = %+v", descriptor)
	}
	artifacts, err := NewArtifactInstaller(t.TempDir()).Install(context.Background(), descriptor)
	if err != nil {
		t.Fatalf("install verified example artifact: %v", err)
	}
	if len(artifacts) != 1 || !artifacts[0].Available || artifacts[0].ManagedPath == "" {
		t.Fatalf("resolved artifacts = %+v", artifacts)
	}

	registry := workspacesurface.NewRegistry()
	services := workspacesurface.NewServiceManager(nil)
	defer func() { _ = services.Shutdown() }()
	lifecycle := NewSurfaceLifecycle(registry, services)
	installed := InstalledPlugin{
		Name: descriptor.Name, Version: descriptor.Version, InstallDir: descriptor.InstallDir,
		WorkspaceSurfaces: descriptor.WorkspaceSurfaces, ResolvedArtifacts: artifacts,
		ComponentFingerprint: trustedComponentFingerprint(descriptor), Generation: 1, Enabled: true,
	}
	if err := lifecycle.RegisterInstalled(installed); err != nil {
		t.Fatalf("register example: %v", err)
	}
	surfaces := registry.Surfaces()
	if len(surfaces) != 1 {
		t.Fatalf("registered surfaces = %+v", surfaces)
	}
	binding, ok := registry.Binding(surfaces[0].Key)
	if !ok {
		t.Fatal("example binding missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := binding.Runtime.Status(ctx, workspacesurface.WorkspaceContext{WorkspaceID: "validation"})
	if err != nil || status.State != workspacesurface.StationReady {
		t.Fatalf("example status = %+v, %v", status, err)
	}
	result, err := binding.Runtime.Invoke(ctx, workspacesurface.Invocation{
		Operation: "greeting.create", Input: []byte(`{"name":"Validation"}`),
		Workspace: workspacesurface.WorkspaceContext{WorkspaceID: "validation"},
	})
	if err != nil || string(result.Output) != `{"message":"Hello, Validation."}` {
		t.Fatalf("example greeting = %s, %v", result.Output, err)
	}
}
