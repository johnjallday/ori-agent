package workspace

import (
	"fmt"

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

// GetFolderPath exposes the canonical disk folder when SyncStore is used by a
// runtime handler that needs workspace-root containment rather than files/.
func (s *SyncStore) GetFolderPath(workspaceID string) (string, error) {
	if s.fileSync == nil {
		return "", fmt.Errorf("workspace folder storage is unavailable")
	}
	return s.fileSync.GetFolderPath(workspaceID)
}

// GetFolderWorkspace reads the canonical workspace.json record rather than
// the SQLite-primary mirror returned by Get.
func (s *SyncStore) GetFolderWorkspace(workspaceID string) (*Workspace, error) {
	if s.fileSync == nil {
		return nil, fmt.Errorf("workspace folder storage is unavailable")
	}
	return s.fileSync.Get(workspaceID)
}

// Save persists the workspace.
//
// The FileStore runs first because it owns the monotonic Version counter and
// performs the lost-write detection check. Bumping there before the primary
// write means the SQLite row records the post-bump version, so a subsequent
// Get -> Save cycle reads back the same version that's on disk and the CAS
// check stays meaningful. If the FileStore write fails (e.g. slug conflict)
// we still persist to the primary so the authoritative record is updated;
// the disk sync is best-effort.
func (s *SyncStore) Save(ws *Workspace) error {
	if s.fileSync != nil && ws != nil && ws.Status != StatusTrashed && ws.Status != StatusMissing {
		// ProjectPath, Designation, TemplateProvenance, and SetupWizardProgress
		// are canonical workspace.json fields not represented by the SQLite
		// workspace table. A workspace fetched from SQLite (the primary store's
		// Get) therefore always carries these as zero values, and must not erase
		// values written directly to the folder store by an unrelated
		// Update/Save cycle (e.g. the template-setup first-open auto-start saving
		// a task status change would otherwise silently wipe out the template
		// provenance persisted moments earlier at workspace-creation time — or,
		// for setup progress, hand the user a wizard that has forgotten the
		// folder they just approved). There is no generic "empty means clear"
		// operation through SyncStore; intentional removals must update the
		// canonical FileStore explicitly.
		//
		// InstalledCapabilities joins them for a different reason: it IS mirrored
		// into SQLite, but several read paths still yield a partial workspace
		// (a legacy row written before the column existed, a record built by a
		// caller that never loaded it). Restoring it from disk is what makes
		// FR-144 hold — "a stale workspace snapshot must not silently erase a
		// capability install".
		//
		// Unlike the fields above, though, this one has a legitimate empty
		// value: uninstall. capabilitiesExplicit distinguishes the two, so an
		// intentional removal writes through while an incidental absence is
		// refilled — which is why removal does NOT need to bypass SyncStore.
		if ws.ProjectPath == "" || ws.Designation == "" || ws.TemplateProvenance == nil || ws.SetupWizardProgress == nil || capabilitiesMissing(ws) {
			if diskWorkspace, err := s.fileSync.Get(ws.ID); err == nil && diskWorkspace != nil {
				if ws.ProjectPath == "" {
					ws.ProjectPath = diskWorkspace.ProjectPath
				}
				if ws.Designation == "" {
					ws.Designation = diskWorkspace.Designation
				}
				if ws.TemplateProvenance == nil {
					ws.TemplateProvenance = diskWorkspace.TemplateProvenance
				}
				if ws.SetupWizardProgress == nil {
					ws.SetupWizardProgress = diskWorkspace.SetupWizardProgress
				}
				if capabilitiesMissing(ws) {
					ws.InstalledCapabilities = CloneInstalledCapabilities(diskWorkspace.InstalledCapabilities)
				}
			}
		}
		if err := s.fileSync.Save(ws); err != nil {
			logger.Warn("Failed to sync workspace to disk", logger.Fields{
				"workspace_id": ws.ID,
				"error":        err,
			})
		}
	}
	return s.primary.Save(ws)
}

// capabilitiesMissing reports whether ws carries no capability records AND did
// not mean to. An empty collection that was deliberately written (an uninstall,
// or a rollback of a failed install) is a real value and must not be refilled
// from disk.
func capabilitiesMissing(ws *Workspace) bool {
	return len(ws.InstalledCapabilities) == 0 && !ws.InstalledCapabilitiesExplicit()
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

// ListActiveForScheduling returns active workspaces for the scheduler, delegating
// to the primary store's lighter (Messages-omitting) scan when it supports one and
// falling back to the full ListActive otherwise.
func (s *SyncStore) ListActiveForScheduling() ([]*Workspace, error) {
	if sl, ok := s.primary.(schedulingLister); ok {
		return sl.ListActiveForScheduling()
	}
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

// GetOutputsPath returns the outputs path from the FileStore so auto-saved
// task results go to the correct workspace folder on disk.
func (s *SyncStore) GetOutputsPath(workspaceID string) string {
	if s.fileSync != nil {
		return s.fileSync.GetOutputsPath(workspaceID)
	}
	return s.primary.GetOutputsPath(workspaceID)
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
	if ws, err := s.primary.Get(workspaceID); err == nil && ws != nil && (ws.Status == StatusTrashed || ws.Status == StatusMissing) {
		return nil
	}
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
