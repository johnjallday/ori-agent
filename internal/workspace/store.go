package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Store manages workspace persistence and retrieval
type Store interface {
	// Save persists a workspace to storage
	Save(ws *Workspace) error

	// Get retrieves a workspace by ID
	Get(id string) (*Workspace, error)

	// List returns all workspace IDs
	List() ([]string, error)

	// Delete removes a workspace from storage
	Delete(id string) error

	// ListActive returns all active workspaces
	ListActive() ([]*Workspace, error)

	// GetFilesPath returns the path for storing files for a workspace
	GetFilesPath(workspaceID string) string
}

// FileStore implements Store using folder-based persistence.
// Each workspace is a folder: workspaces/{slug}/workspace.json
type FileStore struct {
	basePath string
	cache    map[string]*Workspace
	idToPath map[string]string // maps workspace ID → relative folder path from basePath
	index    *Index            // optional global index (nil if not configured)
	mu       sync.RWMutex
}

var ErrWorkspaceFolderSlugConflict = errors.New("workspace folder slug conflict")

// FolderSlugConflictError indicates that the requested workspace folder slug is
// already in use on disk and includes a safe alternative suggestion.
type FolderSlugConflictError struct {
	Slug          string
	SuggestedSlug string
	ParentDir     string
}

func (e *FolderSlugConflictError) Error() string {
	if e == nil {
		return ErrWorkspaceFolderSlugConflict.Error()
	}
	if e.SuggestedSlug != "" {
		return fmt.Sprintf("a workspace folder named %q already exists, suggested slug %q", e.Slug, e.SuggestedSlug)
	}
	return fmt.Sprintf("a workspace folder named %q already exists", e.Slug)
}

func (e *FolderSlugConflictError) Unwrap() error {
	return ErrWorkspaceFolderSlugConflict
}

// NewFileStore creates a new file-based workspace store
func NewFileStore(basePath string) (*FileStore, error) {
	// Ensure base directory exists
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create workspace directory: %w", err)
	}

	store := &FileStore{
		basePath: basePath,
		cache:    make(map[string]*Workspace),
		idToPath: make(map[string]string),
	}

	// Try to open the global index
	idx, err := NewIndex(basePath)
	if err != nil {
		logger.Warn("Failed to open workspace index, continuing without it", logger.Fields{"error": err.Error()})
	} else {
		store.index = idx
	}

	// Load existing workspaces into cache
	if err := store.loadCache(); err != nil {
		return nil, fmt.Errorf("failed to load workspace cache: %w", err)
	}

	// Rebuild the index from disk to ensure consistency
	if store.index != nil {
		if err := store.index.Rebuild(); err != nil {
			logger.Warn("Failed to rebuild workspace index", logger.Fields{"error": err.Error()})
		}
	}

	return store, nil
}

