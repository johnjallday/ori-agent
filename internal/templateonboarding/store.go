package templateonboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SessionFileName is the workspace-folder sidecar that stores the canonical
// template-onboarding session state.
const SessionFileName = "template-onboarding.json"

var ErrSessionNotFound = errors.New("template onboarding session not found")

// FolderResolver is the small part of workspace.FileStore needed by this store.
type FolderResolver interface {
	GetFolderPath(workspaceID string) (string, error)
}

// SessionMirror is an optional best-effort mirror for secondary stores such as
// SQLite. The folder sidecar remains the source of truth.
type SessionMirror interface {
	SaveTemplateOnboardingSession(ctx context.Context, session *Session) error
}

// Store persists template-onboarding sessions in workspace folders.
type Store struct {
	resolver FolderResolver
	mirror   SessionMirror
}

// StoreOption configures a Store.
type StoreOption func(*Store)

// WithMirror adds a best-effort secondary mirror. Mirror errors are ignored
// after the sidecar write succeeds.
func WithMirror(mirror SessionMirror) StoreOption {
	return func(store *Store) {
		store.mirror = mirror
	}
}

// NewStore creates a sidecar-backed session store.
func NewStore(resolver FolderResolver, opts ...StoreOption) *Store {
	store := &Store{resolver: resolver}
	for _, opt := range opts {
		if opt != nil {
			opt(store)
		}
	}
	return store
}

// Save writes the session sidecar atomically. The workspace folder must already
// exist; workspace creation remains owned by the workspace store.
func (s *Store) Save(ctx context.Context, session *Session) error {
	if s == nil || s.resolver == nil {
		return fmt.Errorf("%w: folder resolver is required", ErrInvalidSession)
	}
	if session == nil {
		return fmt.Errorf("%w: session is required", ErrInvalidSession)
	}
	if strings.TrimSpace(session.WorkspaceID) == "" {
		return fmt.Errorf("%w: workspace_id is required", ErrInvalidSession)
	}
	session.normalize()
	path, err := s.Path(session.WorkspaceID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal template onboarding session: %w", err)
	}
	if err := atomicWriteFile(path, data); err != nil {
		return fmt.Errorf("write template onboarding session: %w", err)
	}
	if s.mirror != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		if cloned, cloneErr := session.Clone(); cloneErr == nil {
			_ = s.mirror.SaveTemplateOnboardingSession(ctx, cloned)
		}
	}
	return nil
}

// Load reads the canonical sidecar for a workspace.
func (s *Store) Load(ctx context.Context, workspaceID string) (*Session, error) {
	_ = ctx
	if s == nil || s.resolver == nil {
		return nil, fmt.Errorf("%w: folder resolver is required", ErrInvalidSession)
	}
	path, err := s.Path(workspaceID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is resolver.GetFolderPath(workspaceID)+fixed SessionFileName.
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("read template onboarding session: %w", err)
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parse template onboarding session: %w", err)
	}
	session.normalize()
	if strings.TrimSpace(session.WorkspaceID) == "" {
		return nil, fmt.Errorf("%w: workspace_id is required", ErrInvalidSession)
	}
	if !IsKnownStatus(session.Status) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidStatus, session.Status)
	}
	return &session, nil
}

// Path returns the sidecar file path for a workspace.
func (s *Store) Path(workspaceID string) (string, error) {
	if s == nil || s.resolver == nil {
		return "", fmt.Errorf("%w: folder resolver is required", ErrInvalidSession)
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return "", fmt.Errorf("%w: workspace_id is required", ErrInvalidSession)
	}
	folder, err := s.resolver.GetFolderPath(workspaceID)
	if err != nil {
		return "", fmt.Errorf("resolve workspace folder: %w", err)
	}
	if strings.TrimSpace(folder) == "" {
		return "", fmt.Errorf("resolve workspace folder: empty path")
	}
	return filepath.Join(folder, SessionFileName), nil
}

func atomicWriteFile(path string, data []byte) error {
	const perm os.FileMode = 0o644
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
