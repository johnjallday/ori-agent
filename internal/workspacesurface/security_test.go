package workspacesurface

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSelectPlatformArtifactRequiresExactUnambiguousMatch(t *testing.T) {
	artifacts := []PlatformArtifact{{ID: "demo-darwin-arm64", OS: "darwin", Arch: "arm64"}}
	selected, found, err := SelectPlatformArtifact(artifacts, "darwin", "arm64")
	if err != nil || !found || selected.ID != "demo-darwin-arm64" {
		t.Fatalf("SelectPlatformArtifact() = %+v, %v, %v", selected, found, err)
	}
	if _, found, err := SelectPlatformArtifact(artifacts, "linux", "arm64"); err != nil || found {
		t.Fatalf("unsupported platform = found %v, err %v", found, err)
	}
	duplicate := append(artifacts, PlatformArtifact{ID: "other", OS: "darwin", Arch: "arm64"})
	if _, _, err := SelectPlatformArtifact(duplicate, runtime.GOOS, runtime.GOARCH); err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Fatalf("duplicate platform error = %v", err)
	}
}

func TestReadAssetRejectsAbsoluteTraversalAndSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "ui"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ui", "index.html"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	binding := Binding{AssetRoot: root, EntryAsset: "ui/index.html"}
	asset, err := ReadAsset(binding, "ui/index.html")
	if err != nil || string(asset.Data) != "ok" {
		t.Fatalf("ReadAsset(valid) = %+v, %v", asset, err)
	}

	outside := filepath.Join(t.TempDir(), "outside.html")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "ui", "linked.html")); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{
		outside,
		"../outside.html",
		"ui/../../outside.html",
		"ui%2findex.html",
		"ui/linked.html",
	} {
		if _, err := ReadAsset(binding, candidate); !errors.Is(err, ErrAssetPathInvalid) {
			t.Fatalf("ReadAsset(%q) error = %v, want ErrAssetPathInvalid", candidate, err)
		}
	}
}