// Save persists a workspace to disk inside its folder.
// If the workspace has no FolderSlug, one is derived from the Name.
func (s *FileStore) Save(ws *Workspace) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure the workspace has a folder slug
	if ws.FolderSlug == "" {
		ws.FolderSlug = Slugify(ws.Name)
	}

	// Stale-write detection: if the in-memory cache holds a newer version
	// than the workspace being saved, a concurrent writer beat us to it and
	// our update is overwriting their change. Log a warning so the issue is
	// observable; full CAS rejection requires caller-side retry logic.
	if cached, ok := s.cache[ws.ID]; ok && cached.Version > ws.Version {
		logger.Warn("possible lost write: saving workspace over a newer cached version",
			logger.Fields{
				"workspace_id":     ws.ID,
				"incoming_version": ws.Version,
				"cached_version":   cached.Version,
			})
	}
	ws.Version++

	// Determine the parent directory for this workspace
	parentDir := s.basePath
	if ws.ParentID != "" {
		parentPath, ok := s.idToPath[ws.ParentID]
		if !ok {
			return fmt.Errorf("parent workspace %s not found", ws.ParentID)
		}
		parentDir = filepath.Join(s.basePath, parentPath, SubWorkspacesDir)

		// Check nesting depth
		depth := s.getNestingDepth(ws.ParentID)
		if depth >= MaxNestingDepth {
			return fmt.Errorf("maximum nesting depth of %d exceeded", MaxNestingDepth)
		}
	}

	existingPath, exists := s.idToPath[ws.ID]
	existingFolderPath := ""
	if exists {
		existingFolderPath = s.resolveFolder(existingPath)
	}

	// Default folder target is derived from the current base path and slug.
	folderPath := filepath.Join(parentDir, ws.FolderSlug)

	// Workspaces originally created with SaveAt are tracked by absolute paths.
	// Preserve that absolute location for normal metadata/content saves so we do
	// not accidentally recreate the workspace under the current base path.
	if exists && filepath.IsAbs(existingPath) {
		folderPath = existingFolderPath
	}

	// Check for folder name conflict (only for new workspaces or path changes).
	if !exists {
		// New workspace — check if folder already exists
		if existsOnDisk, err := pathExists(folderPath); err != nil {
			return fmt.Errorf("failed to check workspace folder path: %w", err)
		} else if existsOnDisk {
			return &FolderSlugConflictError{
				Slug:          ws.FolderSlug,
				SuggestedSlug: nextAvailableWorkspaceSlug(parentDir, ws.FolderSlug),
				ParentDir:     parentDir,
			}
		}
	} else if filepath.Clean(existingFolderPath) != filepath.Clean(folderPath) {
		// Existing workspace with changed path — check new folder doesn't exist
		if existsOnDisk, err := pathExists(folderPath); err != nil {
			return fmt.Errorf("failed to check workspace folder path: %w", err)
		} else if existsOnDisk {
			return &FolderSlugConflictError{
				Slug:          ws.FolderSlug,
				SuggestedSlug: nextAvailableWorkspaceSlug(parentDir, ws.FolderSlug),
				ParentDir:     parentDir,
			}
		}
	}

	// Create workspace folder with files and notes subdirectories
	if err := os.MkdirAll(filepath.Join(folderPath, FilesDir), 0755); err != nil {
		return fmt.Errorf("failed to create workspace folder: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(folderPath, NotesDir), 0755); err != nil {
		return fmt.Errorf("failed to create workspace notes folder: %w", err)
	}

	// Serialize workspace
	data, err := ws.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize workspace: %w", err)
	}

	// Write workspace.json inside the folder
	configPath := filepath.Join(folderPath, WorkspaceConfigFile)
	if err := atomicWriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write workspace file: %w", err)
	}

	// Reload from disk to ensure cache has fresh copy with all fields properly set
	freshWS, err := FromJSON(data)
	if err != nil {
		return fmt.Errorf("failed to reload workspace after save: %w", err)
	}

	// Compute relative path from basePath
	var relPath string
	if !filepath.IsAbs(folderPath) || filepath.IsAbs(existingPath) {
		relPath = folderPath
	} else if computedRelPath, err := filepath.Rel(s.basePath, folderPath); err == nil {
		relPath = computedRelPath
	} else {
		relPath = ws.FolderSlug
	}

	// Update cache and ID-to-path mapping
	s.cache[ws.ID] = freshWS
	s.idToPath[ws.ID] = relPath

	// Update the global index
	if s.index != nil {
		_ = s.index.Register(IndexEntry{
			ID:         ws.ID,
			Name:       ws.Name,
			FolderPath: relPath,
			ParentID:   ws.ParentID,
			UpdatedAt:  ws.UpdatedAt,
		})
	}

	return nil
}

