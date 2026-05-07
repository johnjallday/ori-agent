package workspace

import (
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// SyncStore wraps a primary Store and writes through to a FileStore
// so that workspace.json on disk stays in sync with the primary store.
// Reads are served from the primary; the FileStore is a sync target only.
type SyncStore struct {
	primary  Store
	fileSync *FileStore
}

// NewSyncStore creates a store that writes to both the primary store
// and the file-based store. The primary store is authoritative for reads;
// the file store provides portable workspace folders on disk.
func NewSyncStore(primary Store, fileSync *FileStore) *SyncStore {
	return &SyncStore{primary: primary, fileSync: fileSync}
}

// FileStore returns the underlying FileStore used for disk sync.
func (s *SyncStore) FileStore() *FileStore {
	return s.fileSync
}

// Save persists to the primary store, then syncs workspace.json to disk.
func (s *SyncStore) Save(ws *Workspace) error {
	if err := s.primary.Save(ws); err != nil {
		return err
	}
	if s.fileSync != nil {
		if err := s.fileSync.Save(ws); err != nil {
			logger.Warn("Failed to sync workspace to disk", logger.Fields{
				"workspace_id": ws.ID,
				"error":        err,
			})
		}
	}
	return nil
}

// Get retrieves a workspace from the primary store.
func (s *SyncStore) Get(id string) (*Workspace, error) {
	return s.primary.Get(id)
}

// List returns all workspace IDs from the primary store.
func (s *SyncStore) List() ([]string, error) {
	return s.primary.List()
}

// Delete removes a workspace from the primary store and the disk folder.
func (s *SyncStore) Delete(id string) error {
	err := s.primary.Delete(id)
	if s.fileSync != nil {
		if delErr := s.fileSync.Delete(id); delErr != nil {
			logger.Debug("FileStore delete during sync (may not exist on disk)", logger.Fields{
				"workspace_id": id,
				"error":        delErr,
			})
		}
	}
	return err
}

// ListActive returns all active workspaces from the primary store.
func (s *SyncStore) ListActive() ([]*Workspace, error) {
	return s.primary.ListActive()
}

// GetFilesPath returns the files path from the FileStore so uploads
// go to the correct workspace folder on disk.
func (s *SyncStore) GetFilesPath(workspaceID string) string {
	if s.fileSync != nil {
		return s.fileSync.GetFilesPath(workspaceID)
	}
	return s.primary.GetFilesPath(workspaceID)
}

// GetWorkspaceAgent reads a workspace-local agent snapshot. Reads prefer the
// FileStore (which holds the on-disk snapshot) so an imported workspace folder
// can resolve its entry agent before the primary store is hydrated.
func (s *SyncStore) GetWorkspaceAgent(workspaceID, agentName string) (*agent.Agent, bool, error) {
	if s.fileSync != nil {
		if ag, ok, err := s.fileSync.GetWorkspaceAgent(workspaceID, agentName); err == nil && ok {
			return ag, true, nil
		} else if err != nil {
			logger.Debug("FileStore GetWorkspaceAgent failed, falling back to primary", logger.Fields{
				"workspace_id": workspaceID,
				"agent":        agentName,
				"error":        err,
			})
		}
	}
	return s.primary.GetWorkspaceAgent(workspaceID, agentName)
}

// Lock delegates per-workspace serialization to the primary store so that
// Update calls remain atomic regardless of which Store value the caller holds.
func (s *SyncStore) Lock(wsID string) func() { return s.primary.Lock(wsID) }

// Update applies fn under the primary's per-workspace lock, then routes the
// resulting Save through SyncStore.Save (which writes to both primary and the
// disk sync target).
func (s *SyncStore) Update(wsID string, fn func(*Workspace) error) error {
	return CanonicalUpdate(s, wsID, fn)
}

// SaveWorkspaceAgent writes the snapshot to the primary store and to disk.
func (s *SyncStore) SaveWorkspaceAgent(workspaceID, agentName string, ag *agent.Agent) error {
	if err := s.primary.SaveWorkspaceAgent(workspaceID, agentName, ag); err != nil {
		return err
	}
	if s.fileSync != nil {
		if err := s.fileSync.SaveWorkspaceAgent(workspaceID, agentName, ag); err != nil {
			logger.Warn("Failed to sync workspace agent to disk", logger.Fields{
				"workspace_id": workspaceID,
				"agent":        agentName,
				"error":        err,
			})
		}
	}
	return nil
}
