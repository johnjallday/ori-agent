package externalmemory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// Sync results recorded on a source.
const (
	ResultOK          = "ok"
	ResultPartial     = "partial"
	ResultError       = "error"
	ResultUnavailable = "unavailable"
)

// SyncCounts summarizes a source's mirror and its last sync's changes.
type SyncCounts struct {
	Entries int `json:"entries"`
	Folders int `json:"folders"`
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Removed int `json:"removed"`
}

// SourceState is what Ori remembers about one source: the user's switch and
// exclusions, and the outcome of its last sync.
type SourceState struct {
	Enabled bool `json:"enabled"`
	// Exclusions are folder prefixes whose entries are not mirrored (FR4).
	Exclusions       []string   `json:"exclusions"`
	LastSyncStarted  *time.Time `json:"last_sync_started,omitempty"`
	LastSyncFinished *time.Time `json:"last_sync_finished,omitempty"`
	LastResult       string     `json:"last_result,omitempty"`
	// LastError is a short, content-free reason for an error result.
	LastError    string     `json:"last_error,omitempty"`
	SkippedFiles int        `json:"skipped_files,omitempty"`
	Counts       SyncCounts `json:"counts"`
	// StoreFull is set when the last sync stopped adding entries at
	// MaxStoreBytes.
	StoreFull bool `json:"store_full,omitempty"`
	// PasteDate is when the Claude account paste was last replaced.
	PasteDate *time.Time `json:"paste_date,omitempty"`
}

// SourcesDocument is sources.json.
type SourcesDocument struct {
	Version int `json:"version"`
	// LastCompletedSync arms the daily timer: the next scheduled sync is due 24
	// hours after it.
	LastCompletedSync *time.Time             `json:"last_completed_sync,omitempty"`
	Sources           map[string]SourceState `json:"sources"`
}

// Source returns the state for id, zero-valued when absent.
func (d SourcesDocument) Source(id string) SourceState {
	state := d.Sources[id]
	if state.Exclusions == nil {
		state.Exclusions = []string{}
	}
	return state
}

type entriesDocument struct {
	Version int     `json:"version"`
	Source  string  `json:"source"`
	Entries []Entry `json:"entries"`
}

const (
	sourcesFileName  = "sources.json"
	entriesDirName   = "entries"
	documentVersion  = 1
	corruptSuffix    = ".corrupt"
	storeDirPerm     = 0o750
	storeFilePerm    = 0o600
	maxStoreFileSize = MaxStoreBytes + 16<<20
)

// Store keeps the mirror under one directory: sources.json and one
// entries/<source>.json per source. Writes are atomic (temp file + rename). A
// missing or corrupt file loads as empty with a log line; a corrupt file is
// moved aside so the next write cannot be confused with it. Entries are cached
// after the first read; the store is their only writer.
type Store struct {
	dir string

	mu      sync.Mutex
	entries map[string][]Entry
}

// NewStore returns a store rooted at dir (normally
// config.DefaultExternalMemoryDir()).
func NewStore(dir string) *Store {
	return &Store{dir: dir, entries: map[string][]Entry{}}
}

// Dir returns the store's root folder.
func (s *Store) Dir() string { return s.dir }

func (s *Store) sourcesPath() string { return filepath.Join(s.dir, sourcesFileName) }

func (s *Store) entriesPath(source string) (string, error) {
	if !ValidSourceID(source) {
		return "", fmt.Errorf("unknown external memory source %q", source)
	}
	return filepath.Join(s.dir, entriesDirName, source+".json"), nil
}

// LoadSources reads sources.json. Missing or corrupt loads as an empty
// document, so every source reads as off.
func (s *Store) LoadSources() SourcesDocument {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadSourcesLocked()
}

func (s *Store) loadSourcesLocked() SourcesDocument {
	doc := SourcesDocument{Version: documentVersion, Sources: map[string]SourceState{}}
	if !s.readJSON(s.sourcesPath(), &doc) {
		return SourcesDocument{Version: documentVersion, Sources: map[string]SourceState{}}
	}
	if doc.Sources == nil {
		doc.Sources = map[string]SourceState{}
	}
	for id := range doc.Sources {
		if !ValidSourceID(id) {
			delete(doc.Sources, id)
		}
	}
	return doc
}

// UpdateSources applies mutate to sources.json under the store lock and saves
// the result. A mutate error leaves the file unchanged.
func (s *Store) UpdateSources(mutate func(*SourcesDocument) error) (SourcesDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.loadSourcesLocked()
	if err := mutate(&doc); err != nil {
		return SourcesDocument{}, err
	}
	doc.Version = documentVersion
	if err := s.writeJSON(s.sourcesPath(), doc); err != nil {
		return SourcesDocument{}, err
	}
	return doc, nil
}