// SaveAt creates a workspace folder at a custom location (outside the default root).
// The workspace is registered in the index with its absolute path.
func (s *FileStore) SaveAt(ws *Workspace, location string) error {
	if ws.FolderSlug == "" {
		ws.FolderSlug = Slugify(ws.Name)
	}

	folderPath := filepath.Join(location, ws.FolderSlug)

	// Check for conflict
	if existsOnDisk, err := pathExists(folderPath); err != nil {
		return fmt.Errorf("failed to check workspace folder path: %w", err)
	} else if existsOnDisk {
		return &FolderSlugConflictError{
			Slug:          ws.FolderSlug,
			SuggestedSlug: nextAvailableWorkspaceSlug(location, ws.FolderSlug),
			ParentDir:     location,
		}
	}

	// Create workspace folder with files and notes subdirectories
	if err := os.MkdirAll(filepath.Join(folderPath, FilesDir), 0755); err != nil {
		return fmt.Errorf("failed to create workspace folder: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(folderPath, NotesDir), 0755); err != nil {
		return fmt.Errorf("failed to create workspace notes folder: %w", err)
	}

	// Serialize and write workspace.json
	data, err := ws.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize workspace: %w", err)
	}
	configPath := filepath.Join(folderPath, WorkspaceConfigFile)
	if err := atomicWriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write workspace file: %w", err)
	}

	// Reload from disk to get clean copy
	freshWS, err := FromJSON(data)
	if err != nil {
		return fmt.Errorf("failed to reload workspace after save: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Store the absolute path for custom locations
	s.cache[ws.ID] = freshWS
	s.idToPath[ws.ID] = folderPath

	// Register in global index
	if s.index != nil {
		_ = s.index.Register(IndexEntry{
			ID:         ws.ID,
			Name:       ws.Name,
			FolderPath: folderPath,
			ParentID:   ws.ParentID,
			UpdatedAt:  ws.UpdatedAt,
		})
	}

	return nil
}

// RebindExistingFolder attaches an existing folder path to a workspace ID.
// If the folder already contains a workspace.json for the same workspace, the
// disk copy is used as a source for fields that are not mirrored into SQLite.
func (s *FileStore) RebindExistingFolder(ws *Workspace, folderPath string) error {
	if ws == nil {
		return fmt.Errorf("workspace is required")
	}
	if strings.TrimSpace(ws.ID) == "" {
		return fmt.Errorf("workspace id is required")
	}

	normalizedPath, err := filepath.Abs(strings.TrimSpace(folderPath))
	if err != nil {
		return fmt.Errorf("failed to normalize folder path: %w", err)
	}

	info, err := os.Stat(normalizedPath)
	if err != nil {
		return fmt.Errorf("failed to access workspace folder: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace folder must be a directory")
	}

	merged, err := cloneWorkspaceForRebind(ws)
	if err != nil {
		return err
	}

	configPath := filepath.Join(normalizedPath, WorkspaceConfigFile)
	if data, readErr := os.ReadFile(configPath); readErr == nil {
		diskWorkspace, parseErr := FromJSON(data)
		if parseErr != nil {
			return fmt.Errorf("failed to read existing workspace file: %w", parseErr)
		}
		if strings.TrimSpace(diskWorkspace.ID) != "" && diskWorkspace.ID != merged.ID {
			return fmt.Errorf("folder belongs to a different workspace (%s)", diskWorkspace.ID)
		}
		preserveUnmirroredWorkspaceFields(merged, diskWorkspace)
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("failed to read existing workspace file: %w", readErr)
	}

	if strings.TrimSpace(merged.FolderSlug) == "" {
		merged.FolderSlug = Slugify(filepath.Base(normalizedPath))
	}

	if err := os.MkdirAll(filepath.Join(normalizedPath, FilesDir), 0755); err != nil {
		return fmt.Errorf("failed to create workspace files folder: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(normalizedPath, NotesDir), 0755); err != nil {
		return fmt.Errorf("failed to create workspace notes folder: %w", err)
	}

	data, err := merged.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize workspace: %w", err)
	}
	if err := atomicWriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write workspace file: %w", err)
	}

	freshWS, err := FromJSON(data)
	if err != nil {
		return fmt.Errorf("failed to reload workspace after rebind: %w", err)
	}

	storedPath := normalizedPath
	if s.isInsideRoot(normalizedPath) {
		if relPath, relErr := filepath.Rel(s.basePath, normalizedPath); relErr == nil {
			storedPath = relPath
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cache[ws.ID] = freshWS
	s.idToPath[ws.ID] = storedPath

	if s.index != nil {
		_ = s.index.Register(IndexEntry{
			ID:         freshWS.ID,
			Name:       freshWS.Name,
			FolderPath: storedPath,
			ParentID:   freshWS.ParentID,
			UpdatedAt:  freshWS.UpdatedAt,
		})
	}

	return nil
}

// atomicWriteFile writes data to path via a temp file + rename so a crash
// mid-write cannot leave a truncated/corrupt file behind.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func nextAvailableWorkspaceSlug(parentDir, baseSlug string) string {
	baseSlug = Slugify(baseSlug)
	if baseSlug == "" {
		baseSlug = "untitled"
	}

	const maxAttempts = 1000
	for suffix := 2; suffix < 2+maxAttempts; suffix++ {
		candidate := appendWorkspaceSlugSuffix(baseSlug, suffix)
		existsOnDisk, err := pathExists(filepath.Join(parentDir, candidate))
		if err != nil {
			// Stat failed for a reason other than NotExist (permissions, I/O).
			// Returning the candidate would risk a collision the caller cannot
			// detect — skip and try the next suffix instead.
			logger.Warn("slug suffix stat failed, trying next",
				logger.Fields{"parent": parentDir, "candidate": candidate, "error": err})
			continue
		}
		if !existsOnDisk {
			return candidate
		}
	}

	// All numeric suffixes exhausted; use a timestamp-based fallback.
	return appendWorkspaceSlugSuffix(baseSlug, int(time.Now().UnixNano()))
}

func appendWorkspaceSlugSuffix(baseSlug string, suffix int) string {
	suffixText := fmt.Sprintf("-%d", suffix)
	maxBaseLen := MaxSlugLength - len(suffixText)
	if maxBaseLen < 1 {
		maxBaseLen = 1
	}

	trimmedBase := strings.Trim(strings.TrimSpace(baseSlug), "-")
	if len(trimmedBase) > maxBaseLen {
		trimmedBase = strings.TrimRight(trimmedBase[:maxBaseLen], "-")
	}
	if trimmedBase == "" {
		trimmedBase = "untitled"
		if len(trimmedBase) > maxBaseLen {
			trimmedBase = strings.TrimRight(trimmedBase[:maxBaseLen], "-")
		}
		if trimmedBase == "" {
			trimmedBase = "w"
		}
	}

	return trimmedBase + suffixText
}

func cloneWorkspaceForRebind(ws *Workspace) (*Workspace, error) {
	data, err := ws.ToJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to clone workspace for rebind: %w", err)
	}
	clone, err := FromJSON(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode cloned workspace for rebind: %w", err)
	}
	return clone, nil
}

