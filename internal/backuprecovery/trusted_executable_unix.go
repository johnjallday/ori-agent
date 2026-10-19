//go:build darwin || linux

package backuprecovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	resticReleaseSHA256       = "06582569ff2f10e1935a6f12187c76db02f0bae99e6e54098f2cdc374766d768"
	maxTrustedExecutableBytes = 128 << 20
	maxApplicationHelperBytes = 256 << 20 // the measured full server image exceeds restic's separate 128 MiB bound
)

// verifiedExecutable is a descriptor/digest/path check, not an execution API.
// It MUST run inside the bounded compiled helper: filesystem calls themselves
// are not made cancellable by a Go context. Linux supports fixture tests only.
// Production composition must select the fixed release digest and platform,
// retain the descriptor, and check again around each sandboxed invocation.
type verifiedExecutable struct {
	path     string
	file     *os.File
	before   unix.Stat_t
	digest   [sha256.Size]byte
	maxBytes int64
}

func verifyResticExecutable(ctx context.Context, path string) (*verifiedExecutable, error) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return nil, errTrustedExecutable
	}
	decoded, err := hex.DecodeString(resticReleaseSHA256)
	if err != nil || len(decoded) != sha256.Size {
		return nil, errTrustedExecutable
	}
	var digest [sha256.Size]byte
	copy(digest[:], decoded)
	return verifyExecutable(ctx, path, digest)
}

// The digest parameter is an internal test/composition seam, not a setting.
// The production restic entrypoint above always supplies the pinned digest.
func verifyExecutable(ctx context.Context, path string, digest [sha256.Size]byte) (*verifiedExecutable, error) {
	return verifyExecutableWithin(ctx, path, digest, maxTrustedExecutableBytes)
}

func verifyExecutableWithin(ctx context.Context, path string, digest [sha256.Size]byte, maxBytes int64) (*verifiedExecutable, error) {
	if ctx == nil || ctx.Err() != nil || !validMetadataPath(path) || digest == ([sha256.Size]byte{}) || (maxBytes != maxTrustedExecutableBytes && maxBytes != maxApplicationHelperBytes) {
		return nil, errTrustedExecutable
	}
	file, err := openExecutableNoFollow(path)
	if err != nil {
		return nil, errTrustedExecutable
	}
	info, err := statAnchor(file)
	if err != nil || !trustedExecutableStatWithin(info, maxBytes) {
		_ = file.Close()
		return nil, errTrustedExecutable
	}
	checked := &verifiedExecutable{path: path, file: file, before: info, digest: digest, maxBytes: maxBytes}
	if !checked.unchanged(ctx) {
		checked.close()
		return nil, errTrustedExecutable
	}
	return checked, nil
}

func openExecutableNoFollow(path string) (*os.File, error) {
	if !validMetadataPath(path) {
		return nil, errTrustedExecutable
	}
	parent, err := openExecutableParent(filepath.Dir(path))
	if err != nil {
		return nil, errTrustedExecutable
	}
	defer func() { _ = parent.Close() }()
	fd, err := unix.Openat(int(parent.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errTrustedExecutable
	}
	return os.NewFile(uintptr(fd), "backup-executable"), nil
}

// Reject other-user-controlled or writable ancestors as well as symlinks.
// A root-owned sticky temporary directory is allowed: each following component
// must still be owned by root/this user and not writable by other users.
func openExecutableParent(path string) (*os.File, error) {
	if path != "/" && !validMetadataPath(path) {
		return nil, errTrustedExecutable
	}
	flags := directoryTraversalFlags()
	fd, err := unix.Open("/", flags, 0)
	if err != nil {
		return nil, errTrustedExecutable
	}
	parts := []string{}
	if path != "/" {
		parts = strings.Split(strings.TrimPrefix(path, "/"), "/")
	}
	for index := 0; ; index++ {
		var info unix.Stat_t
		if err := unix.Fstat(fd, &info); err != nil || info.Mode&unix.S_IFMT != unix.S_IFDIR ||
			(info.Uid != 0 && int64(info.Uid) != int64(os.Geteuid())) ||
			(info.Mode&0o022 != 0 && (info.Uid != 0 || info.Mode&unix.S_ISVTX == 0)) {
			_ = unix.Close(fd)
			return nil, errTrustedExecutable
		}
		if index == len(parts) {
			break
		}
		next, err := unix.Openat(fd, parts[index], flags, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, errTrustedExecutable
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "backup-executable-parent"), nil
}

func trustedExecutableStat(info unix.Stat_t) bool {
	return trustedExecutableStatWithin(info, maxTrustedExecutableBytes)
}

func trustedExecutableStatWithin(info unix.Stat_t, maxBytes int64) bool {
	return (maxBytes == maxTrustedExecutableBytes || maxBytes == maxApplicationHelperBytes) && info.Dev > 0 && info.Ino != 0 && info.Mode&unix.S_IFMT == unix.S_IFREG && info.Mode&0o022 == 0 && info.Mode&0o111 != 0 &&
		info.Mode&(unix.S_ISUID|unix.S_ISGID|unix.S_ISVTX) == 0 &&
		info.Nlink == 1 && info.Size > 0 && info.Size <= maxBytes &&
		(info.Uid == 0 || int64(info.Uid) == int64(os.Geteuid()))
}

func (checked *verifiedExecutable) close() {
	if checked != nil && checked.file != nil {
		_ = checked.file.Close()
	}
}

func (checked *verifiedExecutable) unchanged(ctx context.Context) bool {
	if ctx == nil || ctx.Err() != nil || checked == nil || checked.file == nil {
		return false
	}
	before, err := statAnchor(checked.file)
	if err != nil || !trustedExecutableStatWithin(before, checked.maxBytes) || !metadataStatEqual(checked.before, before) {
		return false
	}
	hash := sha256.New()
	reader := io.NewSectionReader(checked.file, 0, before.Size)
	buffer := make([]byte, 32<<10)
	for {
		if ctx.Err() != nil {
			return false
		}
		n, err := reader.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return false
		}
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	after, err := statAnchor(checked.file)
	if err != nil || ctx.Err() != nil || digest != checked.digest || !metadataStatEqual(before, after) {
		return false
	}
	current, err := openExecutableNoFollow(checked.path)
	if err != nil {
		return false
	}
	defer func() { _ = current.Close() }()
	linked, err := statAnchor(current)
	return err == nil && metadataStatEqual(after, linked)
}
