//go:build darwin || linux

package backuprecovery

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func executableFixture(t *testing.T) (string, [sha256.Size]byte) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "invented-executable")
	data := []byte("invented bytes; this fixture is never executed")
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	return path, sha256.Sum256(data)
}

func TestExecutableDescriptorDigestAndPathRemainBound(t *testing.T) {
	path, digest := executableFixture(t)
	checked, err := verifyExecutable(t.Context(), path, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer checked.close()
	if !checked.unchanged(t.Context()) {
		t.Fatal("unchanged executable rejected")
	}
	if err := os.Rename(path, path+"-moved"); err != nil {
		t.Fatal(err)
	}
	// Even identical replacement bytes are not the original descriptor.
	if err := os.WriteFile(path, []byte("invented bytes; this fixture is never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	if checked.unchanged(t.Context()) {
		t.Fatal("same-name replacement reused executable trust")
	}
}

func TestExecutableTrustRejectsUnsafeFilesAndAncestry(t *testing.T) {
	for _, scenario := range []string{"missing", "wrong digest", "empty digest", "not executable", "group writable", "world writable", "symlink", "symlink parent", "writable parent", "hardlink", "directory", "fifo", "empty", "oversized", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			path, digest := executableFixture(t)
			ctx := t.Context()
			var err error
			switch scenario {
			case "missing":
				path += "-missing"
			case "wrong digest":
				digest = sha256.Sum256([]byte("wrong"))
			case "empty digest":
				digest = [sha256.Size]byte{}
			case "not executable":
				err = os.Chmod(path, 0600)
			case "group writable":
				err = os.Chmod(path, 0720)
			case "world writable":
				err = os.Chmod(path, 0702)
			case "symlink":
				err = os.Symlink(path, path+"-link")
				path += "-link"
			case "symlink parent":
				parent := filepath.Dir(path)
				err = os.Symlink(parent, filepath.Join(parent, "alias"))
				path = filepath.Join(parent, "alias", filepath.Base(path))
			case "writable parent":
				err = os.Chmod(filepath.Dir(path), 0777)
			case "hardlink":
				err = os.Link(path, path+"-alias")
			case "directory":
				path = filepath.Dir(path)
			case "fifo":
				path += "-fifo"
				err = unix.Mkfifo(path, 0700)
			case "empty":
				err = os.Truncate(path, 0)
			case "oversized":
				err = os.Truncate(path, maxTrustedExecutableBytes+1)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			checked, err := verifyExecutable(ctx, path, digest)
			if checked != nil {
				checked.close()
				t.Fatal("unsafe executable opened")
			}
			if err != errTrustedExecutable {
				t.Fatal("unsafe executable accepted or private diagnostic escaped", err)
			}
		})
	}
}

func TestExecutableTrustInvalidatesMutationCloseAndCancellation(t *testing.T) {
	for _, scenario := range []string{"bytes", "permissions", "closed", "cancelled", "new symlink", "new hardlink", "parent permissions"} {
		t.Run(scenario, func(t *testing.T) {
			path, digest := executableFixture(t)
			checked, err := verifyExecutable(t.Context(), path, digest)
			if err != nil {
				t.Fatal(err)
			}
			defer checked.close()
			ctx := t.Context()
			switch scenario {
			case "bytes":
				err = os.WriteFile(path, []byte("changed"), 0700)
			case "permissions":
				err = os.Chmod(path, 0777)
			case "closed":
				checked.close()
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "new symlink":
				if err = os.Rename(path, path+"-moved"); err == nil {
					err = os.Symlink(path+"-moved", path)
				}
			case "new hardlink":
				err = os.Link(path, path+"-alias")
			case "parent permissions":
				err = os.Chmod(filepath.Dir(path), 0777)
			}
			if err != nil {
				t.Fatal(err)
			}
			if checked.unchanged(ctx) {
				t.Fatal("obsolete trust remained usable")
			}
		})
	}
}

func TestExecutableTrustRejectsPrivilegeBitsAndForeignOwners(t *testing.T) {
	path, _ := executableFixture(t)
	file, err := openExecutableNoFollow(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	info, err := statAnchor(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, bits := range []uint32{unix.S_ISUID, unix.S_ISGID, unix.S_ISVTX} {
		changed := info
		// Stat_t.Mode differs in width across the supported test platforms.
		switch bits {
		case unix.S_ISUID:
			changed.Mode |= unix.S_ISUID
		case unix.S_ISGID:
			changed.Mode |= unix.S_ISGID
		case unix.S_ISVTX:
			changed.Mode |= unix.S_ISVTX
		}
		if trustedExecutableStat(changed) {
			t.Fatal("privileged executable accepted")
		}
	}
	info.Uid = 4294967294
	if trustedExecutableStat(info) {
		t.Fatal("foreign writable-owner authority accepted")
	}
}

func TestProductionExecutablePinCannotUseFixtureDigest(t *testing.T) {
	path, _ := executableFixture(t)
	if checked, err := verifyResticExecutable(t.Context(), path); err != errTrustedExecutable || checked != nil {
		if checked != nil {
			checked.close()
		}
		t.Fatal("production pin accepted an invented executable")
	}
}