func preserveUnmirroredWorkspaceFields(target *Workspace, existing *Workspace) {
	if target == nil || existing == nil {
		return
	}

	if len(target.AgentInstances) == 0 && len(existing.AgentInstances) > 0 {
		target.AgentInstances = append([]AgentInstance(nil), existing.AgentInstances...)
	}
	if len(target.Agents) == 0 && len(existing.Agents) > 0 {
		target.Agents = append([]string(nil), existing.Agents...)
	}
	target.PlannerDecision = existing.PlannerDecision
	target.PendingPlan = existing.PendingPlan

	if len(existing.DynamicAgentRequests) > 0 {
		target.DynamicAgentRequests = append([]types.DynamicAgentRequest(nil), existing.DynamicAgentRequests...)
	}
	if len(existing.SkillBindings) > 0 {
		target.SkillBindings = append([]WorkspaceSkillBinding(nil), existing.SkillBindings...)
	}
	if len(existing.AgentSkillAccess) > 0 {
		target.AgentSkillAccess = append([]WorkspaceAgentSkillAccess(nil), existing.AgentSkillAccess...)
	}
}

// BasePath returns the default workspace root directory.
func (s *FileStore) BasePath() string {
	return s.basePath
}

// Get retrieves a workspace by ID. Returns a deep clone so callers may safely
// mutate the result without affecting the cache or other concurrent callers.
func (s *FileStore) Get(id string) (*Workspace, error) {
	s.mu.RLock()

	// Check cache first
	if ws, ok := s.cache[id]; ok {
		s.mu.RUnlock()
		return cloneWorkspaceForRebind(ws)
	}

	// Check if we know the slug for this ID
	slug, ok := s.idToPath[id]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("workspace %s not found", id)
	}

	// Load from disk
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check cache after acquiring write lock
	if ws, ok := s.cache[id]; ok {
		return cloneWorkspaceForRebind(ws)
	}

	folderPath := s.resolveFolder(slug)
	configPath := filepath.Join(folderPath, WorkspaceConfigFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Folder was removed externally — clean up mappings
			delete(s.idToPath, id)
			return nil, fmt.Errorf("workspace %s not found", id)
		}
		return nil, fmt.Errorf("failed to read workspace file: %w", err)
	}

	ws, err := FromJSON(data)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize workspace: %w", err)
	}

	// Ensure slug is set
	if ws.FolderSlug == "" {
		ws.FolderSlug = slug
	}

	// Run migrations if needed
	if s.migrateIfNeeded(ws, configPath) {
		// Migration happened — persist it
		s.persistMigration(ws, configPath)
	}

	// Update cache
	s.cache[id] = ws

	return cloneWorkspaceForRebind(ws)
}

// List returns all workspace IDs from the in-memory cache.
func (s *FileStore) List() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.idToPath))
	for id := range s.idToPath {
		ids = append(ids, id)
	}
	return ids, nil
}

