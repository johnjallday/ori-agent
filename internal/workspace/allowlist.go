package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultAllowlistFilename is the default file inside the data directory
// (cwd at runtime, typically ori-data/) holding workspace IDs whose agents
// should be hydrated from ~/Ori Workspaces/ on startup.
const DefaultAllowlistFilename = "workspace_allowlist.json"

// Allowlist tracks the set of workspace IDs that have been explicitly imported
// into the current data directory. Only workspaces in this set will have their
// agent snapshots restored at server startup.
//
// The zero value is not usable — call NewAllowlist or LoadAllowlist.
type Allowlist struct {
	path string
	mu   sync.Mutex
	ids  map[string]struct{}
}

type allowlistFile struct {
	WorkspaceIDs []string `json:"workspace_ids"`
}

// NewAllowlist returns an empty Allowlist backed by the given file path.
// The file is not read; call Load to populate from disk.
func NewAllowlist(path string) *Allowlist {
	return &Allowlist{
		path: path,
		ids:  make(map[string]struct{}),
	}
}

// LoadAllowlist reads the allowlist file from disk. A missing file yields an
// empty allowlist (not an error) — callers can treat absence as "no workspaces
// imported yet."
func LoadAllowlist(path string) (*Allowlist, error) {
	a := NewAllowlist(path)
	if err := a.Load(); err != nil {
		return nil, err
	}
	return a, nil
}

// Load (re)reads the allowlist from disk. A missing file is not an error.
func (a *Allowlist) Load() error {
	if a == nil {
		return errors.New("nil allowlist")
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	data, err := os.ReadFile(a.path)
	if err != nil {
		if os.IsNotExist(err) {
			a.ids = make(map[string]struct{})
			return nil
		}
		return fmt.Errorf("read allowlist %s: %w", a.path, err)
	}

	var parsed allowlistFile
	if len(data) > 0 {
		if err := json.Unmarshal(data, &parsed); err != nil {
			return fmt.Errorf("parse allowlist %s: %w", a.path, err)
		}
	}

	a.ids = make(map[string]struct{}, len(parsed.WorkspaceIDs))
	for _, id := range parsed.WorkspaceIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		a.ids[trimmed] = struct{}{}
	}
	return nil
}

// Save atomically writes the allowlist to disk via temp-file + rename.
func (a *Allowlist) Save() error {
	if a == nil {
		return errors.New("nil allowlist")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveUnlocked()
}

func (a *Allowlist) saveUnlocked() error {
	ids := make([]string, 0, len(a.ids))
	for id := range a.ids {
		ids = append(ids, id)
	}
	// Stable order: lexicographic.
	sortStrings(ids)

	payload, err := json.MarshalIndent(allowlistFile{WorkspaceIDs: ids}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal allowlist: %w", err)
	}

	dir := filepath.Dir(a.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir allowlist dir %s: %w", dir, err)
		}
	}

	tmp, err := os.CreateTemp(dir, ".workspace_allowlist-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp allowlist: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp allowlist: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp allowlist: %w", err)
	}
	if err := os.Rename(tmpPath, a.path); err != nil {
		cleanup()
		return fmt.Errorf("rename allowlist: %w", err)
	}
	return nil
}

// Contains reports whether the given workspace ID is in the allowlist.
func (a *Allowlist) Contains(workspaceID string) bool {
	if a == nil {
		return false
	}
	id := strings.TrimSpace(workspaceID)
	if id == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.ids[id]
	return ok
}

// Add inserts a workspace ID and persists the allowlist. A no-op (and no-write)
// if the ID is already present. Blank IDs are rejected.
func (a *Allowlist) Add(workspaceID string) error {
	if a == nil {
		return errors.New("nil allowlist")
	}
	id := strings.TrimSpace(workspaceID)
	if id == "" {
		return errors.New("empty workspace id")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.ids[id]; exists {
		return nil
	}
	a.ids[id] = struct{}{}
	return a.saveUnlocked()
}

// Remove deletes a workspace ID and persists the allowlist. A no-op (and
// no-write) if the ID is absent.
func (a *Allowlist) Remove(workspaceID string) error {
	if a == nil {
		return errors.New("nil allowlist")
	}
	id := strings.TrimSpace(workspaceID)
	if id == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.ids[id]; !exists {
		return nil
	}
	delete(a.ids, id)
	return a.saveUnlocked()
}

// IDs returns a snapshot of the workspace IDs in the allowlist.
func (a *Allowlist) IDs() []string {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.ids))
	for id := range a.ids {
		out = append(out, id)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	// Tiny insertion sort to avoid importing "sort" just for this.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
