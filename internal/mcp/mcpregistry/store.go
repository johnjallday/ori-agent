package mcpregistry

import (
	"encoding/json"
	"os"
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
	FetchedAt time.Time       `json:"fetched_at"`
	Entries   []RegistryEntry `json:"entries"`
}

// Store manages persistence of registry sources and fetched entry cache.
type Store struct {
	mu      sync.RWMutex
	sources []RegistrySource
	cache   []RegistryEntry
	cacheAt time.Time
}

// NewStore creates a Store and loads persisted data from disk.
func NewStore() *Store {
	s := &Store{}
	s.load()
	return s
}

func (s *Store) load() {
	data, err := os.ReadFile(sourcesFile)
	if err == nil {
		var fd sourcesFileData
		if json.Unmarshal(data, &fd) == nil {
			s.sources = fd.Sources
		}
	}
	s.ensureBuiltins()

	cacheData, err := os.ReadFile(cacheFile)
	if err == nil {
		var cd cacheFileData
		if json.Unmarshal(cacheData, &cd) == nil {
			s.cache = cd.Entries
			s.cacheAt = cd.FetchedAt
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
	return os.WriteFile(sourcesFile, data, 0644)
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
	return len(s.cache) > 0 && time.Since(s.cacheAt) < cacheTTL
}

// GetCachedEntries returns a copy of the currently cached entries.
func (s *Store) GetCachedEntries() []RegistryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RegistryEntry, len(s.cache))
	copy(out, s.cache)
	return out
}

// SetCache replaces the cached entries and persists them to disk.
func (s *Store) SetCache(entries []RegistryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.cache = entries
	s.cacheAt = now
	cd := cacheFileData{FetchedAt: now, Entries: entries}
	data, err := json.MarshalIndent(cd, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cacheFile, data, 0644)
}

// InvalidateCache clears the in-memory cache timestamp, forcing a re-fetch on next access.
func (s *Store) InvalidateCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cacheAt = time.Time{}
}