// Delete removes a workspace from storage by deleting the entire folder.
// This also removes all sub-workspaces (cascading delete).
// Safety: only deletes folders that are inside the workspace root.
// Folders outside the root (e.g., imported project directories) are unregistered but not deleted.
func (s *FileStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	relPath, ok := s.idToPath[id]
	if !ok {
		return fmt.Errorf("workspace %s not found", id)
	}

	folderPath := s.resolveFolder(relPath)

	// Safety check: only delete folders inside the workspace root.
	// Never delete imported/external folders (e.g., user project directories).
	if s.isInsideRoot(folderPath) {
		if err := os.RemoveAll(folderPath); err != nil {
			return fmt.Errorf("failed to delete workspace folder: %w", err)
		}
	} else {
		logger.Info("Workspace folder outside root, unregistering without deleting from disk",
			logger.Fields{"id": id, "path": folderPath})
	}

	// Remove this workspace and all children from cache and mappings
	s.removeFromCacheRecursive(id)

	return nil
}

// isInsideRoot checks whether a path is inside the workspace root directory.
func (s *FileStore) isInsideRoot(path string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absBase, err := filepath.Abs(s.basePath)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absBase, absPath)
	if err != nil {
		return false
	}
	// Must be relative (not starting with "..") and not equal to "."
	return !filepath.IsAbs(rel) && rel != "." && (len(rel) < 2 || rel[:2] != "..")
}

// removeFromCacheRecursive removes a workspace and all its children from cache/index.
// Caller must hold s.mu.
func (s *FileStore) removeFromCacheRecursive(id string) {
	// Find and remove all children first
	for childID, ws := range s.cache {
		if ws.ParentID == id {
			s.removeFromCacheRecursive(childID)
		}
	}

	delete(s.cache, id)
	delete(s.idToPath, id)

	if s.index != nil {
		_ = s.index.Unregister(id)
	}
}

// ListActive returns all active workspaces
func (s *FileStore) ListActive() ([]*Workspace, error) {
	ids, err := s.List()
	if err != nil {
		return nil, err
	}

	var active []*Workspace
	for _, id := range ids {
		ws, err := s.Get(id)
		if err != nil {
			continue // Skip workspaces that fail to load
		}
		if ws.GetStatus() == StatusActive {
			active = append(active, ws)
		}
	}

	return active, nil
}

// GetFilesPath returns the path for storing files for a workspace
func (s *FileStore) GetFilesPath(workspaceID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	slug, ok := s.idToPath[workspaceID]
	if !ok {
		// Fallback for unknown workspaces
		return filepath.Join(s.basePath, workspaceID, FilesDir)
	}
	return filepath.Join(s.resolveFolder(slug), FilesDir)
}

// Rename changes a workspace's folder name. The workspace ID is preserved.
func (s *FileStore) Rename(id, newName string) error {
	return s.RenameWithSlug(id, newName, "")
}

func (s *FileStore) RenameWithSlug(id, newName, requestedSlug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldRelPath, ok := s.idToPath[id]
	if !ok {
		return fmt.Errorf("workspace %s not found", id)
	}

	newSlug := ""
	if trimmedRequestedSlug := strings.TrimSpace(requestedSlug); trimmedRequestedSlug != "" {
		newSlug = Slugify(trimmedRequestedSlug)
	}
	if newSlug == "" {
		newSlug = Slugify(newName)
	}
	oldFolderPath := s.resolveFolder(oldRelPath)
	parentDir := filepath.Dir(oldFolderPath)

	// If the slug hasn't changed, just update the display name
	if newSlug == filepath.Base(oldFolderPath) {
		if ws, ok := s.cache[id]; ok {
			ws.Name = newName
			ws.FolderSlug = newSlug
			return s.persistWorkspaceLocked(ws)
		}
		return nil
	}

	// Check if new folder name already exists
	newFolderPath := filepath.Join(parentDir, newSlug)
	if _, err := os.Stat(newFolderPath); err == nil {
		return &FolderSlugConflictError{
			Slug:          newSlug,
			SuggestedSlug: nextAvailableWorkspaceSlug(parentDir, newSlug),
			ParentDir:     parentDir,
		}
	}

	// Rename the folder on disk
	if err := os.Rename(oldFolderPath, newFolderPath); err != nil {
		return fmt.Errorf("failed to rename workspace folder: %w", err)
	}

	// Compute new path (keep absolute if original was absolute)
	var newRelPath string
	if filepath.IsAbs(oldRelPath) {
		newRelPath = newFolderPath
	} else {
		relPath, relErr := filepath.Rel(s.basePath, newFolderPath)
		if relErr != nil {
			newRelPath = newSlug
		} else {
			newRelPath = relPath
		}
	}

	// Update the workspace metadata
	ws, ok := s.cache[id]
	if !ok {
		configPath := filepath.Join(newFolderPath, WorkspaceConfigFile)
		data, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("failed to read workspace after rename: %w", err)
		}
		ws, err = FromJSON(data)
		if err != nil {
			return fmt.Errorf("failed to deserialize workspace after rename: %w", err)
		}
	}

	ws.Name = newName
	ws.FolderSlug = newSlug

	// Update mappings first so persistWorkspaceLocked can find the path
	s.idToPath[id] = newRelPath
	s.cache[id] = ws

	// Persist updated workspace.json in new location
	if err := s.persistWorkspaceLocked(ws); err != nil {
		return err
	}

	// Update global index
	if s.index != nil {
		_ = s.index.Register(IndexEntry{
			ID:         id,
			Name:       newName,
			FolderPath: newRelPath,
			ParentID:   ws.ParentID,
			UpdatedAt:  ws.UpdatedAt,
		})
	}

	return nil
}

