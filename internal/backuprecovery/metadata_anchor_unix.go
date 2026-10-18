//go:build darwin || linux

package backuprecovery

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// metadataAnchor is the descriptor boundary for the future read broker. It must
// run in a separately cancellable, sandboxed child: a local filesystem syscall
// cannot be made cancellable merely by putting it in a server goroutine. Linux
// builds exercise descriptor tests only; they do not enable provider support.
type metadataAnchor struct {
	path     string
	root     *os.File
	identity anchoredIdentity
}

func validMetadataPath(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" ||
		len(path) > maxSourcePathBytes || !utf8.ValidString(path) {
		return false
	}
	return !strings.ContainsFunc(path, func(r rune) bool { return unicode.IsControl(r) || r == unicode.ReplacementChar })
}

func statAnchor(file *os.File) (unix.Stat_t, error) {
	var info unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &info); err != nil {
		return unix.Stat_t{}, errMetadataBundle
	}
	return info, nil
}

func anchorID(info unix.Stat_t) anchoredIdentity {
	if info.Dev <= 0 || info.Ino == 0 {
		return anchoredIdentity{}
	}
	return anchoredIdentity{device: uint64(info.Dev), inode: info.Ino}
}

func openMetadataDirectory(path string) (*os.File, error) {
	if !validMetadataPath(path) {
		return nil, errMetadataBundle
	}
	flags := directoryTraversalFlags()
	fd, err := unix.Open("/", flags, 0)
	if err != nil {
		return nil, errMetadataBundle
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, err := unix.Openat(fd, part, flags, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, errMetadataBundle
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "backup-metadata-directory"), nil
}

func newMetadataAnchor(path string, expected anchoredIdentity) (*metadataAnchor, error) {
	if expected.device == 0 || expected.inode == 0 {
		return nil, errMetadataBundle
	}
	root, err := openMetadataDirectory(path)
	if err != nil {
		return nil, errMetadataBundle
	}
	info, err := statAnchor(root)
	if err != nil || anchorID(info) != expected || info.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = root.Close()
		return nil, errMetadataBundle
	}
	return &metadataAnchor{path: path, root: root, identity: expected}, nil
}

func (anchor *metadataAnchor) close() {
	if anchor != nil && anchor.root != nil {
		_ = anchor.root.Close()
	}
}

func (anchor *metadataAnchor) unchanged() bool {
	if anchor == nil || anchor.root == nil {
		return false
	}
	current, err := openMetadataDirectory(anchor.path)
	if err != nil {
		return false
	}
	defer func() { _ = current.Close() }()
	info, err := statAnchor(current)
	return err == nil && anchorID(info) == anchor.identity
}

func metadataStatEqual(a, b unix.Stat_t) bool {
	// Atime is intentionally excluded: reading is allowed to update it.
	id := anchorID(a)
	return id.device != 0 && id.inode != 0 && id == anchorID(b) && a.Mode == b.Mode && a.Nlink == b.Nlink &&
		a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

func (anchor *metadataAnchor) readFile(ctx context.Context, parent *os.File, name string, limit, remaining int) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, errMetadataBundle
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, errMetadataBundle
	}
	file := os.NewFile(uintptr(fd), "backup-encrypted-metadata")
	defer func() { _ = file.Close() }()
	before, err := statAnchor(file)
	if err != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 ||
		anchorID(before).device != anchor.identity.device || before.Size <= 0 || before.Size > int64(limit) || before.Size > int64(remaining) {
		return nil, errMetadataBundle
	}
	data, err := io.ReadAll(io.LimitReader(file, before.Size+1))
	if err != nil || ctx.Err() != nil || int64(len(data)) != before.Size {
		return nil, errMetadataBundle
	}
	after, err := statAnchor(file)
	var current unix.Stat_t
	if err != nil || unix.Fstatat(int(parent.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!metadataStatEqual(before, after) || !metadataStatEqual(after, current) {
		return nil, errMetadataBundle
	}
	return data, nil
}

func (anchor *metadataAnchor) collect(ctx context.Context) (metadataBundle, error) {
	if ctx == nil || ctx.Err() != nil || !anchor.unchanged() {
		return metadataBundle{}, errMetadataBundle
	}
	var configBefore unix.Stat_t
	if unix.Fstatat(int(anchor.root.Fd()), "config", &configBefore, unix.AT_SYMLINK_NOFOLLOW) != nil {
		return metadataBundle{}, errMetadataBundle
	}
	config, err := anchor.readFile(ctx, anchor.root, "config", maxEncryptedConfigBytes, maxEncryptedMetadataBytes)
	if err != nil {
		return metadataBundle{}, errMetadataBundle
	}
	bundle := metadataBundle{entries: []metadataEntry{{kind: metadataConfig, data: config}}}
	total := len(config)
	for _, section := range []struct {
		name             string
		kind             byte
		count, fileLimit int
	}{{"keys", metadataKey, maxMetadataKeyCount, maxEncryptedKeyBytes},
		{"snapshots", metadataSnapshot, maxSnapshotCount, maxEncryptedSnapshotBytes}} {
		entries, err := anchor.collectSection(ctx, section.name, section.kind, section.count, section.fileLimit, &total)
		if err != nil {
			return metadataBundle{}, errMetadataBundle
		}
		bundle.entries = append(bundle.entries, entries...)
	}
	// Config may be atomically replaced while keys/snapshots are collected.
	finalConfig, err := anchor.readFile(ctx, anchor.root, "config", maxEncryptedConfigBytes, maxEncryptedConfigBytes)
	var configAfter unix.Stat_t
	if err != nil || unix.Fstatat(int(anchor.root.Fd()), "config", &configAfter, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!metadataStatEqual(configBefore, configAfter) || !bytes.Equal(config, finalConfig) || !anchor.unchanged() || !bundle.valid() {
		return metadataBundle{}, errMetadataBundle
	}
	return bundle, nil
}

func (anchor *metadataAnchor) collectSection(ctx context.Context, name string, kind byte, count, fileLimit int, total *int) ([]metadataEntry, error) {
	if ctx.Err() != nil {
		return nil, errMetadataBundle
	}
	fd, err := unix.Openat(int(anchor.root.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, errMetadataBundle
	}
	dir := os.NewFile(uintptr(fd), "backup-metadata-section")
	defer func() { _ = dir.Close() }()
	before, err := statAnchor(dir)
	if err != nil || anchorID(before).device != anchor.identity.device {
		return nil, errMetadataBundle
	}
	// Limit enumeration itself, not only the list after allocating it.
	names, err := dir.Readdirnames(count + 1)
	if err != nil && err != io.EOF || len(names) > count {
		return nil, errMetadataBundle
	}
	sort.Strings(names)
	entries := make([]metadataEntry, 0, len(names))
	for _, entryName := range names {
		if !metadataID(entryName) {
			return nil, errMetadataBundle
		}
		data, err := anchor.readFile(ctx, dir, entryName, fileLimit, maxEncryptedMetadataBytes-*total)
		if err != nil {
			return nil, errMetadataBundle
		}
		*total += len(data)
		entries = append(entries, metadataEntry{kind: kind, id: entryName, data: data})
	}
	after, err := statAnchor(dir)
	var current unix.Stat_t
	if err != nil || unix.Fstatat(int(anchor.root.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!metadataStatEqual(before, after) || !metadataStatEqual(after, current) {
		return nil, errMetadataBundle
	}
	return entries, nil
}
