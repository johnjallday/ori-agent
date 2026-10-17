//go:build darwin || linux

package backuprecovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func metadataAnchorFixture(t *testing.T) (string, *metadataAnchor) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "-invented-repository")
	for _, dir := range []string{root, filepath.Join(root, "keys"), filepath.Join(root, "snapshots")} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range inventedMetadataBundle().entries {
		path := filepath.Join(root, "config")
		if entry.kind == metadataKey {
			path = filepath.Join(root, "keys", entry.id)
		} else if entry.kind == metadataSnapshot {
			path = filepath.Join(root, "snapshots", entry.id)
		}
		if err := os.WriteFile(path, entry.data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var info unix.Stat_t
	if err := unix.Stat(root, &info); err != nil {
		t.Fatal(err)
	}
	anchor, err := newMetadataAnchor(root, anchorID(info))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(anchor.close)
	return root, anchor
}

func TestMetadataAnchorReadsOnlyFixedMetadataNamespaces(t *testing.T) {
	root, anchor := metadataAnchorFixture(t)
	// A forbidden source/data/locks read would fail. None is needed.
	for _, name := range []string{"data", "index", "locks", "personal-content"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o000); err != nil {
			t.Fatal(err)
		}
	}
	got, err := anchor.collect(t.Context())
	if err != nil || len(got.entries) != 3 {
		t.Fatalf("fixed metadata collection failed: %v", err)
	}
	for i, wanted := range inventedMetadataBundle().entries {
		if got.entries[i].kind != wanted.kind || got.entries[i].id != wanted.id || string(got.entries[i].data) != string(wanted.data) {
			t.Fatal("unexpected metadata")
		}
	}
}

func TestMetadataAnchorRejectsReplacedRootAndSymlinkAncestor(t *testing.T) {
	root, anchor := metadataAnchorFixture(t)
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := anchor.collect(t.Context()); err != errMetadataBundle {
		t.Fatal("same-name replacement accepted")
	}
	alias := filepath.Join(filepath.Dir(root), "alias")
	if err := os.Symlink(filepath.Dir(root), alias); err != nil {
		t.Fatal(err)
	}
	if _, err := newMetadataAnchor(filepath.Join(alias, filepath.Base(root)), anchor.identity); err != errMetadataBundle {
		t.Fatal("symlink ancestor accepted")
	}
	if _, err := newMetadataAnchor(root+"-old", anchoredIdentity{device: anchor.identity.device + 1, inode: anchor.identity.inode}); err != errMetadataBundle {
		t.Fatal("wrong expected device accepted")
	}
}

func TestMetadataAnchorRefusesLinksAndSpecialFilesWithoutBlocking(t *testing.T) {
	for _, kind := range []string{"config_symlink", "config_hardlink", "config_fifo", "keys_symlink", "snapshot_symlink", "unexpected_name"} {
		t.Run(kind, func(t *testing.T) {
			root, anchor := metadataAnchorFixture(t)
			config := filepath.Join(root, "config")
			saved := filepath.Join(filepath.Dir(root), "saved-config")
			switch kind {
			case "config_symlink", "config_fifo":
				if err := os.Rename(config, saved); err != nil {
					t.Fatal(err)
				}
				if kind == "config_symlink" {
					if err := os.Symlink(saved, config); err != nil {
						t.Fatal(err)
					}
				} else if err := unix.Mkfifo(config, 0o600); err != nil {
					t.Fatal(err)
				}
			case "config_hardlink":
				if err := os.Link(config, saved); err != nil {
					t.Fatal(err)
				}
			case "keys_symlink":
				keys := filepath.Join(root, "keys")
				if err := os.Rename(keys, keys+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(keys+"-old", keys); err != nil {
					t.Fatal(err)
				}
			case "snapshot_symlink":
				path := filepath.Join(root, "snapshots", strings.Repeat("2", 64))
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(config, path); err != nil {
					t.Fatal(err)
				}
			case "unexpected_name":
				if err := os.WriteFile(filepath.Join(root, "snapshots", "untrusted-name"), []byte("invented"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			start := time.Now()
			if _, err := anchor.collect(t.Context()); err != errMetadataBundle {
				t.Fatal("unsafe metadata accepted")
			}
			if time.Since(start) > time.Second {
				t.Fatal("special-file rejection blocked")
			}
		})
	}
}

func TestMetadataAnchorBoundsEnumerationAndBytes(t *testing.T) {
	for _, kind := range []string{"keys", "snapshots", "config_bytes", "snapshot_bytes"} {
		t.Run(kind, func(t *testing.T) {
			root, anchor := metadataAnchorFixture(t)
			if kind == "keys" || kind == "snapshots" {
				limit := maxMetadataKeyCount
				if kind == "snapshots" {
					limit = maxSnapshotCount
				}
				for i := 0; i <= limit; i++ {
					if err := os.WriteFile(filepath.Join(root, kind, fmt.Sprintf("%064x", i)), []byte("invented"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				path, size := filepath.Join(root, "config"), int64(maxEncryptedConfigBytes+1)
				if kind == "snapshot_bytes" {
					path, size = filepath.Join(root, "snapshots", strings.Repeat("2", 64)), int64(maxEncryptedSnapshotBytes+1)
				}
				if err := os.Truncate(path, size); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := anchor.collect(t.Context()); err != errMetadataBundle {
				t.Fatal("oversized metadata accepted")
			}
		})
	}
}

func TestMetadataAnchorCancelledAndInvalidPaths(t *testing.T) {
	_, anchor := metadataAnchorFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := anchor.collect(ctx); err != errMetadataBundle {
		t.Fatal("cancelled read accepted")
	}
	for _, path := range []string{"", "/", "relative", "/a/../b", "/a//b", "/a/", "/a\x00b", "/a\nb"} {
		if validMetadataPath(path) {
			t.Fatalf("invalid path accepted: %q", path)
		}
	}
}