// LoadEntries returns a copy of a source's mirrored entries, removed ones
// included. Missing or corrupt loads as empty.
func (s *Store) LoadEntries(source string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.loadEntriesLocked(source))
}

func (s *Store) loadEntriesLocked(source string) []Entry {
	if cached, ok := s.entries[source]; ok {
		return cached
	}
	path, err := s.entriesPath(source)
	if err != nil {
		return nil
	}
	var doc entriesDocument
	var entries []Entry
	if s.readJSON(path, &doc) {
		for _, entry := range doc.Entries {
			if entry.ID == "" || entry.Source != source {
				continue
			}
			entries = append(entries, entry)
		}
	}
	s.entries[source] = entries
	return entries
}

// SaveEntries replaces a source's mirrored entries.
func (s *Store) SaveEntries(source string, entries []Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveEntriesLocked(source, entries)
}

func (s *Store) saveEntriesLocked(source string, entries []Entry) error {
	path, err := s.entriesPath(source)
	if err != nil {
		return err
	}
	if entries == nil {
		entries = []Entry{}
	}
	if err := s.writeJSON(path, entriesDocument{Version: documentVersion, Source: source, Entries: entries}); err != nil {
		return err
	}
	s.entries[source] = slices.Clone(entries)
	return nil
}

// UpdateEntry applies mutate to one entry under the store lock and saves the
// source's file. It reports false when no entry has that id.
func (s *Store) UpdateEntry(source, id string, mutate func(*Entry)) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := slices.Clone(s.loadEntriesLocked(source))
	for i := range entries {
		if entries[i].ID != id {
			continue
		}
		mutate(&entries[i])
		if err := s.saveEntriesLocked(source, entries); err != nil {
			return Entry{}, false, err
		}
		return entries[i], true, nil
	}
	return Entry{}, false, nil
}

// TotalBodyBytes is the stored size of every source's bodies, the measure
// MaxStoreBytes caps.
func (s *Store) TotalBodyBytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, id := range SourceIDs {
		for _, entry := range s.loadEntriesLocked(id) {
			total += len(entry.Body)
		}
	}
	return total
}

// readJSON decodes path into v. A missing file returns false quietly; an
// unreadable or corrupt one returns false with a log line, and a corrupt one is
// moved aside to <name>.corrupt.
func (s *Store) readJSON(path string, v any) bool {
	info, err := os.Stat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.Warn("External memory file unreadable; treating as empty", logger.Fields{"path": path, "error": err})
		}
		return false
	}
	if info.Size() > maxStoreFileSize {
		logger.Warn("External memory file exceeds the store cap; treating as empty", logger.Fields{"path": path, "bytes": info.Size()})
		return false
	}
	// path is the store's own directory joined with a fixed name or a
	// validated source id.
	data, err := os.ReadFile(path) // #nosec G304 -- store-owned path built from fixed names and validated source ids
	if err != nil {
		logger.Warn("External memory file unreadable; treating as empty", logger.Fields{"path": path, "error": err})
		return false
	}
	if err := json.Unmarshal(data, v); err != nil {
		logger.Warn("External memory file is corrupt; treating as empty", logger.Fields{"path": path, "error": err})
		if renameErr := os.Rename(path, path+corruptSuffix); renameErr != nil {
			logger.Warn("Could not move corrupt external memory file aside", logger.Fields{"path": path, "error": renameErr})
		}
		return false
	}
	return true
}

func (s *Store) writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	if err := os.MkdirAll(filepath.Dir(path), storeDirPerm); err != nil {
		return fmt.Errorf("create external memory folder: %w", err)
	}
	return atomicWriteFile(path, append(data, '\n'))
}

// atomicWriteFile writes data via a temp file in the destination directory
// followed by a rename, so a crash mid-write can never leave a truncated file.
func atomicWriteFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Chmod(storeFilePerm); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("set permissions on %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("flush %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

// normalizeExclusions trims, expands a leading ~, cleans, and de-duplicates
// folder prefixes. Relative prefixes are dropped: an exclusion names a folder.
func normalizeExclusions(prefixes []string, home string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range prefixes {
		prefix := expandHome(strings.TrimSpace(raw), home)
		if prefix == "" || !filepath.IsAbs(prefix) {
			continue
		}
		prefix = filepath.Clean(prefix)
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		out = append(out, prefix)
	}
	return out
}

func expandHome(path, home string) string {
	if home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// excluded reports whether folder sits at or under one of the prefixes. An
// entry with no folder is about the user in general and is never excluded.
func excluded(folder string, prefixes []string) bool {
	if folder == "" {
		return false
	}
	for _, prefix := range prefixes {
		if folder == prefix || strings.HasPrefix(folder, strings.TrimRight(prefix, "/")+"/") {
			return true
		}
	}
	return false
}
