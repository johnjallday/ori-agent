//go:build !windows

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
	"sync"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"golang.org/x/sys/unix"
)

// Creating the stable advisory lock must itself be serialized across store
// instances. macOS openat can return ENOENT when two descriptors concurrently
// create the same no-follow lockfile; flock coordinates the later operations.
var knowledgeLockCreation sync.Mutex

// KnowledgeStore reads/writes lifecycle metadata under a *resolved* HQ. The
// only public identity input is userID; it never accepts a path or caller-
// supplied binding. Store instances coordinate through a stable advisory lock
// file and fresh disk reads (not a process-local cache).
type KnowledgeStore struct {
	resolver *KnowledgeResolver
	folders  workspace.FolderResolver
	// Test-only failure seam. No product caller may configure it.
	beforeRename func() error
}

func NewKnowledgeStore(resolver *KnowledgeResolver, folders workspace.FolderResolver) *KnowledgeStore {
	return &KnowledgeStore{resolver: resolver, folders: folders}
}

func (s *KnowledgeStore) Read(ctx context.Context, userID string) (KnowledgeDocument, error) {
	binding, err := s.resolve(ctx, userID)
	if err != nil {
		return KnowledgeDocument{}, err
	}
	dir, err := s.openDir(binding, false)
	if err != nil {
		return KnowledgeDocument{}, err
	}
	if dir == nil {
		return KnowledgeDocument{SchemaVersion: KnowledgeSchemaVersion, Owner: binding.owner()}, nil
	}
	defer func() { _ = dir.Close() }()
	doc, err := readKnowledge(dir, binding.owner())
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
	dir, err := s.openDir(binding, true)
	if err != nil {
		return KnowledgeDocument{}, err
	}
	defer func() { _ = dir.Close() }()
	knowledgeLockCreation.Lock()
	lock, err := openKnowledgeChild(dir, knowledgeLockName, unix.O_CREAT|unix.O_RDWR, 0o600)
	knowledgeLockCreation.Unlock()
	if err != nil {
		return KnowledgeDocument{}, fmt.Errorf("personal assistant: open knowledge lock: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return KnowledgeDocument{}, fmt.Errorf("personal assistant: lock knowledge: %w", err)
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()

	if err := s.checkBinding(ctx, binding); err != nil {
		return KnowledgeDocument{}, err
	}
	doc, err := readKnowledge(dir, binding.owner())
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
	encoded, err := encodeKnowledge(doc)
	if err != nil {
		return KnowledgeDocument{}, err
	}
	if err := s.write(dir, encoded); err != nil {
		return KnowledgeDocument{}, err
	}
	if err := s.checkBinding(ctx, binding); err != nil {
		return KnowledgeDocument{}, err
	}
	return cloneKnowledge(doc), nil
}

func (s *KnowledgeStore) resolve(ctx context.Context, userID string) (KnowledgeBinding, error) {
	if s == nil || s.resolver == nil || s.folders == nil {
		return KnowledgeBinding{}, ErrRepairNeeded
	}
	return s.resolver.Resolve(ctx, userID)
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

// openDir uses no-follow, directory-relative handles. Even if a workspace
// contains an attacker-controlled `.ori` symlink, neither reads nor writes
// follow it to another folder. The folder path is supplied only by the
// workspace store using the server-validated HQ ID.
func (s *KnowledgeStore) openDir(binding KnowledgeBinding, create bool) (*os.File, error) {
	root, err := s.folders.GetFolderPath(binding.HQWorkspaceID)
	if err != nil {
		return nil, err
	}
	folder, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0) // #nosec G304 -- root comes from server workspace ID lookup
	if err != nil {
		return nil, fmt.Errorf("personal assistant: open hq folder: %w", err)
	}
	defer func() { _ = unix.Close(folder) }()
	if create {
		if err := unix.Mkdirat(folder, workspace.SidecarDirName, 0o750); err != nil && !errors.Is(err, unix.EEXIST) {
			return nil, fmt.Errorf("personal assistant: create knowledge directory: %w", err)
		}
	}
	fd, err := unix.Openat(folder, workspace.SidecarDirName, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) && !create {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("personal assistant: open knowledge directory: %w", err)
	}
	var info unix.Stat_t
	// The OS process UID and stat UID use the same nonnegative kernel UID range.
	// #nosec G115 -- Getuid cannot exceed the kernel UID range used by Stat_t.Uid.
	if err := unix.Fstat(fd, &info); err != nil || info.Mode&unix.S_IFMT != unix.S_IFDIR || info.Mode&0o002 != 0 || info.Uid != uint32(os.Getuid()) {
		_ = unix.Close(fd)
		return nil, ErrKnowledgeCorrupt
	}
	return os.NewFile(uintptr(fd), filepath.Join(root, workspace.SidecarDirName)), nil
}

func openKnowledgeChild(dir *os.File, name string, flags int, mode uint32) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, mode)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Mode&0o077 != 0 {
		_ = file.Close()
		return nil, ErrKnowledgeCorrupt
	}
	return file, nil
}

