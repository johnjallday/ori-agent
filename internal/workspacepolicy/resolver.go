package workspacepolicy

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

// Resolver answers "what is this workspace's effective planning policy right
// now" by combining its stored settings with what its folder actually is.
//
// Both halves are read per call rather than cached. Settings change from the
// settings screen, and the folder's branch changes from a terminal the app
// never sees; a policy computed once at startup would confidently describe a
// world that has moved.
type Resolver struct {
	store workspace.Store
}

// NewResolver returns a policy resolver over a workspace store.
func NewResolver(store workspace.Store) *Resolver {
	return &Resolver{store: store}
}

// Policy returns the effective policy and the capabilities it was computed
// from. Callers usually want the policy; the preflight adapters want both,
// because a block message names the current branch.
func (r *Resolver) Policy(_ context.Context, workspaceID string) (workspacesettings.EffectivePolicy, workspacesettings.WorkspaceCapabilities) {
	settings := workspacesettings.DefaultSettings()
	caps := workspacesettings.WorkspaceCapabilities{}

	if r != nil && r.store != nil {
		if ws, err := r.store.Get(workspaceID); err == nil && ws != nil {
			settings = workspacesettings.Extract(ws.SharedData)
			caps = Inspect(r.codeFolder(ws))
		}
	}
	return workspacesettings.BuildEffectivePolicy(settings, caps), caps
}

// Capabilities returns just what the workspace's folder supports.
func (r *Resolver) Capabilities(ctx context.Context, workspaceID string) workspacesettings.WorkspaceCapabilities {
	_, caps := r.Policy(ctx, workspaceID)
	return caps
}

// CodeFolder returns the workspace directory that contains the user's project
// files. Planning artifacts use the same root repository inspection checks, so
// an approved software Plan writes beside the code that `wt start` will read
// rather than into Ori's private workspace metadata folder.
func (r *Resolver) CodeFolder(workspaceID string) string {
	if r == nil || r.store == nil {
		return ""
	}
	ws, err := r.store.Get(workspaceID)
	if err != nil || ws == nil {
		return ""
	}
	return r.codeFolder(ws)
}

// codeFolder returns the directory whose version control matters.
//
// A workspace folder under the workspaces root is Ori's own storage; it is
// almost never a repository, and inspecting it would report "not a repository"
// for every workspace that points at real code. The project path is the folder
// the user's work actually lives in, so that is the one whose branch a branch
// precondition is about (FR-135).
func (r *Resolver) codeFolder(ws *workspace.Workspace) string {
	if ws == nil {
		return ""
	}

	if strings.TrimSpace(ws.ProjectPath) != "" {
		if info, err := projectPathInfo(r.store, ws.ID); err == nil && info != nil && info.Resolved {
			return info.AbsolutePath
		}
	}
	// Fall back to the workspace's own files root. A workspace with no project
	// path may still be a checkout somebody opened directly.
	if r.store != nil {
		return r.store.GetFilesPath(ws.ID)
	}
	return ""
}

// projectPathInfo reaches the concrete store's project-path resolution.
//
// It is a type assertion rather than a Store method because project paths are a
// FileStore concern: the in-memory store used by tests has no folders to
// resolve, and widening the interface for it would force every implementation
// to answer a question only one of them can.
func projectPathInfo(store workspace.Store, workspaceID string) (*workspace.ProjectPathInfo, error) {
	resolver, ok := store.(interface {
		GetProjectPathInfo(string) (*workspace.ProjectPathInfo, error)
	})
	if !ok {
		return nil, nil
	}
	return resolver.GetProjectPathInfo(workspaceID)
}