// Import registers an external workspace folder. If the folder is already under
// the workspaces root, it is registered in-place. Otherwise, it is copied into
// the workspaces root. Returns a warning message if project_path cannot be resolved.
func (s *FileStore) Import(folderPath string) (*Workspace, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate the folder contains a workspace.json
	configPath := filepath.Join(folderPath, WorkspaceConfigFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, "", fmt.Errorf("invalid workspace folder: %s not found", WorkspaceConfigFile)
	}

	ws, err := FromJSON(data)
	if err != nil {
		return nil, "", fmt.Errorf("invalid workspace.json: %w", err)
	}

	if ws.FolderSlug == "" {
		ws.FolderSlug = filepath.Base(folderPath)
	}

	// Determine target location
	absFolder, err := filepath.Abs(folderPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve folder path: %w", err)
	}
	absBase, err := filepath.Abs(s.basePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve base path: %w", err)
	}

	var targetPath string
	isInPlace := false

	// Check if already under workspaces root
	rel, err := filepath.Rel(absBase, absFolder)
	if err == nil && !filepath.IsAbs(rel) && len(rel) >= 2 && rel[:2] != ".." {
		// Already inside basePath — register in-place
		targetPath = absFolder
		isInPlace = true
	}

	if !isInPlace {
		// Copy into workspaces root
		targetPath = filepath.Join(s.basePath, ws.FolderSlug)

		// Check for conflict
		if _, err := os.Stat(targetPath); err == nil {
			return nil, "", fmt.Errorf("a workspace folder named %q already exists, choose a different name", ws.FolderSlug)
		}

		if err := copyDir(absFolder, targetPath); err != nil {
			return nil, "", fmt.Errorf("failed to copy workspace folder: %w", err)
		}
	}

	// Compute relative path for storage
	relPath, err := filepath.Rel(s.basePath, targetPath)
	if err != nil {
		relPath = ws.FolderSlug
	}

	// Register in cache and index
	s.cache[ws.ID] = ws
	s.idToPath[ws.ID] = relPath

	if s.index != nil {
		_ = s.index.Register(IndexEntry{
			ID:         ws.ID,
			Name:       ws.Name,
			FolderPath: relPath,
			ParentID:   ws.ParentID,
			UpdatedAt:  ws.UpdatedAt,
		})
	}

	// Import sub-workspaces recursively
	subDir := filepath.Join(targetPath, SubWorkspacesDir)
	if entries, err := os.ReadDir(subDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				subPath := filepath.Join(subDir, entry.Name())
				s.importSubWorkspace(subPath, ws.ID)
			}
		}
	}

	// Check project_path resolution
	var warning string
	if ws.ProjectPath != "" {
		resolved := filepath.Join(targetPath, ws.ProjectPath)
		if _, err := os.Stat(resolved); err != nil {
			warning = fmt.Sprintf("Project directory not found at resolved path %q — please update the project_path", resolved)
		}
	}

	return ws, warning, nil
}

