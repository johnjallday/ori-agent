//go:build windows

package personalassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"golang.org/x/sys/windows"
)

// KnowledgeStore is the Windows counterpart to the no-follow Unix store. It
// rejects reparse-point/symlink directories and files and serializes updates
// through a stable Windows file lock. A local user who can replace a workspace
// folder concurrently is outside this process's filesystem trust boundary.
type KnowledgeStore struct {
	resolver     *KnowledgeResolver
	folders      workspace.FolderResolver
	beforeRename func() error // test-only failure seam
}

func NewKnowledgeStore(resolver *KnowledgeResolver, folders workspace.FolderResolver) *KnowledgeStore {
	return &KnowledgeStore{resolver: resolver, folders: folders}
}

func (s *KnowledgeStore) resolve(ctx context.Context, userID string) (KnowledgeBinding, error) {
	if s == nil || s.resolver == nil || s.folders == nil {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	return s.resolver.Resolve(ctx, userID)
}

func (s *KnowledgeStore) path(binding KnowledgeBinding, create bool) (string, error) {
	folder, err := s.folders.GetFolderPath(binding.HQWorkspaceID)
	if err != nil {
		return "", err
	}
	if err := requireKnowledgeDir(folder); err != nil {
		return "", err
	}
	dir := filepath.Join(folder, workspace.SidecarDirName)
	if create {
		if err := os.Mkdir(dir, 0o750); err != nil && !os.IsExist(err) {
			return "", err
		}
	}
	if err := requireKnowledgeDir(dir); err != nil {
		if os.IsNotExist(err) && !create {
			return "", nil
		}
		return "", err
	}
	return dir, nil
}

func requireKnowledgeDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeIrregular != 0 {
		return ErrKnowledgeCorrupt
	}
	return nil
}

func readKnowledgeWindows(dir string, owner KnowledgeOwner) (KnowledgeDocument, error) {
	missing := KnowledgeDocument{SchemaVersion: KnowledgeSchemaVersion, Owner: owner}
	path := filepath.Join(dir, knowledgeFileName)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return missing, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return KnowledgeDocument{}, ErrKnowledgeCorrupt
	}
	file, err := os.Open(path) // #nosec G304 -- fixed filename under server-resolved, checked HQ folder
	if err != nil {
		return KnowledgeDocument{}, ErrKnowledgeCorrupt
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, knowledgeMaxBytes+1))
	if err != nil || len(data) > knowledgeMaxBytes {
		return KnowledgeDocument{}, ErrKnowledgeCorrupt
	}
	var doc KnowledgeDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return KnowledgeDocument{}, ErrKnowledgeCorrupt
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || doc.SchemaVersion != KnowledgeSchemaVersion || doc.Version < 1 || doc.Owner != owner {
		return KnowledgeDocument{}, ErrKnowledgeCorrupt
	}
	doc.Present = true
	if err := validateKnowledge(doc); err != nil {
		return KnowledgeDocument{}, ErrKnowledgeCorrupt
	}
	return doc, nil
}

func (s *KnowledgeStore) checkBinding(ctx context.Context, before KnowledgeBinding) error {
	now, err := s.resolve(ctx, before.UserID)
	if err != nil {
		return err
	}
	if now != before {
		return ErrConflict
	}
	return nil
}

func (s *KnowledgeStore) Read(ctx context.Context, userID string) (KnowledgeDocument, error) {
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return KnowledgeDocument{}, err
	}
	dir, err := s.path(binding, false)
	if err != nil {
		return KnowledgeDocument{}, err
	}
	if dir == "" {
		return KnowledgeDocument{SchemaVersion: KnowledgeSchemaVersion, Owner: binding.owner()}, nil
	}
	doc, err := readKnowledgeWindows(dir, binding.owner())
	if err != nil {
		return KnowledgeDocument{}, err
	}
	if err := s.checkBinding(ctx, binding); err != nil {
		return KnowledgeDocument{}, err
	}
	return doc, nil
}

