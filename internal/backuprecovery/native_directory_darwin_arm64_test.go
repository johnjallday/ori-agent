//go:build darwin && arm64

package backuprecovery

import (
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNativeDirectoryBindsMetadataAndRejectsReplacement(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "invented-source")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "invented-content"), []byte("must not be read"), 0600); err != nil {
		t.Fatal(err)
	}
	directory, err := readNativeDirectory(path)
	if err != nil {
		t.Fatal("native metadata unavailable for the local APFS fixture")
	}
	defer directory.close()
	if directory.identity.device == 0 || directory.identity.inode == 0 || !directory.mount.local || directory.mount.rootFileID == 0 || !diskPartitionReference.MatchString(directory.mount.device) || !directory.sameSelectedDirectory() || !directory.unchanged() {
		t.Fatal("missing native mount identity")
	}
	if err := os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if directory.sameSelectedDirectory() || directory.unchanged() {
		t.Fatal("same-name folder replacement remained valid")
	}
	directory.close()
	if directory.sameSelectedDirectory() || directory.unchanged() {
		t.Fatal("closed native evidence remained valid")
	}
}

func TestNativeDirectoryRejectsUnapprovedPathShapesAndLinks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "source")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(base, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(base, "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "relative", "", root + "/../source", root + "\x00", root + "\n", root + "-missing", filepath.Join(base, "alias"), filepath.Join(base, "alias", "nested"), fifo} {
		if result, err := readNativeDirectory(path); err != errStorageIdentity || result != nil {
			if result != nil {
				result.close()
			}
			t.Fatal("unsafe path accepted")
		}
	}
}

// Test-only negative controls: never run these content/enum attempts against
// user-selected folders. The caller creates and owns the invented sentinels.
func TestNativeDirectorySandboxHelper(t *testing.T) {
	if os.Getenv("ORI_BACKUP_DIRECTORY_HELPER") != "1" {
		return
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, maxSourcePathBytes+1))
	if err != nil || len(input) > maxSourcePathBytes {
		os.Exit(71)
	}
	probe, err := openSearchDirectory(string(input))
	if err != nil {
		os.Exit(81)
	}
	if path, err := searchDirectoryPath(probe); err != nil || path != string(input) {
		os.Exit(82)
	}
	if _, err := statAnchor(probe); err != nil {
		os.Exit(83)
	}
	var fs unix.Statfs_t
	if unix.Fstatfs(int(probe.Fd()), &fs) != nil {
		os.Exit(84)
	}
	if _, err := decodeNativeMount(fs); err != nil {
		os.Exit(85)
	}
	_ = probe.Close()
	directory, err := readNativeDirectory(string(input))
	if err != nil {
		os.Exit(71)
	}
	if !directory.unchanged() {
		os.Exit(76)
	}
	if names, err := directory.file.Readdirnames(1); err == nil || len(names) != 0 {
		os.Exit(72)
	}
	if fd, err := unix.Openat(int(directory.file.Fd()), "invented-content", unix.O_RDONLY|unix.O_NOFOLLOW, 0); err == nil {
		_ = unix.Close(fd)
		os.Exit(73)
	}
	if err := os.WriteFile(filepath.Join(string(input), "must-not-be-created"), []byte("no"), 0600); err == nil {
		os.Exit(74)
	}
	directory.close()
	if _, err := io.WriteString(os.Stdout, "metadata-only\n"); err != nil {
		os.Exit(75)
	}
	os.Exit(0)
}

func TestNativeDirectorySandboxDeniesEnumerationContentAndWrites(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, `invented " (allow default) source`)
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "invented-content"), []byte("must not be read"), 0600); err != nil {
		t.Fatal(err)
	}
	profile, err := directoryMetadataSandboxProfile(sandboxFixtureExecutable(t), []string{source})
	if err != nil {
		t.Fatal(err)
	}
	out := runSandboxFixture(t, profile, []byte(source), "TestNativeDirectorySandboxHelper")
	if string(out) != "metadata-only\n" {
		t.Fatal("native metadata success sentinel is missing")
	}
	if _, err := os.Stat(filepath.Join(source, "must-not-be-created")); !os.IsNotExist(err) {
		t.Fatal("source write escaped containment")
	}
}

func nativeMountFixture() unix.Statfs_t {
	var fs unix.Statfs_t
	copy(fs.Fstypename[:], "apfs")
	copy(fs.Mntonname[:], "/Invented Mount")
	copy(fs.Mntfromname[:], "/dev/disk900000s1")
	fs.Flags = unix.MNT_LOCAL
	return fs
}

func TestNativeMountDecoderRejectsNonlocalTruncatedAndSnapshotDevices(t *testing.T) {
	valid := nativeMountFixture()
	if _, err := decodeNativeMount(valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*unix.Statfs_t){
		func(fs *unix.Statfs_t) { fs.Flags = 0 },
		func(fs *unix.Statfs_t) { clear(fs.Fstypename[:]); copy(fs.Fstypename[:], "nfs") },
		func(fs *unix.Statfs_t) {
			for i := range fs.Mntonname {
				fs.Mntonname[i] = 'a'
			}
		},
		func(fs *unix.Statfs_t) { clear(fs.Mntonname[:]) },
		func(fs *unix.Statfs_t) { clear(fs.Mntonname[:]); copy(fs.Mntonname[:], "/a/../b") },
		func(fs *unix.Statfs_t) { clear(fs.Mntfromname[:]); copy(fs.Mntfromname[:], "/dev/disk900000s1s1") },
		func(fs *unix.Statfs_t) { clear(fs.Mntfromname[:]); copy(fs.Mntfromname[:], "/dev/disk900000") },
		func(fs *unix.Statfs_t) { clear(fs.Mntfromname[:]); copy(fs.Mntfromname[:], "//server/share") },
		func(fs *unix.Statfs_t) { clear(fs.Mntfromname[:]); copy(fs.Mntfromname[:], "-disk900000s1") },
	} {
		fs := valid
		change(&fs)
		if _, err := decodeNativeMount(fs); err != errStorageIdentity {
			t.Fatal("unsupported mount became identity")
		}
	}
}

func TestNativeCapacityPreservesUnknownAndKnownZero(t *testing.T) {
	for _, fs := range []unix.Statfs_t{{}, {Bsize: 4096}, {Bsize: 4096, Blocks: 4, Bavail: 5}, {Bsize: 4096, Blocks: math.MaxUint64}} {
		if nativeFilesystemCapacity(fs) != nil {
			t.Fatal("inconsistent capacity invented a value")
		}
	}
	for _, available := range []uint64{0, 2, 4} {
		capacity := nativeFilesystemCapacity(unix.Statfs_t{Bsize: 4096, Blocks: 4, Bavail: available})
		if capacity == nil || capacity.total != 16384 || capacity.available != available*4096 {
			t.Fatal("valid capacity lost")
		}
	}
}
