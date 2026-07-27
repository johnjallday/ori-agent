package connections

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store persists the single active Google connection as a JSON metadata file
// under the data dir. It holds only safe metadata plus opaque credential
// references — never tokens, secrets, or authorization codes, which live in the
// vault behind those references (FR 35). V1 supports one active connection
// (one Google `sub`), so the store is a single record, not a collection.
//
// The path is anchored to the data dir the caller resolves (config.DefaultDataDir
// in production), never to the current working directory, so a change of CWD can
// never strand or fork the connection state.
type Store struct {
	mu   sync.RWMutex
	path string
}

// NewStore returns a store rooted at dataDir.
func NewStore(dataDir string) *Store {
	return &Store{path: filepath.Join(dataDir, "connections", "google.json")}
}

// Load returns the persisted connection, or (nil, nil) when none exists yet so
// callers never have to special-case "not connected".
func (s *Store) Load() (*Connection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read connection: %w", err)
	}
	var c Connection
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("decode connection: %w", err)
	}
	return &c, nil
}

// Save persists the connection atomically (temp file + rename) with owner-only
// permissions. Persisting `Connection` is safe because it carries only metadata
// and opaque credential references.
func (s *Store) Save(c *Connection) error {
	if c == nil {
		return errors.New("connections: cannot save a nil connection")
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode connection: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return fmt.Errorf("create connection dir: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write connection: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit connection: %w", err)
	}
	return nil
}

// Delete removes the persisted connection. A missing file is not an error, so
// Delete is idempotent.
func (s *Store) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete connection: %w", err)
	}
	return nil
}
