package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrPromoteActiveWork refuses to promote a workspace while one of its
// tickets is running or waiting for a choice.
var ErrPromoteActiveWork = errors.New("workspace: stop the running ticket before turning this workspace into a group")

// PromoteStore is what promoting needs from a store. The SyncStore writes
// both workspace.json and the session store in one Update, so the kind
// change is visible to the group-parent check members are created against.
type PromoteStore interface {
	Get(id string) (*Workspace, error)
	Update(id string, fn func(*Workspace) error) error
	GetFolderPath(id string) (string, error)
}

// PromoteResult says what a promotion did. Promoted is false when the
// workspace already was a group: a second run of the same confirmed plan.
type PromoteResult struct {
	Promoted bool
}

// PromoteWorkspaceToGroup turns an existing workspace into a group in place
// (folder-agent-evolution FR35): same id, name, folder, agents and map
// position; its kind becomes group and its managed folder gets the
// sub-workspaces/ directory members live in. Nothing on disk moves and no
// agent is created: the workspace's own entry agent is the group's manager.
//
// It refuses a workspace with running work and one nested so deep that a
// member would exceed MaxNestingDepth. It is idempotent: a workspace that is
// already a group is left as it is. A crash after the directory is made and
// before the kind changes leaves an empty directory the next run reuses.
func PromoteWorkspaceToGroup(store PromoteStore, id string) (PromoteResult, error) {
	if store == nil || id == "" {
		return PromoteResult{}, errors.New("workspace: nothing to promote")
	}
	ws, err := store.Get(id)
	if err != nil || ws == nil {
		return PromoteResult{}, fmt.Errorf("workspace: %s is unavailable", id)
	}
	if ws.Kind == groupWorkspaceKind {
		return PromoteResult{}, nil
	}
	if hasActiveTicket(ws) {
		return PromoteResult{}, ErrPromoteActiveWork
	}
	if depth := promoteDepth(store, ws); depth+1 > MaxNestingDepth {
		return PromoteResult{}, ErrMaxNestingDepthExceeded
	}
	folder, err := store.GetFolderPath(id)
	if err != nil || folder == "" {
		return PromoteResult{}, fmt.Errorf("workspace: the folder of %s is unavailable", id)
	}
	if err := os.MkdirAll(filepath.Join(folder, SubWorkspacesDir), 0o750); err != nil {
		return PromoteResult{}, err
	}
	promoted := false
	err = store.Update(id, func(w *Workspace) error {
		if w.Kind == groupWorkspaceKind {
			return nil
		}
		if hasActiveTicket(w) {
			return ErrPromoteActiveWork
		}
		w.Kind = groupWorkspaceKind
		promoted = true
		return nil
	})
	if err != nil {
		return PromoteResult{}, err
	}
	return PromoteResult{Promoted: promoted}, nil
}

// hasActiveTicket is the active-work rule the regroup and delete guards use:
// a ticket running or waiting for a choice.
func hasActiveTicket(ws *Workspace) bool {
	for _, task := range ws.Tasks {
		if task.Status == TaskStatusInProgress || task.Status == TaskStatusWaitingForChoice {
			return true
		}
	}
	return false
}

// promoteDepth is the workspace's own nesting depth (1 at the top level).
func promoteDepth(store PromoteStore, ws *Workspace) int {
	depth := 1
	seen := map[string]bool{ws.ID: true}
	for parent := ws.ParentID; parent != "" && !seen[parent] && depth <= MaxNestingDepth; depth++ {
		seen[parent] = true
		next, err := store.Get(parent)
		if err != nil || next == nil {
			break
		}
		parent = next.ParentID
	}
	return depth
}
