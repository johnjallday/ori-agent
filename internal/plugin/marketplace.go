package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Marketplace is a catalog of installable plugins (a marketplace.json),
// compatible with Claude Code and Codex marketplace layouts.
type Marketplace struct {
	Name    string             `json:"name"`
	Source  string             `json:"source,omitempty"` // where the catalog was added from
	Dir     string             `json:"dir,omitempty"`    // resolved local catalog directory
	Plugins []MarketplaceEntry `json:"plugins"`
}

// MarketplaceEntry is one plugin listed in a marketplace. Source may be a path
// relative to the catalog, an absolute path, or a git URL.
type MarketplaceEntry struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Description string `json:"description,omitempty"`
}

// marketplaceManifestPaths are the catalog file locations checked, in order —
// covering Claude Code (.claude-plugin/, repo root) and Codex (.agents/plugins/).
var marketplaceManifestPaths = []string{
	"marketplace.json",
	filepath.Join(".claude-plugin", "marketplace.json"),
	filepath.Join(".codex-plugin", "marketplace.json"),
	filepath.Join(".agents", "plugins", "marketplace.json"),
}

// ParseMarketplace finds and parses a marketplace.json under dir.
func ParseMarketplace(dir string) (Marketplace, error) {
	for _, rel := range marketplaceManifestPaths {
		data, err := os.ReadFile(filepath.Join(dir, rel)) // #nosec G304 -- path under a user-provided catalog dir
		if err != nil {
			continue
		}
		var m Marketplace
		if err := json.Unmarshal(data, &m); err != nil {
			return Marketplace{}, fmt.Errorf("plugin: parse marketplace %s: %w", rel, err)
		}
		m.Dir = dir
		return m, nil
	}
	return Marketplace{}, fmt.Errorf("plugin: no marketplace.json found under %s", dir)
}

// resolveEntrySource resolves a marketplace entry's source: a relative path is
// joined to the catalog dir; git URLs and absolute paths are returned as-is.
func resolveEntrySource(catalogDir, source string) string {
	if isGitURL(source) || filepath.IsAbs(source) {
		return source
	}
	return filepath.Join(catalogDir, filepath.Clean(source))
}

// marketplaceRecord is a persisted reference to an added marketplace.
type marketplaceRecord struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Dir    string `json:"dir"`
}

// MarketplaceStore persists the set of added marketplaces.
type MarketplaceStore struct {
	path string
	mu   sync.Mutex
}

// NewMarketplaceStore returns a store backed by marketplaces.json under dir.
func NewMarketplaceStore(dir string) *MarketplaceStore {
	return &MarketplaceStore{path: filepath.Join(dir, "marketplaces.json")}
}

func (s *MarketplaceStore) load() ([]marketplaceRecord, error) {
	data, err := os.ReadFile(s.path) // #nosec G304 -- managed plugins directory
	if err != nil {
		if os.IsNotExist(err) {
			return []marketplaceRecord{}, nil
		}
		return nil, fmt.Errorf("plugin: read marketplaces: %w", err)
	}
	var recs []marketplaceRecord
	if err := json.Unmarshal(data, &recs); err != nil {
		return nil, fmt.Errorf("plugin: parse marketplaces: %w", err)
	}
	return recs, nil
}

func (s *MarketplaceStore) save(recs []marketplaceRecord) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return fmt.Errorf("plugin: create marketplaces dir: %w", err)
	}
	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return fmt.Errorf("plugin: marshal marketplaces: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("plugin: write marketplaces: %w", err)
	}
	return nil
}

// Put inserts or replaces a marketplace record (keyed by name).
func (s *MarketplaceStore) Put(rec marketplaceRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.load()
	if err != nil {
		return err
	}
	for i := range recs {
		if recs[i].Name == rec.Name {
			recs[i] = rec
			return s.save(recs)
		}
	}
	return s.save(append(recs, rec))
}

// Get returns the named marketplace record.
func (s *MarketplaceStore) Get(name string) (marketplaceRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.load()
	if err != nil {
		return marketplaceRecord{}, false, err
	}
	for _, r := range recs {
		if r.Name == name {
			return r, true, nil
		}
	}
	return marketplaceRecord{}, false, nil
}

// List returns all added marketplace records, sorted by name.
func (s *MarketplaceStore) List() ([]marketplaceRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.load()
	if err != nil {
		return nil, err
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
	return recs, nil
}

// AddMarketplace resolves a marketplace source, parses its catalog, and records
// it so plugins can later be installed from it by name.
func (m *Manager) AddMarketplace(source string) (Marketplace, error) {
	dir, err := ResolveSource(source, m.cloneDir)
	if err != nil {
		return Marketplace{}, err
	}
	mp, err := ParseMarketplace(dir)
	if err != nil {
		return Marketplace{}, err
	}
	mp.Source = source
	if err := m.marketplaces.Put(marketplaceRecord{Name: mp.Name, Source: source, Dir: dir}); err != nil {
		return Marketplace{}, err
	}
	return mp, nil
}

// Marketplaces returns the added marketplaces with their current catalog entries.
func (m *Manager) Marketplaces() ([]Marketplace, error) {
	recs, err := m.marketplaces.List()
	if err != nil {
		return nil, err
	}
	out := make([]Marketplace, 0, len(recs))
	for _, r := range recs {
		mp, err := ParseMarketplace(r.Dir)
		if err != nil {
			mp = Marketplace{} // catalog unreadable; report the record without entries
		}
		mp.Name = r.Name
		mp.Source = r.Source
		mp.Dir = r.Dir
		out = append(out, mp)
	}
	return out, nil
}

// marketplaceEntrySource resolves a marketplace entry to an installable source
// (relative entry paths are joined to the catalog dir).
func (m *Manager) marketplaceEntrySource(marketplaceName, pluginName string) (string, error) {
	rec, ok, err := m.marketplaces.Get(marketplaceName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("plugin: marketplace %q not added", marketplaceName)
	}
	mp, err := ParseMarketplace(rec.Dir)
	if err != nil {
		return "", err
	}
	for _, e := range mp.Plugins {
		if e.Name == pluginName {
			return resolveEntrySource(rec.Dir, e.Source), nil
		}
	}
	return "", fmt.Errorf("plugin: %q not found in marketplace %q", pluginName, marketplaceName)
}

// PreviewFromMarketplace returns the trust report for a marketplace plugin
// without installing anything.
func (m *Manager) PreviewFromMarketplace(marketplaceName, pluginName string, prefer SourceFormat) (TrustReport, error) {
	src, err := m.marketplaceEntrySource(marketplaceName, pluginName)
	if err != nil {
		return TrustReport{}, err
	}
	return m.Preview(src, prefer)
}

// InstallFromMarketplace installs a named plugin listed in an added marketplace.
func (m *Manager) InstallFromMarketplace(marketplaceName, pluginName string, prefer SourceFormat, confirm ConfirmFunc) (InstalledPlugin, error) {
	src, err := m.marketplaceEntrySource(marketplaceName, pluginName)
	if err != nil {
		return InstalledPlugin{}, err
	}
	return m.Install(src, prefer, confirm)
}
