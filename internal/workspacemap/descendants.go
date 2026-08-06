package workspacemap

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspaceLister enumerates the workspaces a cluster move might contain.
//
// Read-only, like WorkspaceLookup: resolving who moves with a district must
// never be able to change who belongs to it (FR-8).
type WorkspaceLister interface {
	ListActive() ([]*workspace.Workspace, error)
}

// StoreDescendantResolver answers "which anchors move with this district?" from
// the live workspace hierarchy.
//
// It is deliberately server-side. The browser knows which buildings it drew
// inside an outline, but a cluster move commits against whatever the hierarchy
// says at commit time — so a workspace reparented in Tree a moment ago moves
// with the district it is actually in, not the one a stale tab drew it in
// (FR-86, FR-88).
type StoreDescendantResolver struct {
	lister WorkspaceLister
}

// NewDescendantResolver builds a resolver over the composed workspace store.
func NewDescendantResolver(lister WorkspaceLister) *StoreDescendantResolver {
	return &StoreDescendantResolver{lister: lister}
}

// GroupNodeIDs returns the group followed by every active workspace beneath it,
// at any depth.
//
// Depth is flattened on purpose: V1 draws one district per top-level group and
// renders deeper members inside it, so a cluster move has to carry them too or
// the district would visibly leave part of itself behind (FR-92).
func (r *StoreDescendantResolver) GroupNodeIDs(_ context.Context, groupID string) ([]string, error) {
	if r == nil || r.lister == nil {
		return nil, ErrGroupResolverUnavailable
	}
	groupID, err := NormalizeNodeID(groupID)
	if err != nil {
		return nil, err
	}
	workspaces, err := r.lister.ListActive()
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces for group %q: %w", groupID, err)
	}

	childrenByParent := make(map[string][]string, len(workspaces))
	known := make(map[string]bool, len(workspaces))
	for _, ws := range workspaces {
		if ws == nil || ws.ID == "" {
			continue
		}
		known[ws.ID] = true
		if ws.ParentID != "" {
			childrenByParent[ws.ParentID] = append(childrenByParent[ws.ParentID], ws.ID)
		}
	}
	if !known[groupID] {
		return nil, fmt.Errorf("%w: %q", ErrNodeNotFound, groupID)
	}

	// Breadth-first with a visited set: a hierarchy that has somehow become
	// cyclic must not hang a request.
	ids := []string{groupID}
	visited := map[string]bool{groupID: true}
	for cursor := 0; cursor < len(ids); cursor++ {
		for _, child := range childrenByParent[ids[cursor]] {
			if visited[child] {
				continue
			}
			visited[child] = true
			ids = append(ids, child)
			if len(ids) > MaxPositionsPerOperation {
				return nil, fmt.Errorf("%w: group %q has more than %d members", ErrPatchTooLarge, groupID, MaxPositionsPerOperation)
			}
		}
	}
	return ids, nil
}

var _ DescendantResolver = (*StoreDescendantResolver)(nil)
