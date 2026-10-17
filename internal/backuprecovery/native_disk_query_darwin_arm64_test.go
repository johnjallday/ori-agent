//go:build darwin && arm64

package backuprecovery

import (
	"context"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNativeDiskQuerySpecHasOnlyOneFixedMetadataOperation(t *testing.T) {
	spec, err := nativeDiskReadSpec("disk900000s1", "/invented-private")
	if err != nil || spec.executable != "/usr/sbin/diskutil" || !reflect.DeepEqual(spec.arguments, []string{"info", "-plist", "/dev/disk900000s1"}) ||
		spec.directory != "/invented-private" || spec.stdoutLimit != maxDiskMetadataBytes || spec.timeout != scopedQueryLimit || !spec.pipelineMember || spec.input != nil ||
		!reflect.DeepEqual(spec.environment, []string{"HOME=/invented-private", "TMPDIR=/invented-private", "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC", "GOMAXPROCS=2"}) {
		t.Fatal("OS query escaped the fixed operation/environment")
	}
	for _, device := range []string{"", "-all", "/dev/disk900000s1", "disk900000s1/../disk1", "disk900000s1\n", "disk900000;id", "disk900000s1 extra"} {
		if _, err := nativeDiskReadSpec(device, "/invented-private"); err != errStorageIdentity {
			t.Fatal("unsafe query target accepted")
		}
	}
	if _, err := nativeDiskReadSpec("disk900000s1", "relative"); err != errStorageIdentity {
		t.Fatal("unsafe private directory accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := queryNativeDisk(ctx, "disk900000s1", "/must-not-be-inspected"); err != errStorageIdentity {
		t.Fatal("cancelled query performed native I/O")
	}
	if _, err := queryNativeDisk(nil, "disk900000s1", "/must-not-be-inspected"); err != errStorageIdentity {
		t.Fatal("missing context admitted")
	}
}

func TestFixedSystemEntrypointsRequireProtectedRootOwnedExecutable(t *testing.T) {
	for _, kind := range []systemExecutableKind{systemContainmentLauncher, systemDiskMetadataReader, systemProductMetadata} {
		verified, err := verifySystemExecutable(kind)
		if err != nil {
			t.Fatal("fixed protected OS executable unavailable")
		}
		if !verified.unchanged() {
			t.Fatal("OS executable lost descriptor/path binding")
		}
		verified.close()
		if verified.unchanged() {
			t.Fatal("closed OS descriptor still trusted")
		}
	}
	for _, kind := range []systemExecutableKind{0, 99} {
		if path := systemExecutablePath(kind); path != "" {
			t.Fatal("unknown OS operation selected a path")
		}
		if verified, err := verifySystemExecutable(kind); verified != nil || err != errTrustedExecutable {
			t.Fatal("unknown OS executable accepted")
		}
	}
	info := unix.Stat_t{Dev: 1, Ino: 1, Mode: unix.S_IFREG | 0755, Nlink: 1, Size: 1024, Uid: 0}
	fs := nativeMountFixture()
	fs.Flags = unix.MNT_LOCAL | unix.MNT_RDONLY
	if !protectedSystemExecutable(info, fs) {
		t.Fatal("protected fixture rejected")
	}
	helperInfo := info
	helperInfo.Size = maxTrustedExecutableBytes + 1
	if trustedExecutableStat(helperInfo) || !trustedExecutableStatWithin(helperInfo, maxApplicationHelperBytes) {
		t.Fatal("application helper bound changed the separate restic bound")
	}
	helperInfo.Size = maxApplicationHelperBytes + 1
	if trustedExecutableStatWithin(helperInfo, maxApplicationHelperBytes) {
		t.Fatal("application helper exceeded its own bound")
	}
	for _, change := range []func(*unix.Stat_t, *unix.Statfs_t){
		func(i *unix.Stat_t, _ *unix.Statfs_t) { i.Uid = 42 },
		func(i *unix.Stat_t, _ *unix.Statfs_t) { i.Mode |= 0020 },
		func(i *unix.Stat_t, _ *unix.Statfs_t) { i.Mode |= unix.S_ISUID },
		func(i *unix.Stat_t, _ *unix.Statfs_t) { i.Nlink = 2 },
		func(_ *unix.Stat_t, f *unix.Statfs_t) { f.Flags = unix.MNT_LOCAL },
		func(_ *unix.Stat_t, f *unix.Statfs_t) { f.Flags = unix.MNT_RDONLY },
		func(_ *unix.Stat_t, f *unix.Statfs_t) { clear(f.Fstypename[:]); copy(f.Fstypename[:], "nfs") },
	} {
		i, f := info, fs
		change(&i, &f)
		if protectedSystemExecutable(i, f) {
			t.Fatal("unprotected executable accepted")
		}
	}
}
