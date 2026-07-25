package downloadsjanitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// StateDirName is the folder, inside a workspace's own folder, that holds every
// Downloads Janitor record for that workspace. Keeping state in the workspace
// folder makes disk the source of truth and makes workspace deletion, move, and
// backup carry Janitor state along with everything else.
const StateDirName = "downloads-janitor"

// settingsFileName is the settings record inside StateDirName.
const settingsFileName = "settings.json"

// ErrWorkspaceMismatch reports a persisted record whose workspace id does not
// match the workspace it was read for. It is a hard error, never a silent
// fallback: serving one workspace's Janitor state to another would break the
// isolation the whole feature depends on (FR-118).
var ErrWorkspaceMismatch = errors.New("downloads janitor state belongs to a different workspace")

// FolderResolver resolves a workspace ID to its folder path on disk.
type FolderResolver = workspace.FolderResolver

// Store is the durable, workspace-scoped home for Downloads Janitor state.
//
// Disk is authoritative: every read goes to the file, every write replaces it
// atomically (temp file + rename), and there is no in-memory copy that could
// survive a restart with a different answer. Mutations are serialized per
// workspace so a concurrent read-modify-write cannot lose an update.
type Store struct {
	resolver FolderResolver

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewStore returns a store that keeps each workspace's state inside that
// workspace's folder.
func NewStore(resolver FolderResolver) *Store {
	return &Store{resolver: resolver, locks: map[string]*sync.Mutex{}}
}

// lockFor returns the per-workspace mutex, creating it on first use.
func (s *Store) lockFor(workspaceID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	lock, ok := s.locks[workspaceID]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[workspaceID] = lock
	}
	return lock
}

// StateDir returns the workspace's Janitor state directory. The workspace ID is
// only a lookup key for the folder resolver, never a path segment, so a hostile
// id cannot traverse out of the workspace tree.
func (s *Store) StateDir(workspaceID string) (string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return "", fmt.Errorf("%w: workspace id is required", ErrInvalidSettings)
	}
	if s == nil || s.resolver == nil {
		return "", errors.New("downloads janitor storage is unavailable")
	}
	folder, err := s.resolver.GetFolderPath(workspaceID)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace folder: %w", err)
	}
	if strings.TrimSpace(folder) == "" {
		return "", fmt.Errorf("workspace %s has no folder on disk", workspaceID)
	}
	return filepath.Join(folder, StateDirName), nil
}

func (s *Store) settingsPath(workspaceID string) (string, error) {
	dir, err := s.StateDir(workspaceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, settingsFileName), nil
}

// LoadSettings returns the workspace's settings. A workspace with no state —
// never set up, state directory absent, or a record written by an older build
// that lacks today's fields — loads as fresh, unconfigured settings, so the
// workspace simply reports "Setup required" instead of erroring (FR-5).
//
// A record that exists but belongs to a different workspace is an error, not a
// fallback.
func (s *Store) LoadSettings(workspaceID string) (JanitorSettings, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	path, err := s.settingsPath(workspaceID)
	if err != nil {
		return JanitorSettings{}, err
	}
	// path is filepath.Join(resolver-provided workspace folder, two package
	// constants); workspaceID is a lookup key, not a path component.
	data, err := os.ReadFile(path) // #nosec G304 -- constructed from a resolved workspace folder plus fixed constants
	if err != nil {
		if os.IsNotExist(err) {
			return NewSettings(workspaceID), nil
		}
		return JanitorSettings{}, fmt.Errorf("failed to read downloads janitor settings: %w", err)
	}

	var stored JanitorSettings
	if err := json.Unmarshal(data, &stored); err != nil {
		// A corrupt record must not strand the workspace or, worse, be
		// interpreted as a partially configured one. Fail closed to
		// "Setup required" and let the user redo a two-minute setup.
		return NewSettings(workspaceID), nil
	}
	if id := strings.TrimSpace(stored.WorkspaceID); id != "" && id != workspaceID {
		return JanitorSettings{}, fmt.Errorf("%w: record is for %s", ErrWorkspaceMismatch, id)
	}
	stored.WorkspaceID = workspaceID
	return stored.Normalize(), nil
}

// SaveSettings validates and atomically persists the workspace's settings.
// Nothing partial is written: on any failure the previous record is left
// untouched.
func (s *Store) SaveSettings(settings JanitorSettings) error {
	workspaceID := strings.TrimSpace(settings.WorkspaceID)
	if workspaceID == "" {
		return fmt.Errorf("%w: workspace id is required", ErrInvalidSettings)
	}
	if err := settings.Validate(); err != nil {
		return err
	}
	lock := s.lockFor(workspaceID)
	lock.Lock()
	defer lock.Unlock()
	return s.saveSettingsLocked(settings)
}

func (s *Store) saveSettingsLocked(settings JanitorSettings) error {
	path, err := s.settingsPath(settings.WorkspaceID)
	if err != nil {
		return err
	}
	settings.SchemaVersion = SettingsSchemaVersion
	if settings.UpdatedAt.IsZero() {
		settings.UpdatedAt = time.Now()
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode downloads janitor settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("failed to create downloads janitor state directory: %w", err)
	}
	return atomicWriteFile(path, append(data, '\n'))
}

// UpdateSettings applies mutate to the workspace's current settings and
// persists the result, holding the workspace's lock across the whole
// read-modify-write so concurrent updates cannot clobber each other. It returns
// the settings as persisted. A mutate that returns an error leaves disk
// unchanged.
func (s *Store) UpdateSettings(workspaceID string, mutate func(*JanitorSettings) error) (JanitorSettings, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return JanitorSettings{}, fmt.Errorf("%w: workspace id is required", ErrInvalidSettings)
	}
	if mutate == nil {
		return s.LoadSettings(workspaceID)
	}
	lock := s.lockFor(workspaceID)
	lock.Lock()
	defer lock.Unlock()

	current, err := s.LoadSettings(workspaceID)
	if err != nil {
		return JanitorSettings{}, err
	}
	next := current
	if err := mutate(&next); err != nil {
		return JanitorSettings{}, err
	}
	next.WorkspaceID = workspaceID
	if err := next.Validate(); err != nil {
		return JanitorSettings{}, err
	}
	next.UpdatedAt = time.Now()
	if err := s.saveSettingsLocked(next); err != nil {
		return JanitorSettings{}, err
	}
	return next.Normalize(), nil
}

// atomicWriteFile writes data via a temp file in the destination directory
// followed by a rename, so a crash mid-write can never leave a truncated or
// half-updated record behind.
func atomicWriteFile(path string, data []byte) error {
	const perm os.FileMode = 0o600
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary state file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("failed to write state file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("failed to set state file permissions: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("failed to flush state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("failed to close state file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("failed to replace state file: %w", err)
	}
	return nil
}
