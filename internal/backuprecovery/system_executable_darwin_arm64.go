//go:build darwin && arm64

package backuprecovery

import (
	"os"

	"golang.org/x/sys/unix"
)

type systemExecutableKind uint8

const (
	systemContainmentLauncher systemExecutableKind = iota + 1
	systemDiskMetadataReader
	systemProductMetadata
	systemTimeMachineUtility // only its two read verbs, under the Time Machine profile
)

// These are fixed OS-owned entrypoints, not configuration/PATH lookup. They
// must reside on a local read-only APFS filesystem, be root-owned and remain
// descriptor/path bound. Validation belongs inside the bounded helper. This
// trusts the protected OS, not an ad-hoc copy or a caller-selected executable.
type verifiedSystemExecutable struct {
	path   string
	file   *os.File
	before unix.Stat_t
}

func systemExecutablePath(kind systemExecutableKind) string {
	switch kind {
	case systemContainmentLauncher:
		return "/usr/bin/sandbox-exec"
	case systemDiskMetadataReader:
		return "/usr/sbin/diskutil"
	case systemProductMetadata:
		return "/usr/bin/sw_vers"
	case systemTimeMachineUtility:
		return "/usr/bin/tmutil"
	default:
		return ""
	}
}

func verifySystemExecutable(kind systemExecutableKind) (*verifiedSystemExecutable, error) {
	path := systemExecutablePath(kind)
	if path == "" {
		return nil, errTrustedExecutable
	}
	file, err := openExecutableNoFollow(path)
	if err != nil {
		return nil, errTrustedExecutable
	}
	info, err := statAnchor(file)
	var fs unix.Statfs_t
	if err != nil || unix.Fstatfs(int(file.Fd()), &fs) != nil || !protectedSystemExecutable(info, fs) {
		_ = file.Close()
		return nil, errTrustedExecutable
	}
	verified := &verifiedSystemExecutable{path: path, file: file, before: info}
	if !verified.unchanged() {
		verified.close()
		return nil, errTrustedExecutable
	}
	return verified, nil
}

func protectedSystemExecutable(info unix.Stat_t, fs unix.Statfs_t) bool {
	filesystem, ok := nativeCString(fs.Fstypename[:])
	return ok && filesystem == "apfs" && fs.Flags&(unix.MNT_RDONLY|unix.MNT_LOCAL) == unix.MNT_RDONLY|unix.MNT_LOCAL && info.Uid == 0 && trustedExecutableStat(info)
}

func (verified *verifiedSystemExecutable) close() {
	if verified != nil && verified.file != nil {
		_ = verified.file.Close()
	}
}

func (verified *verifiedSystemExecutable) unchanged() bool {
	if verified == nil || verified.file == nil {
		return false
	}
	held, err := statAnchor(verified.file)
	var fs unix.Statfs_t
	if err != nil || unix.Fstatfs(int(verified.file.Fd()), &fs) != nil || !protectedSystemExecutable(held, fs) || !metadataStatEqual(verified.before, held) {
		return false
	}
	linked, err := openExecutableNoFollow(verified.path)
	if err != nil {
		return false
	}
	defer func() { _ = linked.Close() }()
	current, err := statAnchor(linked)
	return err == nil && metadataStatEqual(held, current)
}
