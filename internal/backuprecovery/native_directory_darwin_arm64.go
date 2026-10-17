//go:build darwin && arm64

package backuprecovery

import (
	"bytes"
	"math"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// readNativeDirectory must run in the cancellable compiled helper, not a server
// goroutine. It performs no enumeration, content read, mount action or diskutil
// discovery. Native fd evidence also handles /Users-style system firmlinks;
// string containment in the reported mount point would incorrectly reject them.
func readNativeDirectory(path string) (*nativeDirectory, error) {
	if !validMetadataPath(path) {
		return nil, errStorageIdentity
	}
	file, err := openSearchDirectory(path)
	if err != nil {
		return nil, errStorageIdentity
	}
	result := &nativeDirectory{path: path, file: file}
	fail := func() (*nativeDirectory, error) { result.close(); return nil, errStorageIdentity }
	canonical, err := searchDirectoryPath(file)
	if err != nil || canonical != path {
		return fail()
	}
	info, err := statAnchor(file)
	if err != nil || info.Mode&unix.S_IFMT != unix.S_IFDIR || anchorID(info).device == 0 {
		return fail()
	}
	result.identity = anchorID(info)
	var fs unix.Statfs_t
	if unix.Fstatfs(int(file.Fd()), &fs) != nil {
		return fail()
	}
	mount, err := decodeNativeMount(fs)
	if err != nil {
		return fail()
	}
	root, err := openSearchDirectory(mount.mountPoint)
	if err != nil {
		return fail()
	}
	defer func() { _ = root.Close() }()
	rootInfo, err := statAnchor(root)
	if err != nil || rootInfo.Mode&unix.S_IFMT != unix.S_IFDIR || anchorID(rootInfo).device != result.identity.device || rootInfo.Ino == 0 {
		return fail()
	}
	var rootFS unix.Statfs_t
	if unix.Fstatfs(int(root.Fd()), &rootFS) != nil {
		return fail()
	}
	rootMount, err := decodeNativeMount(rootFS)
	if err != nil || rootMount != mount {
		return fail()
	}
	mount.deviceID = result.identity.device
	mount.rootFileID = rootInfo.Ino
	result.mount = mount
	result.capacity = nativeFilesystemCapacity(fs)
	// Reopen the selected name after the metadata queries. The held descriptor
	// and mount evidence are rechecked again by the owning complete operation.
	if !result.sameSelectedDirectory() {
		return fail()
	}
	return result, nil
}

func (directory *nativeDirectory) sameSelectedDirectory() bool {
	if directory == nil || directory.file == nil || directory.identity.device == 0 || directory.identity.inode == 0 {
		return false
	}
	current, err := openSearchDirectory(directory.path)
	if err != nil {
		return false
	}
	defer func() { _ = current.Close() }()
	held, heldErr := statAnchor(directory.file)
	linked, linkedErr := statAnchor(current)
	return heldErr == nil && linkedErr == nil && anchorID(held) == directory.identity && anchorID(linked) == directory.identity
}

func openSearchDirectory(path string) (*os.File, error) {
	if path != "/" && !validMetadataPath(path) {
		return nil, errStorageIdentity
	}
	flags := directoryTraversalFlags()
	fd, err := unix.Open("/", flags, 0)
	if err != nil {
		return nil, errStorageIdentity
	}
	if path != "/" {
		for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
			next, err := unix.Openat(fd, part, flags, 0)
			_ = unix.Close(fd)
			if err != nil {
				return nil, errStorageIdentity
			}
			fd = next
		}
	}
	return os.NewFile(uintptr(fd), "backup-directory-metadata"), nil
}

func searchDirectoryPath(file *os.File) (string, error) {
	if file == nil {
		return "", errStorageIdentity
	}
	var buffer [1024]byte // Darwin MAXPATHLEN/PATH_MAX, including NUL terminator
	// #nosec G103 -- F_GETPATH is a read-only query on the owned descriptor; the kernel's MAXPATHLEN output has this exact fixed capacity.
	_, _, errno := unix.Syscall(unix.SYS_FCNTL, file.Fd(), unix.F_GETPATH, uintptr(unsafe.Pointer(&buffer[0])))
	runtime.KeepAlive(&buffer)
	path, ok := nativeCString(buffer[:])
	if errno != 0 || !ok || !validNativeMountPoint(path) {
		return "", errStorageIdentity
	}
	return path, nil
}

func decodeNativeMount(fs unix.Statfs_t) (nativeMountEvidence, error) {
	bad := func() (nativeMountEvidence, error) { return nativeMountEvidence{}, errStorageIdentity }
	filesystem, ok := nativeCString(fs.Fstypename[:])
	if !ok || filesystem != "apfs" || fs.Flags&unix.MNT_LOCAL == 0 {
		return bad()
	}
	mountPoint, ok := nativeCString(fs.Mntonname[:])
	if !ok || !validNativeMountPoint(mountPoint) {
		return bad()
	}
	device, ok := nativeCString(fs.Mntfromname[:])
	if !ok || !strings.HasPrefix(device, "/dev/") {
		return bad()
	}
	device = strings.TrimPrefix(device, "/dev/")
	if !diskPartitionReference.MatchString(device) {
		return bad()
	}
	return nativeMountEvidence{device: device, mountPoint: mountPoint, filesystem: filesystem, local: true}, nil
}

func nativeCString(value []byte) (string, bool) {
	end := bytes.IndexByte(value, 0)
	if end < 1 {
		return "", false
	}
	return string(value[:end]), true
}

// Native capacity is a point-in-time filesystem value, not the selected data's
// size or a promise that another backup will fit. Unknown is distinct from zero.
func nativeFilesystemCapacity(fs unix.Statfs_t) *nativeCapacity {
	blockSize := uint64(fs.Bsize)
	if blockSize == 0 || fs.Blocks == 0 || fs.Bavail > fs.Blocks || fs.Blocks > math.MaxUint64/blockSize {
		return nil
	}
	return &nativeCapacity{total: fs.Blocks * blockSize, available: fs.Bavail * blockSize}
}
