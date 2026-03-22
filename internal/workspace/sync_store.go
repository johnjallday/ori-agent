package workspace

import (
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
