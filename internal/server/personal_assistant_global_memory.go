package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// builderGlobalMemory is the Memory tab's seam to global memory. It reads the
// lifecycle service when called: the memory handler is built in a phase that
// can run before the personal-assistant services exist.
type builderGlobalMemory struct{ b *ServerBuilder }

func (g builderGlobalMemory) Workspace(ctx context.Context) (string, error) {
	if g.b == nil || g.b.personalAssistantLearning == nil {
		return "", personalassistant.ErrGlobalMemoryUnavailable
	}
	return g.b.personalAssistantLearning.GlobalMemoryWorkspace(ctx, userprofile.LocalUserID)
}

func (g builderGlobalMemory) Promote(ctx context.Context, req personalassistant.WorkspacePromoteRequest) (personalassistant.WorkspacePromoteResult, personalassistant.KnowledgeItem, error) {
	if g.b == nil || g.b.personalAssistantLearning == nil {
		return "", personalassistant.KnowledgeItem{}, personalassistant.ErrGlobalMemoryUnavailable
	}
	return g.b.personalAssistantLearning.PromoteWorkspaceEntry(ctx, userprofile.LocalUserID, req)
}

// PromoteExternal is Promote to global for an external memory mirror entry.
func (g builderGlobalMemory) PromoteExternal(ctx context.Context, req personalassistant.ExternalPromoteRequest) (personalassistant.WorkspacePromoteResult, personalassistant.KnowledgeItem, error) {
	if g.b == nil || g.b.personalAssistantLearning == nil {
		return "", personalassistant.KnowledgeItem{}, personalassistant.ErrGlobalMemoryUnavailable
	}
	return g.b.personalAssistantLearning.PromoteExternalEntry(ctx, userprofile.LocalUserID, req)
}

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

func (w builderWorkspaces) ListActive() ([]*workspace.Workspace, error) {
	if w.b == nil || w.b.workspaceStore == nil {
		return nil, errWorkspaceStoreUnavailable
	}
	return w.b.workspaceStore.ListActive()
}
