package workspacecontinuity

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStageVerifiedCanonicalFileStreamsLargeOwnedAssetWithoutReplacement(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "files"), 0700); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("private authored asset"), MaxChunkBytes/len("private authored asset")+2)
	if err := os.WriteFile(filepath.Join(source, "files", "attachment.bin"), data, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	ref := Fingerprint{Path: "files/attachment.bin", Digest: Digest(data), Bytes: int64(len(data))}
	if err := StageVerifiedCanonicalFile(t.Context(), source, root, ref); err != nil {
		t.Fatal(err)
	}
	staged, err := ReadCanonicalFile(t.Context(), destination, ref.Path, len(data))
	if err != nil || !bytes.Equal(staged, data) {
		t.Fatal("streamed bytes changed", err)
	}
	if err := StageVerifiedCanonicalFile(t.Context(), source, root, ref); !errors.Is(err, ErrChanged) {
		t.Fatal("repeat stage overwrote authored asset", err)
	}
	bad := ref
	bad.Digest = Digest([]byte("other"))
	if err := StageVerifiedCanonicalFile(t.Context(), source, root, bad); !errors.Is(err, ErrChanged) {
		t.Fatal("existing asset was replaced", err)
	}
	if err := StageVerifiedCanonicalFile(t.Context(), source, root, Fingerprint{Path: "files/../escape", Digest: ref.Digest, Bytes: ref.Bytes}); !errors.Is(err, ErrInvalid) {
		t.Fatal("traversal was accepted", err)
	}
	if err := StageVerifiedCanonicalFile(t.Context(), source, root, Fingerprint{Path: "agents/guide/config.json", Digest: ref.Digest, Bytes: ref.Bytes}); !errors.Is(err, ErrInvalid) {
		t.Fatal("owned-asset staging accepted agent secret configuration", err)
	}
}

func TestStageVerifiedCanonicalFileRefusesLinkedSourceAndDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require platform permissions")
	}
	source, destination := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "files"), 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(outside, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "files", "reference")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	ref := Fingerprint{Path: "files/reference", Digest: Digest([]byte("external")), Bytes: 8}
	if err := StageVerifiedCanonicalFile(t.Context(), source, root, ref); !errors.Is(err, ErrUnsafe) {
		t.Fatal("linked source escaped", err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(destination, "files", "linked")); err != nil {
		t.Fatal(err)
	}
	ref.Path = "files/linked/other"
	if err := StageVerifiedCanonicalFile(t.Context(), source, root, ref); !errors.Is(err, ErrUnsafe) {
		t.Fatal("linked stage parent escaped", err)
	}
	content, err := os.ReadFile(outside)
	if err != nil || string(content) != "external" {
		t.Fatal("external file changed", err)
	}
}
