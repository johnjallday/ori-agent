package namesearch

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspaceBacked adapts the workspace store to the agent and file sources. It
// only reads: Get, the two folder paths, and the workspace-local agent snapshot.
type WorkspaceBacked struct {
	Store workspace.Store
}

var (
	_ AgentSource = WorkspaceBacked{}
	_ FileSource  = WorkspaceBacked{}
)

// AgentNames lists the agents a workspace's Agents section shows: the ones it
// references that also have a workspace-local snapshot, as the tree's own list
// does.
func (w WorkspaceBacked) AgentNames(workspaceID string) ([]string, error) {
	if w.Store == nil {
		return nil, errors.New("workspace store unavailable")
	}
	ws, err := w.Store.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, name := range workspace.ReferencedAgentNames(ws) {
		agent, ok, err := w.Store.GetWorkspaceAgent(workspaceID, name)
		if err != nil || !ok || agent == nil {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// WalkFileNames walks one section of a workspace's stored files.
func (w WorkspaceBacked) WalkFileNames(ctx context.Context, workspaceID string, section FileSection, limits FileLimits, visit func(FileEntry)) (int, WalkStop, error) {
	if w.Store == nil {
		return 0, WalkComplete, errors.New("workspace store unavailable")
	}
	ws, err := w.Store.Get(workspaceID)
	if err != nil {
		return 0, WalkComplete, err
	}
	root, kind := w.Store.GetFilesPath(workspaceID), workspace.NameWalkFiles
	if section == SectionOutputs {
		root, kind = w.Store.GetOutputsPath(workspaceID), workspace.NameWalkOutputs
	}
	visited, stop, err := workspace.WalkFileNames(ctx, ws, root, kind,
		workspace.NameWalkLimits{MaxEntries: limits.MaxEntries, MaxDepth: limits.MaxDepth},
		func(e workspace.NameWalkEntry) {
			visit(FileEntry{RelativePath: e.RelativePath, Name: e.Name, Size: e.Size, ModTime: e.ModTime})
		})
	return visited, WalkStop(stop), err
}
