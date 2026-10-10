package mcpregistry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	sourcesFile = "mcp_search_sources.json"
	cacheFile   = "mcp_search_cache.json"
	cacheTTL    = time.Hour
)

type sourcesFileData struct {
	Sources []RegistrySource `json:"sources"`
}

type cacheFileData struct {
	FetchedAt    time.Time                    `json:"fetched_at"`
	Entries      []RegistryEntry              `json:"entries"`
	Observations map[string]sourceObservation `json:"source_observations,omitempty"`
}

// Store manages persistence of registry sources and fetched entry cache.
type Store struct {
	mu               sync.RWMutex
	baseDir          string
	sources          []RegistrySource
	cache            []RegistryEntry
	cacheAt          time.Time
	cacheInvalidated bool
	observations     map[string]sourceObservation
}

// NewStore creates a Store and loads persisted data from disk.
func NewStore() *Store { return NewStoreAt(".") }

// NewStoreAt binds registry persistence to one explicit installation root.
func NewStoreAt(baseDir string) *Store {
	s := &Store{baseDir: baseDir}
	s.load()
	return s
}

// PersistencePaths reports custom-source and fetched-cache documents.
func (s *Store) PersistencePaths() (string, string) {
	return filepath.Join(s.baseDir, sourcesFile), filepath.Join(s.baseDir, cacheFile)
}

func (s *Store) load() {
	sourcesPath, cachePath := s.PersistencePaths()
	// #nosec G304 -- the constructor binds the owner root and both leaf names
	// are compiled constants; callers cannot supply a file name.
	data, err := os.ReadFile(sourcesPath)
	if err == nil {
		var fd sourcesFileData
		if json.Unmarshal(data, &fd) == nil {
			s.sources = fd.Sources
		}
	}
	s.ensureBuiltins()

	// #nosec G304 -- cachePath uses the same bound root and compiled leaf name.
	cacheData, err := os.ReadFile(cachePath)
	if err == nil {
		var cd cacheFileData
		if json.Unmarshal(cacheData, &cd) == nil {
			s.cache = cd.Entries
			s.cacheAt = cd.FetchedAt
			s.observations = cd.Observations
		}
	}
}

// ensureBuiltins adds the built-in curated source if not already present.
func (s *Store) ensureBuiltins() {
	for _, src := range s.sources {
		if src.ID == "builtin-curated" {
			return
		}
	}
	builtin := RegistrySource{
		ID:         "builtin-curated",
		Name:       "Ori Curated",
		SourceType: "builtin",
		Enabled:    true,
		IsBuiltin:  true,
	}
	s.sources = append([]RegistrySource{builtin}, s.sources...)
}

func (s *Store) saveSources() error {
	fd := sourcesFileData{Sources: s.sources}
	data, err := json.MarshalIndent(fd, "", "  ")
	if err != nil {
		return err
	}
	sourcesPath, _ := s.PersistencePaths()
	return os.WriteFile(sourcesPath, data, 0o600)
}

// GetSources returns a copy of all configured registry sources.
func (s *Store) GetSources() []RegistrySource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RegistrySource, len(s.sources))
	copy(out, s.sources)
	return out
}

// AddSource persists a new registry source.
func (s *Store) AddSource(src RegistrySource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources = append(s.sources, src)
	return s.saveSources()
}

// RemoveSource removes a non-builtin source by ID.
func (s *Store) RemoveSource(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := make([]RegistrySource, 0, len(s.sources))
	for _, src := range s.sources {
		if src.ID != id {
			filtered = append(filtered, src)
		}
	}
	s.sources = filtered
	return s.saveSources()
}

// IsCacheValid reports whether the cached entries are still within the TTL.
func (s *Store) IsCacheValid() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	age := time.Since(s.cacheAt)
	return !s.cacheInvalidated && !s.cacheAt.IsZero() && age >= 0 && age < cacheTTL
}

// GetCachedEntries returns a copy of the currently cached entries.
func (s *Store) GetCachedEntries() []RegistryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RegistryEntry, len(s.cache))
	for i, entry := range s.cache {
		out[i] = cloneEntry(entry)
	}
	return out
}

// SetCache replaces the cached entries and persists them to disk.
func (s *Store) SetCache(entries []RegistryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.cache = make([]RegistryEntry, len(entries))
	for i, entry := range entries {
		s.cache[i] = cloneEntry(entry)
	}
	s.cacheAt = now
	s.cacheInvalidated = false
	s.observations = nil // legacy all-source refresh cannot prove per-source emptiness
	return s.writeCache(cacheFileData{FetchedAt: now, Entries: s.cache})
}

func (s *Store) writeCache(cd cacheFileData) error {
	data, err := json.MarshalIndent(cd, "", "  ")
	if err != nil {
		return err
	}
	_, cachePath := s.PersistencePaths()
	return os.WriteFile(cachePath, data, 0o600)
}

// InvalidateCache marks the cache stale while retaining its observation time.
func (s *Store) InvalidateCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cacheInvalidated = true
	s.observations = nil
}
