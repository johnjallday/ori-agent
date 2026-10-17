package server

import (
	"errors"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

var errWorkspaceStoreUnavailable = errors.New("workspace store is unavailable")

// builderWorkspaces reads the builder's composed workspace store when called.
// The personal-assistant handler is wired in an earlier phase than that store,
// so capturing the store at wiring time could capture nil.
type builderWorkspaces struct{ b *ServerBuilder }

func (w builderWorkspaces) Get(id string) (*workspace.Workspace, error) {
	if w.b == nil || w.b.workspaceStore == nil {
		return nil, errWorkspaceStoreUnavailable
	}
	return w.b.workspaceStore.Get(id)
}