func readKnowledge(dir *os.File, owner KnowledgeOwner) (KnowledgeDocument, error) {
	missing := KnowledgeDocument{SchemaVersion: KnowledgeSchemaVersion, Owner: owner}
	file, err := openKnowledgeChild(dir, knowledgeFileName, unix.O_RDONLY, 0)
	if errors.Is(err, unix.ENOENT) {
		return missing, nil
	}
	if err != nil {
		return KnowledgeDocument{}, fmt.Errorf("%w: open sidecar", ErrKnowledgeCorrupt)
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

func encodeKnowledge(doc KnowledgeDocument) ([]byte, error) {
	if err := validateKnowledge(doc); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if len(data) > knowledgeMaxBytes {
		return nil, ErrKnowledgeLimit
	}
	return data, nil
}

func (s *KnowledgeStore) write(dir *os.File, data []byte) error {
	return s.writeNamed(dir, knowledgeFileName, ".personal-assistant-knowledge-", data)
}

// writeNamed replaces one sidecar file under the open .ori directory: a
// no-follow temp file with mode 0600, written and fsynced, renamed over the
// target only if the target is absent or a regular file, then the directory
// itself is fsynced. Every sidecar document shares this path.
func (s *KnowledgeStore) writeNamed(dir *os.File, fileName, tempPrefix string, data []byte) error {
	name := tempPrefix + uuid.NewString()
	file, err := openKnowledgeChild(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("personal assistant: create sidecar temp: %w", err)
	}
	defer func() { _ = unix.Unlinkat(int(dir.Fd()), name, 0) }()
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
	// Refuse a symlink (or other nonregular target) before replacing it.
	// A concurrent local writer must cooperate with the advisory lock.
	old, err := openKnowledgeChild(dir, fileName, unix.O_RDONLY, 0)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return ErrKnowledgeCorrupt
	}
	if old != nil {
		_ = old.Close()
	}
	if err := unix.Renameat(int(dir.Fd()), name, int(dir.Fd()), fileName); err != nil {
		return fmt.Errorf("personal assistant: replace sidecar: %w", err)
	}
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("personal assistant: sync sidecar directory: %w", err)
	}
	return nil
}

// readSidecarBytes reads one bounded sidecar file under the open .ori
// directory. present is false when the file does not exist yet.
func readSidecarBytes(dir *os.File, fileName string, maxBytes int) (data []byte, present bool, err error) {
	file, err := openKnowledgeChild(dir, fileName, unix.O_RDONLY, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%w: open sidecar", ErrKnowledgeCorrupt)
	}
	defer func() { _ = file.Close() }()
	data, err = io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil || len(data) > maxBytes {
		return nil, false, ErrKnowledgeCorrupt
	}
	return data, true, nil
}

// lockSidecar takes the exclusive advisory lock named lockName under the open
// .ori directory, creating it when create is set. The returned release must
// be called once the critical section ends.
func lockSidecar(dir *os.File, lockName string, create bool) (release func(), err error) {
	flags := unix.O_RDWR
	if create {
		flags |= unix.O_CREAT
	}
	knowledgeLockCreation.Lock()
	lock, err := openKnowledgeChild(dir, lockName, flags, 0o600)
	knowledgeLockCreation.Unlock()
	if err != nil {
		return nil, fmt.Errorf("personal assistant: open sidecar lock: %w", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("personal assistant: lock sidecar: %w", err)
	}
	return func() {
		_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
		_ = lock.Close()
	}, nil
}
