package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// InstalledPlugin records an installed plugin and exactly what it registered, so
// uninstall is exact and reversible (PRD req #14).
type InstalledPlugin struct {
	Name        string       `json:"name"`
	Version     string       `json:"version,omitempty"`
	Description string       `json:"description,omitempty"`
	Source      string       `json:"source"`
	Format      SourceFormat `json:"format"`
	InstallDir  string       `json:"install_dir"`
	MCPServers  []string     `json:"mcp_servers,omitempty"` // namespaced names
	Skills      []string     `json:"skills,omitempty"`
	Enabled     bool         `json:"enabled"`
	InstalledAt time.Time    `json:"installed_at"`
}

// Store is the JSON-backed installed-plugins registry. Components owned by a
// plugin are recorded here so they can be listed and cleanly removed; plugin
// components are read-only/plugin-owned (PRD decision #8) and managed only
// through install/uninstall.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore returns a store backed by installed.json under the managed plugins dir.
func NewStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, "installed.json")}
}

func (s *Store) load() ([]InstalledPlugin, error) {
	data, err := os.ReadFile(s.path) // #nosec G304 -- path is the managed plugins directory
	if err != nil {
		if os.IsNotExist(err) {
			return []InstalledPlugin{}, nil
		}
		return nil, fmt.Errorf("plugin: read store: %w", err)
	}
	var list []InstalledPlugin
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("plugin: parse store: %w", err)
	}
	return list, nil
}

func (s *Store) save(list []InstalledPlugin) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return fmt.Errorf("plugin: create store dir: %w", err)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("plugin: marshal store: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("plugin: write store: %w", err)
	}
	return nil
}

// List returns installed plugins sorted by name.
func (s *Store) List() ([]InstalledPlugin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// Get returns the named plugin, if installed.
func (s *Store) Get(name string) (InstalledPlugin, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return InstalledPlugin{}, false, err
	}
	for _, p := range list {
		if p.Name == name {
			return p, true, nil
		}
	}
	return InstalledPlugin{}, false, nil
}

// Put inserts or replaces a plugin entry.
func (s *Store) Put(p InstalledPlugin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].Name == p.Name {
			list[i] = p
			return s.save(list)
		}
	}
	return s.save(append(list, p))
}

// Delete removes a plugin entry (no-op if absent).
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	out := make([]InstalledPlugin, 0, len(list))
	for _, p := range list {
		if p.Name != name {
			out = append(out, p)
		}
	}
	return s.save(out)
}

// SetEnabled updates a plugin's enabled flag.
func (s *Store) SetEnabled(name string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].Name == name {
			list[i].Enabled = enabled
			return s.save(list)
		}
	}
	return fmt.Errorf("plugin: %q not installed", name)
}
