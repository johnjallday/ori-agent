package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

var ErrSymbolicScopeUnavailable = errors.New("plugin symbolic scope is unavailable")

type SymbolicScopeResolver interface {
	Resolve(context.Context, workspacesurface.WorkspaceContext, []string) (runtimecapability.CapabilityExecutionScope, error)
}

// HostSymbolicScopeResolver maps the closed manifest vocabulary onto canonical
// host-owned roots/posture. It never accepts a raw path or endpoint from a
// manifest, workspace, frame, or service response.
type HostSymbolicScopeResolver struct{}

func (HostSymbolicScopeResolver) Resolve(_ context.Context, workspace workspacesurface.WorkspaceContext, symbols []string) (runtimecapability.CapabilityExecutionScope, error) {
	result := runtimecapability.CapabilityExecutionScope{NetworkPosture: runtimecapability.CapabilityNetworkDisabled}
	writable := make(map[string]struct{})
	for _, raw := range symbols {
		symbol := strings.ToLower(strings.TrimSpace(raw))
		switch symbol {
		case "workspace_project_read":
			if _, err := trustedScopeRoot(workspace.WorkspaceRoot, false); err != nil {
				return runtimecapability.CapabilityExecutionScope{}, err
			}
		case "workspace_project_write":
			root, err := trustedScopeRoot(workspace.WorkspaceRoot, false)
			if err != nil {
				return runtimecapability.CapabilityExecutionScope{}, err
			}
			writable[root] = struct{}{}
		case "plugin_data_write":
			root, err := trustedScopeRoot(workspace.PluginDataRoot, true)
			if err != nil {
				return runtimecapability.CapabilityExecutionScope{}, err
			}
			writable[root] = struct{}{}
		case "loopback_reaper":
			result.NetworkPosture = runtimecapability.CapabilityNetworkLocal
		case "reaper_runner_exchange":
			// This requires a separately configured host-owned exchange root. The
			// generic resolver has none and fails closed rather than deriving one
			// from plugin/workspace/service text.
			return runtimecapability.CapabilityExecutionScope{}, ErrSymbolicScopeUnavailable
		default:
			return runtimecapability.CapabilityExecutionScope{}, ErrSymbolicScopeUnavailable
		}
	}
	for root := range writable {
		result.AdditionalWritableRoots = append(result.AdditionalWritableRoots, root)
	}
	sort.Strings(result.AdditionalWritableRoots)
	return result, nil
}

func trustedScopeRoot(root string, create bool) (string, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." || !filepath.IsAbs(root) {
		return "", ErrSymbolicScopeUnavailable
	}
	if create {
		if err := os.MkdirAll(root, 0o750); err != nil {
			return "", ErrSymbolicScopeUnavailable
		}
	}
	info, err := os.Lstat(root) // #nosec G304 -- root comes only from the host's canonical workspace context
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrSymbolicScopeUnavailable
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || !filepath.IsAbs(resolved) {
		return "", ErrSymbolicScopeUnavailable
	}
	return filepath.Clean(resolved), nil
}
