package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	// OfficialMarketplaceName is the catalog name published by Anthropic's
	// official, managed plugin directory.
	OfficialMarketplaceName = "claude-plugins-official"
	// OfficialMarketplaceSource is the git source for that directory. It is the
	// single backend-held source the "Add official marketplace" button installs;
	// the UI never hardcodes the URL.
	OfficialMarketplaceSource = "https://github.com/anthropics/claude-plugins-official.git"
)

// Marketplace is a catalog of installable plugins (a marketplace.json),
// compatible with Claude Code and Codex marketplace layouts.
type Marketplace struct {
	Name    string             `json:"name"`
	Source  string             `json:"source,omitempty"` // where the catalog was added from
	Dir     string             `json:"dir,omitempty"`    // resolved local catalog directory
	Plugins []MarketplaceEntry `json:"plugins"`
}

// MarketplaceAuthor is a catalog entry's author. Catalogs use either a bare
// string or an object with name/email; both normalize to this.
type MarketplaceAuthor struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// MarketplaceEntry is one plugin listed in a marketplace. In real catalogs the
// "source" is often an object (e.g. {"source":"local","path":"./..."},
// {"source":"github","repo":"owner/name"}, or a git repo + subdirectory
// {"source":"git-subdir","url":"...","path":"plugins/x","ref":"v1"}) rather than
// a string; UnmarshalJSON normalizes all forms to a single installable Source (a
// path relative to the catalog, an absolute path, a git URL, or a git repo +
// subdirectory encoded by encodeGitSubdir). The remaining fields are display
// metadata used by the browse UI (cards, search, and category/tag filtering).
type MarketplaceEntry struct {
	Name        string            `json:"name"`
	Source      string            `json:"source"`
	Description string            `json:"description,omitempty"`
	Category    string            `json:"category,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Keywords    []string          `json:"keywords,omitempty"`
	Author      MarketplaceAuthor `json:"author,omitzero"`
	Homepage    string            `json:"homepage,omitempty"`
}

// UnmarshalJSON accepts both the string and object forms of "source", and the
// string or object forms of "author". Missing display fields default to empty.
func (e *MarketplaceEntry) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Source      json.RawMessage `json:"source"`
		Category    string          `json:"category"`
		Tags        []string        `json:"tags"`
		Keywords    []string        `json:"keywords"`
		Author      json.RawMessage `json:"author"`
		Homepage    string          `json:"homepage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.Name = raw.Name
	e.Description = raw.Description
	e.Source = normalizeEntrySource(raw.Source)
	e.Category = raw.Category
	e.Tags = raw.Tags
	e.Keywords = raw.Keywords
	e.Homepage = raw.Homepage
	e.Author = parseMarketplaceAuthor(raw.Author)
	return nil
}

// parseMarketplaceAuthor accepts the bare-string and object forms of "author".
func parseMarketplaceAuthor(raw json.RawMessage) MarketplaceAuthor {
	if len(raw) == 0 {
		return MarketplaceAuthor{}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return MarketplaceAuthor{Name: s}
	}
	var obj MarketplaceAuthor
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj
	}
	return MarketplaceAuthor{}
}

// normalizeEntrySource reduces a marketplace entry's "source" (a string or an
// object) to one installable source string, or "" if it can't be resolved.
func normalizeEntrySource(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Source string `json:"source"`
		Type   string `json:"type"`
		Path   string `json:"path"`
		Repo   string `json:"repo"`
		URL    string `json:"url"`
		Ref    string `json:"ref"`
		Sha    string `json:"sha"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	gitURL := obj.URL
	if gitURL == "" && obj.Repo != "" {
		gitURL = "https://github.com/" + obj.Repo + ".git"
	}
	switch {
	case gitURL != "" && obj.Path != "":
		// git repo + subdirectory (the "git-subdir"/"url" entry types): clone the
		// repo and install from the subpath, pinned to ref/sha when present. Must
		// be checked before the bare-path case — these entries carry both a url
		// and a path, and the path is relative to the repo, not the catalog dir.
		return encodeGitSubdir(gitURL, obj.Path, obj.Ref, obj.Sha)
	case gitURL != "" && (obj.Ref != "" || obj.Sha != ""):
		// whole-repo plugin pinned to a specific commit
		return encodeGitSubdir(gitURL, "", obj.Ref, obj.Sha)
	case gitURL != "":
		return gitURL
	case obj.Path != "":
		// path relative to (or absolute from) the catalog dir
		return obj.Path
	default:
		return ""
	}
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
