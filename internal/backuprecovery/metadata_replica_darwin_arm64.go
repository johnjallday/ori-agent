//go:build darwin && arm64

package backuprecovery

import (
	"context"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

type replicaDirectory struct {
	file     *os.File
	parent   *os.File
	name     string
	identity anchoredIdentity
}

type replicaFile struct {
	parent *os.File
	name   string
	stat   unix.Stat_t
}

// metadataReplica owns only newly/exclusively created scratch. Every write and
// cleanup is descriptor-relative. It never stages plaintext metadata/passwords,
// traverses source data, or recursively deletes a selected directory.
type metadataReplica struct {
	path        string
	parent      *os.File
	directories []replicaDirectory
	files       []replicaFile
}

// Must run in the bounded helper, using its composition-owned private scratch
// parent. This is not a user-configurable destination or filesystem write API.
func stageMetadataReplica(ctx context.Context, parentPath string, bundle metadataBundle) (*metadataReplica, error) {
	if ctx == nil || ctx.Err() != nil || !bundle.valid() || !validMetadataPath(parentPath) {
		return nil, errMetadataBundle
	}
	parent, err := openExecutableParent(parentPath)
	if err != nil {
		return nil, errMetadataBundle
	}
	info, err := statAnchor(parent)
	canonical, pathErr := searchDirectoryPath(parent)
	if err != nil || pathErr != nil || canonical != parentPath || int64(info.Uid) != int64(os.Geteuid()) || info.Mode&0022 != 0 {
		_ = parent.Close()
		return nil, errMetadataBundle
	}
	replica := &metadataReplica{parent: parent, path: filepath.Join(parentPath, "backup-read-"+uuid.NewString())}
	fail := func() (*metadataReplica, error) { _ = replica.close(); return nil, errMetadataBundle }
	root, err := replica.mkdir(parent, filepath.Base(replica.path))
	if err != nil {
		return fail()
	}
	repository, err := replica.mkdir(root, "repository")
	if err != nil {
		return fail()
	}
	if _, err := replica.mkdir(root, "home"); err != nil {
		return fail()
	}
	if _, err := replica.mkdir(root, "tmp"); err != nil {
		return fail()
	}
	keys, err := replica.mkdir(repository, "keys")
	if err != nil {
		return fail()
	}
	snapshots, err := replica.mkdir(repository, "snapshots")
	if err != nil {
		return fail()
	}
	for _, entry := range bundle.entries {
		if ctx.Err() != nil {
			return fail()
		}
		directory, name := repository, "config"
		switch entry.kind {
		case metadataKey:
			directory, name = keys, entry.id
		case metadataSnapshot:
			directory, name = snapshots, entry.id
		}
		fd, err := unix.Openat(int(directory.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		if err != nil {
			return fail()
		}
		file := os.NewFile(uintptr(fd), "backup-encrypted-replica")
		n, writeErr := file.Write(entry.data)
		stat, statErr := statAnchor(file)
		closeErr := file.Close()
		if statErr == nil {
			replica.files = append(replica.files, replicaFile{parent: directory, name: name, stat: stat})
		}
		if writeErr != nil || closeErr != nil || statErr != nil || n != len(entry.data) || !privateReplicaFile(stat) {
			return fail()
		}
	}
	if !replica.unchanged() {
		return fail()
	}
	return replica, nil
}

func privateReplicaFile(info unix.Stat_t) bool {
	return info.Dev > 0 && info.Ino != 0 && info.Mode&unix.S_IFMT == unix.S_IFREG && info.Mode&0777 == 0600 && info.Nlink == 1 && int64(info.Uid) == int64(os.Geteuid())
}

func (replica *metadataReplica) mkdir(parent *os.File, name string) (*os.File, error) {
	if unix.Mkdirat(int(parent.Fd()), name, 0700) != nil {
		return nil, errMetadataBundle
	}
	fd, err := unix.Openat(int(parent.Fd()), name, directoryTraversalFlags(), 0)
	if err != nil {
		return nil, errMetadataBundle
	}
	file := os.NewFile(uintptr(fd), "backup-private-scratch")
	info, err := statAnchor(file)
	if err != nil || info.Mode&unix.S_IFMT != unix.S_IFDIR || info.Mode&0777 != 0700 || int64(info.Uid) != int64(os.Geteuid()) || anchorID(info).device == 0 {
		_ = file.Close()
		return nil, errMetadataBundle
	}
	replica.directories = append(replica.directories, replicaDirectory{file: file, parent: parent, name: name, identity: anchorID(info)})
	return file, nil
}

func (replica *metadataReplica) unchanged() bool {
	if replica == nil || replica.parent == nil || len(replica.directories) != 6 {
		return false
	}
	path, err := openSearchDirectory(replica.path)
	if err != nil {
		return false
	}
	defer func() { _ = path.Close() }()
	linked, err := statAnchor(path)
	if err != nil || anchorID(linked) != replica.directories[0].identity {
		return false
	}
	for _, dir := range replica.directories {
		held, err := statAnchor(dir.file)
		var current unix.Stat_t
		if err != nil || unix.Fstatat(int(dir.parent.Fd()), dir.name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || anchorID(held) != dir.identity || anchorID(current) != dir.identity {
			return false
		}
	}
	for _, file := range replica.files {
		var current unix.Stat_t
		if unix.Fstatat(int(file.parent.Fd()), file.name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || !privateReplicaFile(current) || !metadataStatEqual(file.stat, current) {
			return false
		}
	}
	return true
}

// Stop/reap all children before calling close. Changed/unexpected files are left
// intact and reported unavailable, never recursively followed or deleted.
func (replica *metadataReplica) close() error {
	if replica == nil || replica.parent == nil {
		return nil
	}
	ok := true
	for _, file := range replica.files {
		var current unix.Stat_t
		if unix.Fstatat(int(file.parent.Fd()), file.name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || !metadataStatEqual(file.stat, current) || unix.Unlinkat(int(file.parent.Fd()), file.name, 0) != nil {
			ok = false
		}
	}
	for i := len(replica.directories) - 1; i >= 0; i-- {
		dir := replica.directories[i]
		var current unix.Stat_t
		if unix.Fstatat(int(dir.parent.Fd()), dir.name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || anchorID(current) != dir.identity || unix.Unlinkat(int(dir.parent.Fd()), dir.name, unix.AT_REMOVEDIR) != nil {
			ok = false
		}
		_ = dir.file.Close()
	}
	_ = replica.parent.Close()
	replica.parent = nil
	if !ok {
		return errMetadataBundle
	}
	return nil
}