// importSubWorkspace registers a sub-workspace found during import. Caller must hold s.mu.
func (s *FileStore) importSubWorkspace(folderPath, parentID string) {
	configPath := filepath.Join(folderPath, WorkspaceConfigFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}

	ws, err := FromJSON(data)
	if err != nil {
		return
	}

	if ws.FolderSlug == "" {
		ws.FolderSlug = filepath.Base(folderPath)
	}
	ws.ParentID = parentID

	relPath, err := filepath.Rel(s.basePath, folderPath)
	if err != nil {
		return
	}

	s.cache[ws.ID] = ws
	s.idToPath[ws.ID] = relPath

	if s.index != nil {
		_ = s.index.Register(IndexEntry{
			ID:         ws.ID,
			Name:       ws.Name,
			FolderPath: relPath,
			ParentID:   parentID,
			UpdatedAt:  ws.UpdatedAt,
		})
	}

	// Recurse
	subDir := filepath.Join(folderPath, SubWorkspacesDir)
	if entries, err := os.ReadDir(subDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				s.importSubWorkspace(filepath.Join(subDir, entry.Name()), ws.ID)
			}
		}
	}
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// SyncWorkspaceInfo is a lightweight workspace summary for sync display.
type SyncWorkspaceInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

// SyncStatus holds the result of comparing disk state against the primary store.
type SyncStatus struct {
	InSync       bool                `json:"in_sync"`
	Unregistered []SyncWorkspaceInfo `json:"unregistered"`
	Orphaned     []SyncWorkspaceInfo `json:"orphaned"`
}

// CachedWorkspaces returns deep copies of all workspaces currently in the FileStore cache.
// Callers may safely mutate the returned workspaces without affecting the cache.
func (s *FileStore) CachedWorkspaces() map[string]*Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]*Workspace, len(s.cache))
	for id, ws := range s.cache {
		clone, err := cloneWorkspaceForRebind(ws)
		if err != nil {
			logger.Warn("failed to clone cached workspace", logger.Fields{"id": id, "error": err})
			continue
		}
		result[id] = clone
	}
	return result
}

// ClearAll removes all workspaces from the in-memory cache and index.
// This is used during application reset to ensure stale data is not served
// after the workspace directory has been deleted from disk.
func (s *FileStore) ClearAll() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cache = make(map[string]*Workspace)
	s.idToPath = make(map[string]string)

	if s.index != nil {
		_ = s.index.Rebuild()
	}
}

// Close releases resources held by the FileStore, including the index database.
func (s *FileStore) Close() error {
	if s.index != nil {
		return s.index.Close()
	}
	return nil
}

// GetIndex returns the global workspace index (may be nil).
func (s *FileStore) GetIndex() *Index {
	return s.index
}

// resolveFolder converts a path from idToPath to an absolute folder path.
// Paths from SaveAt are absolute; paths from Save are relative to basePath.
func (s *FileStore) resolveFolder(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(s.basePath, path)
}

// GetFolderPath returns the absolute folder path for a workspace
func (s *FileStore) GetFolderPath(workspaceID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	slug, ok := s.idToPath[workspaceID]
	if !ok {
		return "", fmt.Errorf("workspace %s not found", workspaceID)
	}
	return s.resolveFolder(slug), nil
}

// persistWorkspaceLocked writes workspace.json to disk. Caller must hold s.mu.
func (s *FileStore) persistWorkspaceLocked(ws *Workspace) error {
	relPath, ok := s.idToPath[ws.ID]
	if !ok {
		relPath = ws.FolderSlug
	}
	data, err := ws.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize workspace: %w", err)
	}
	configPath := filepath.Join(s.resolveFolder(relPath), WorkspaceConfigFile)
	if err := atomicWriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write workspace file: %w", err)
	}
	return nil
}

// getNestingDepth returns the nesting depth of a workspace by traversing ParentID.
// Caller must hold s.mu (read or write).
func (s *FileStore) getNestingDepth(id string) int {
	depth := 0
	current := id
	for current != "" {
		depth++
		ws, ok := s.cache[current]
		if !ok {
			break
		}
		current = ws.ParentID
	}
	return depth
}

// migrateIfNeeded checks if the workspace needs migration and returns true if so.
func (s *FileStore) migrateIfNeeded(ws *Workspace, _ string) bool {
	// If migration created AgentInstances
	needsPersist := len(ws.AgentInstances) > 0 && len(ws.Agents) > 0

	// If scheduled tasks were migrated to task schedules
	if len(ws.ScheduledTasks) > 0 {
		ws.ClearLegacyScheduledTasks()
		needsPersist = true
	}

	return needsPersist
}

