package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestCollectContinuityOwnedFilesInventoriesOnlyConfinedExactBytes(t *testing.T) {
	root := t.TempDir()
	if entries, err := CollectContinuityOwnedFiles(t.Context(), root); err != nil || len(entries) != 0 {
		t.Fatal("absent folder is not an empty owner", entries, err)
	}
	if err := os.MkdirAll(filepath.Join(root, FilesDir, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	asset := bytes.Repeat([]byte("private authored attachment"), workspacecontinuity.MaxChunkBytes/len("private authored attachment")+2)
	location := filepath.Join(root, FilesDir, "nested", "asset.bin")
	if err := os.WriteFile(location, asset, 0600); err != nil {
		t.Fatal(err)
	}
	files, err := CollectContinuityOwnedFiles(t.Context(), root)
	if err != nil || len(files) != 1 || files[0] != (workspacecontinuity.Fingerprint{Path: "files/nested/asset.bin", Digest: workspacecontinuity.Digest(asset), Bytes: int64(len(asset))}) {
		t.Fatal("large nested asset not captured exactly", files, err)
	}
	if err := CheckContinuityFilesCoverage(t.Context(), root, files); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, FilesDir, "unrepresented"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := CollectContinuityOwnedFiles(t.Context(), root); !errors.Is(err, workspacecontinuity.ErrIncomplete) {
		t.Fatal("empty directory was silently lost", err)
	}
	if err := os.Remove(filepath.Join(root, FilesDir, "unrepresented")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(location, filepath.Join(root, FilesDir, "linked")); err != nil {
			t.Fatal(err)
		}
		if _, err := CollectContinuityOwnedFiles(t.Context(), root); !errors.Is(err, workspacecontinuity.ErrUnsafe) {
			t.Fatal("linked asset captured", err)
		}
		if err := os.Remove(filepath.Join(root, FilesDir, "linked")); err != nil {
			t.Fatal(err)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CollectContinuityOwnedFiles(canceled, root); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled inventory ran", err)
	}
	if err := os.Truncate(location, workspacecontinuity.MaxBlobBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := CollectContinuityOwnedFiles(t.Context(), root); !errors.Is(err, workspacecontinuity.ErrLimit) {
		t.Fatal("oversized asset fingerprinted", err)
	}
}
