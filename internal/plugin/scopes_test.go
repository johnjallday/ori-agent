package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

func TestHostSymbolicScopeResolverUsesOnlyCanonicalHostRoots(t *testing.T) {
	workspaceRoot := t.TempDir()
	pluginRoot := filepath.Join(t.TempDir(), "plugin-data")
	scope, err := (HostSymbolicScopeResolver{}).Resolve(context.Background(), workspacesurface.WorkspaceContext{
		WorkspaceID: "workspace-a", WorkspaceRoot: workspaceRoot, PluginDataRoot: pluginRoot,
	}, []string{"workspace_project_read", "workspace_project_write", "plugin_data_write"})
	if err != nil {
		t.Fatal(err)
	}
	if scope.NetworkPosture != runtimecapability.CapabilityNetworkDisabled || len(scope.AdditionalWritableRoots) != 2 {
		t.Fatalf("scope = %+v", scope)
	}
	resolvedWorkspace, _ := filepath.EvalSymlinks(workspaceRoot)
	resolvedPlugin, _ := filepath.EvalSymlinks(pluginRoot)
	for _, root := range scope.AdditionalWritableRoots {
		if root != resolvedWorkspace && root != resolvedPlugin {
			t.Fatalf("untrusted writable root escaped resolver: %q", root)
		}
	}
}

func TestHostSymbolicScopeResolverRejectsRawUnknownAndSymlinkRoots(t *testing.T) {
	workspaceRoot := t.TempDir()
	contextValue := workspacesurface.WorkspaceContext{
		WorkspaceID: "workspace-a", WorkspaceRoot: workspaceRoot, PluginDataRoot: filepath.Join(t.TempDir(), "data"),
	}
	for _, symbol := range []string{"/tmp/write-here", "https://example.test", "workspace_broadened_write", "external_exchange_write"} {
		if _, err := (HostSymbolicScopeResolver{}).Resolve(context.Background(), contextValue, []string{symbol}); err == nil {
			t.Fatalf("scope %q was accepted", symbol)
		}
	}
	link := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(workspaceRoot, link); err != nil {
		t.Fatal(err)
	}
	contextValue.WorkspaceRoot = link
	if _, err := (HostSymbolicScopeResolver{}).Resolve(context.Background(), contextValue, []string{"workspace_project_write"}); err == nil {
		t.Fatal("symlink workspace root was accepted")
	}
}
