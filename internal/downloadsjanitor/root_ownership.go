package downloadsjanitor

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// WorkspaceLister enumerates every workspace so the service can answer "is any
// other workspace already managing this folder?" (FR-49).
//
// It is an optional store capability rather than part of WorkspaceStore: a
// store that cannot list simply cannot answer the question, and the service
// degrades to allowing the setup rather than refusing every one.
type WorkspaceLister interface {
	List() ([]string, error)
}

// RootOwner identifies the workspace currently managing a folder.
type RootOwner struct {
	WorkspaceID   string
	WorkspaceName string
	Root          string
}

// findConflictingOwner returns the workspace already managing a folder that
// overlaps root, if any.
//
// Overlap means exact match, ancestor, or descendant (FR-49): two File Janitors
// managing nested folders would race to propose and act on the same files, and
// the action journal could no longer say which install owned an outcome.
//
// Only ACTIVE installs count. A workspace that revoked access, or whose
// capability was removed, keeps its retained audit state but releases the
// folder — so its old root must not block a new setup.
func (s *Service) findConflictingOwner(requestingWorkspaceID, root string) (*RootOwner, bool) {
	if s == nil || strings.TrimSpace(root) == "" {
		return nil, false
	}
	lister, ok := s.workspaces.(WorkspaceLister)
	if !ok {
		// The store cannot enumerate workspaces. Refusing every setup on that
		// basis would be worse than the race it prevents, so the check is
		// skipped and the (rare, local, single-user) conflict stays possible.
		return nil, false
	}
	ids, err := lister.List()
	if err != nil {
		logger.Warn("File Janitor could not check folder ownership across workspaces", logger.Fields{
			"error": err.Error(),
		})
		return nil, false
	}

	requesting := strings.TrimSpace(requestingWorkspaceID)
	for _, id := range ids {
		if strings.EqualFold(strings.TrimSpace(id), requesting) {
			continue
		}
		settings, err := s.store.LoadSettings(id)
		if err != nil {
			// An unreadable neighbor is not evidence of a conflict. Blocking on
			// it would let one broken workspace prevent every future setup.
			continue
		}
		if !settings.IsSetUp() {
			continue
		}
		if !RootsOverlap(settings.RootPath, root) {
			continue
		}
		owner := &RootOwner{WorkspaceID: id, Root: settings.RootPath}
		if ws, err := s.readWorkspace(id); err == nil && ws != nil {
			owner.WorkspaceName = ws.Name
		}
		return owner, true
	}
	return nil, false
}

// ensureRootAvailable refuses a folder another workspace already manages.
//
// It runs at setup and relink, before any grant is recorded, so a rejected
// selection leaves no directory reference, binding, watcher, or schedule
// behind.
func (s *Service) ensureRootAvailable(workspaceID, root string) error {
	owner, conflicted := s.findConflictingOwner(workspaceID, root)
	if !conflicted {
		return nil
	}
	return conflictError(owner.WorkspaceName, owner.WorkspaceID)
}

// currentRootID returns the workspace's current managed-folder generation id,
// used to stamp journal entries that are written outside the main apply path.
// An unreadable settings record yields "", which reads as "the current root" —
// the same tolerance applied to entries written before RootID existed.
func (s *Service) currentRootID(workspaceID string) string {
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return ""
	}
	return settings.RootID
}

// Compile-time reminder that the workspace store type is the one this file
// type-asserts against.
var _ = (*workspace.Workspace)(nil)