func (s *KnowledgeStore) Update(ctx context.Context, userID string, expectedVersion int64, mutate func(*KnowledgeDocument) error) (KnowledgeDocument, error) {
	if mutate == nil || expectedVersion < 0 {
		return KnowledgeDocument{}, ErrConflict
	}
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return KnowledgeDocument{}, err
	}
	dir, err := s.path(binding, true)
	if err != nil {
		return KnowledgeDocument{}, err
	}
	lockPath := filepath.Join(dir, knowledgeLockName)
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- fixed lock under server-resolved HQ
	if err != nil {
		return KnowledgeDocument{}, err
	}
	defer func() { _ = lock.Close() }()
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() {
		return KnowledgeDocument{}, ErrKnowledgeCorrupt
	}
	overlap := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(lock.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlap); err != nil {
		return KnowledgeDocument{}, fmt.Errorf("personal assistant: lock knowledge: %w", err)
	}
	defer func() { _ = windows.UnlockFileEx(windows.Handle(lock.Fd()), 0, 1, 0, overlap) }()
	if err := s.checkBinding(ctx, binding); err != nil {
		return KnowledgeDocument{}, err
	}
	doc, err := readKnowledgeWindows(dir, binding.owner())
	if err != nil {
		return KnowledgeDocument{}, err
	}
	if doc.Version != expectedVersion {
		return KnowledgeDocument{}, ErrConflict
	}
	if err := mutate(&doc); err != nil {
		return KnowledgeDocument{}, err
	}
	if err := s.checkBinding(ctx, binding); err != nil {
		return KnowledgeDocument{}, err
	}
	doc.Version++
	doc.SchemaVersion = KnowledgeSchemaVersion
	doc.Owner = binding.owner()
	doc.Present = true
	if err := validateKnowledge(doc); err != nil {
		return KnowledgeDocument{}, err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return KnowledgeDocument{}, err
	}
	data = append(data, '\n')
	if len(data) > knowledgeMaxBytes {
		return KnowledgeDocument{}, ErrKnowledgeLimit
	}
	// An existing corrupt/symlinked file cannot be replaced as a repair.
	if _, err := readKnowledgeWindows(dir, binding.owner()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return KnowledgeDocument{}, err
	}
	if err := s.writeNamed(dir, knowledgeFileName, ".personal-assistant-knowledge-", data); err != nil {
		return KnowledgeDocument{}, err
	}
	return cloneKnowledge(doc), s.checkBinding(ctx, binding)
}

// writeNamed replaces one sidecar file under the .ori directory through a
// synced temp file and a rename. Every sidecar document shares this path.
func (s *KnowledgeStore) writeNamed(dir, fileName, tempPrefix string, data []byte) error {
	file, err := os.CreateTemp(dir, tempPrefix)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if s.beforeRename != nil {
		if err := s.beforeRename(); err != nil {
			return err
		}
	}
	return os.Rename(file.Name(), filepath.Join(dir, fileName))
}

// readSidecarBytes reads one bounded sidecar file under the .ori directory.
// present is false when the file does not exist yet.
func readSidecarBytes(dir, fileName string, maxBytes int) (data []byte, present bool, err error) {
	path := filepath.Join(dir, fileName)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, false, ErrKnowledgeCorrupt
	}
	file, err := os.Open(path) // #nosec G304 -- fixed filename under server-resolved, checked HQ folder
	if err != nil {
		return nil, false, ErrKnowledgeCorrupt
	}
	defer func() { _ = file.Close() }()
	data, err = io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil || len(data) > maxBytes {
		return nil, false, ErrKnowledgeCorrupt
	}
	return data, true, nil
}

// lockSidecar takes the exclusive lock named lockName under the .ori
// directory, creating it when create is set.
func lockSidecar(dir, lockName string, create bool) (release func(), err error) {
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	lock, err := os.OpenFile(filepath.Join(dir, lockName), flags, 0o600) // #nosec G304 -- fixed lock under server-resolved HQ
	if err != nil {
		return nil, err
	}
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = lock.Close()
		return nil, ErrKnowledgeCorrupt
	}
	overlap := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(lock.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlap); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("personal assistant: lock sidecar: %w", err)
	}
	return func() {
		_ = windows.UnlockFileEx(windows.Handle(lock.Fd()), 0, 1, 0, overlap)
		_ = lock.Close()
	}, nil
}
