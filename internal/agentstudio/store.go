package agentstudio

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
}

// FileStore implements Store using file-based persistence
type FileStore struct {
	basePath string
	cache    map[string]*Workspace
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
	}

	// Load existing workspaces into cache
	if err := store.loadCache(); err != nil {
		return nil, fmt.Errorf("failed to load workspace cache: %w", err)
	}

	return store, nil
}

// Save persists a workspace to disk
func (s *FileStore) Save(ws *Workspace) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Serialize workspace
	data, err := ws.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize workspace: %w", err)
	}

	// Write to file
	filePath := s.getFilePath(ws.ID)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write workspace file: %w", err)
	}

	// Update cache
	s.cache[ws.ID] = ws

	return nil
}

// Get retrieves a workspace by ID
func (s *FileStore) Get(id string) (*Workspace, error) {
	s.mu.RLock()

	// Check cache first
	if ws, ok := s.cache[id]; ok {
		s.mu.RUnlock()
		return ws, nil
	}
	s.mu.RUnlock()

	// Load from disk if not in cache
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.getFilePath(id)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("workspace %s not found", id)
		}
		return nil, fmt.Errorf("failed to read workspace file: %w", err)
	}

	ws, err := FromJSON(data)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize workspace: %w", err)
	}

	// Update cache
	s.cache[id] = ws

	return ws, nil
}

// List returns all workspace IDs
func (s *FileStore) List() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace directory: %w", err)
	}

	var ids []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			id := entry.Name()[:len(entry.Name())-5] // Remove .json extension
			ids = append(ids, id)
		}
	}

	return ids, nil
}

// Delete removes a workspace from storage
func (s *FileStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.getFilePath(id)
	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("workspace %s not found", id)
		}
		return fmt.Errorf("failed to delete workspace file: %w", err)
	}

	// Remove from cache
	delete(s.cache, id)

	return nil
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

// getFilePath returns the file path for a workspace ID
func (s *FileStore) getFilePath(id string) string {
	return filepath.Join(s.basePath, id+".json")
}

// loadCache loads all workspaces into memory cache
func (s *FileStore) loadCache() error {
	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Directory doesn't exist yet, that's ok
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			filePath := filepath.Join(s.basePath, entry.Name())
			data, err := os.ReadFile(filePath)
			if err != nil {
				continue // Skip files that can't be read
			}

			ws, err := FromJSON(data)
			if err != nil {
				continue // Skip files that can't be deserialized
			}

			s.cache[ws.ID] = ws
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