// persistMigration saves a migrated workspace back to disk.
func (s *FileStore) persistMigration(ws *Workspace, configPath string) {
	data, err := ws.ToJSON()
	if err != nil {
		logger.Error("failed to serialize migrated workspace", logger.Fields{"err": err, "workspace_id": ws.ID})
		return
	}
	if err := atomicWriteFile(configPath, data, 0644); err != nil {
		logger.Error("failed to persist migrated workspace", logger.Fields{"workspace_id": ws.ID, "err": err})
		return
	}
	logger.Info("Successfully persisted migration for workspace", logger.Fields{"workspace_id": ws.ID})
}

// loadCache scans workspace directories and loads all workspaces into memory.
func (s *FileStore) loadCache() error {
	return s.loadWorkspacesFromDir(s.basePath, 0)
}

// loadWorkspacesFromDir recursively loads workspaces from a directory.
func (s *FileStore) loadWorkspacesFromDir(dir string, depth int) error {
	if depth > MaxNestingDepth {
		return nil // Stop recursing beyond max depth
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		folderPath := filepath.Join(dir, entry.Name())
		configPath := filepath.Join(folderPath, WorkspaceConfigFile)

		// Check if this directory contains a workspace.json
		data, err := os.ReadFile(configPath)
		if err != nil {
			continue // Not a workspace folder, skip
		}

		ws, err := FromJSON(data)
		if err != nil {
			// Quarantine the corrupt file so it isn't silently skipped on
			// every subsequent boot. The renamed file remains for forensic
			// recovery; the workspace will surface as unregistered.
			quarantinePath := fmt.Sprintf("%s.corrupt-%d", configPath, time.Now().UnixNano())
			if renameErr := os.Rename(configPath, quarantinePath); renameErr != nil {
				logger.Error("Failed to quarantine corrupt workspace file", logger.Fields{
					"file":         configPath,
					"parse_error":  err.Error(),
					"rename_error": renameErr.Error(),
				})
			} else {
				logger.Error("Quarantined corrupt workspace file", logger.Fields{
					"file":           configPath,
					"quarantined_to": quarantinePath,
					"parse_error":    err.Error(),
				})
			}
			continue
		}

		// Ensure FolderSlug is set
		if ws.FolderSlug == "" {
			ws.FolderSlug = entry.Name()
		}

		// Run migrations
		if s.migrateIfNeeded(ws, configPath) {
			s.persistMigration(ws, configPath)
		}

		// Compute relative path from basePath
		relPath, err := filepath.Rel(s.basePath, folderPath)
		if err != nil {
			relPath = entry.Name()
		}

		s.cache[ws.ID] = ws
		s.idToPath[ws.ID] = relPath

		// Recurse into sub-workspaces directory
		subDir := filepath.Join(folderPath, SubWorkspacesDir)
		if err := s.loadWorkspacesFromDir(subDir, depth+1); err != nil {
			logger.Warn("Failed to load sub-workspaces", logger.Fields{
				"dir":   subDir,
				"error": err.Error(),
			})
		}
	}

	return nil
}

// InMemoryStore implements Store using in-memory storage (for testing)
type InMemoryStore struct {
	workspaces map[string]*Workspace
	mu         sync.RWMutex
}

// NewInMemoryStore creates a new in-memory workspace store
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		workspaces: make(map[string]*Workspace),
	}
}

// Save stores a workspace in memory
func (s *InMemoryStore) Save(ws *Workspace) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.workspaces[ws.ID] = ws
	return nil
}

// Get retrieves a workspace by ID
func (s *InMemoryStore) Get(id string) (*Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ws, ok := s.workspaces[id]
	if !ok {
		return nil, fmt.Errorf("workspace %s not found", id)
	}

	return ws, nil
}

// List returns all workspace IDs
func (s *InMemoryStore) List() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.workspaces))
	for id := range s.workspaces {
		ids = append(ids, id)
	}

	return ids, nil
}

// Delete removes a workspace
func (s *InMemoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.workspaces[id]; !ok {
		return fmt.Errorf("workspace %s not found", id)
	}

	delete(s.workspaces, id)
	return nil
}

// ListActive returns all active workspaces
func (s *InMemoryStore) ListActive() ([]*Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var active []*Workspace
	for _, ws := range s.workspaces {
		if ws.GetStatus() == StatusActive {
			active = append(active, ws)
		}
	}

	return active, nil
}

// GetFilesPath returns the path for storing files for a workspace (in-memory uses temp dir)
func (s *InMemoryStore) GetFilesPath(workspaceID string) string {
	return filepath.Join(os.TempDir(), "ori-workspace-files", workspaceID, FilesDir)
}
