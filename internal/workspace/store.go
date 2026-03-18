package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/johnjallday/ori-agent/internal/logger"
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

	// Check for folder name conflict (only for new workspaces, not updates)
	folderPath := filepath.Join(parentDir, ws.FolderSlug)
	if existingPath, exists := s.idToPath[ws.ID]; !exists {
		// New workspace — check if folder already exists
		if _, err := os.Stat(folderPath); err == nil {
			return fmt.Errorf("a workspace folder named %q already exists, choose a different name", ws.FolderSlug)
		}
	} else if filepath.Join(s.basePath, existingPath) != folderPath {
		// Existing workspace with changed path — check new folder doesn't exist
		if _, err := os.Stat(folderPath); err == nil {
			return fmt.Errorf("a workspace folder named %q already exists, choose a different name", ws.FolderSlug)
		}
	}

	// Create workspace folder and files subdirectory
	if err := os.MkdirAll(filepath.Join(folderPath, FilesDir), 0755); err != nil {
		return fmt.Errorf("failed to create workspace folder: %w", err)
	}

	// Serialize workspace
	data, err := ws.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize workspace: %w", err)
	}

	// Write workspace.json inside the folder
	configPath := filepath.Join(folderPath, WorkspaceConfigFile)
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write workspace file: %w", err)
	}

	// Reload from disk to ensure cache has fresh copy with all fields properly set
	freshWS, err := FromJSON(data)
	if err != nil {
		return fmt.Errorf("failed to reload workspace after save: %w", err)
	}

	// Compute relative path from basePath
	relPath, err := filepath.Rel(s.basePath, folderPath)
	if err != nil {
		relPath = ws.FolderSlug
	}

	// Update cache and ID-to-path mapping
	s.cache[ws.ID] = freshWS
	s.idToPath[ws.ID] = relPath

	// Update the global index
	if s.index != nil {
		s.index.Register(IndexEntry{
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
	if _, err := os.Stat(folderPath); err == nil {
		return fmt.Errorf("a workspace folder named %q already exists at %s, choose a different name", ws.FolderSlug, location)
	}

	// Create workspace folder and files subdirectory
	if err := os.MkdirAll(filepath.Join(folderPath, FilesDir), 0755); err != nil {
		return fmt.Errorf("failed to create workspace folder: %w", err)
	}

	// Serialize and write workspace.json
	data, err := ws.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize workspace: %w", err)
	}
	configPath := filepath.Join(folderPath, WorkspaceConfigFile)
	if err := os.WriteFile(configPath, data, 0644); err != nil {
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
		s.index.Register(IndexEntry{
			ID:         ws.ID,
			Name:       ws.Name,
			FolderPath: folderPath,
			ParentID:   ws.ParentID,
			UpdatedAt:  ws.UpdatedAt,
		})
	}

	return nil
}

// BasePath returns the default workspace root directory.
func (s *FileStore) BasePath() string {
	return s.basePath
}

// Get retrieves a workspace by ID
func (s *FileStore) Get(id string) (*Workspace, error) {
	s.mu.RLock()

	// Check cache first
	if ws, ok := s.cache[id]; ok {
		s.mu.RUnlock()
		return ws, nil
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
		return ws, nil
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

	return ws, nil
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
func (s *FileStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	relPath, ok := s.idToPath[id]
	if !ok {
		return fmt.Errorf("workspace %s not found", id)
	}

	folderPath := s.resolveFolder(relPath)
	if err := os.RemoveAll(folderPath); err != nil {
		return fmt.Errorf("failed to delete workspace folder: %w", err)
	}

	// Remove this workspace and all children from cache and mappings
	s.removeFromCacheRecursive(id)

	return nil
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
		s.index.Unregister(id)
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
	s.mu.Lock()
	defer s.mu.Unlock()

	oldRelPath, ok := s.idToPath[id]
	if !ok {
		return fmt.Errorf("workspace %s not found", id)
	}

	newSlug := Slugify(newName)
	oldFolderPath := s.resolveFolder(oldRelPath)
	parentDir := filepath.Dir(oldFolderPath)

	// If the slug hasn't changed, just update the display name
	if newSlug == filepath.Base(oldRelPath) {
		if ws, ok := s.cache[id]; ok {
			ws.Name = newName
			return s.persistWorkspaceLocked(ws)
		}
		return nil
	}

	// Check if new folder name already exists
	newFolderPath := filepath.Join(parentDir, newSlug)
	if _, err := os.Stat(newFolderPath); err == nil {
		return fmt.Errorf("a workspace folder named %q already exists, choose a different name", newSlug)
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
		s.index.Register(IndexEntry{
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
		s.index.Register(IndexEntry{
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
		s.index.Register(IndexEntry{
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
	if err := os.WriteFile(configPath, data, 0644); err != nil {
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
	needsPersist := false

	// If migration created AgentInstances
	if len(ws.AgentInstances) > 0 && len(ws.Agents) > 0 {
		needsPersist = true
	}

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
	if err := os.WriteFile(configPath, data, 0644); err != nil {
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
			logger.Warn("Failed to deserialize workspace file, skipping", logger.Fields{
				"file":  configPath,
				"error": err.Error(),
			})
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
