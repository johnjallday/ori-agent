//go:build darwin && arm64

package backuprecovery

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

// This protocol is only for invented test fixtures, not the production helper.
type metadataSandboxFixture struct {
	Source     string
	Repository string
}

func TestMetadataBrokerSandboxHelper(t *testing.T) {
	if os.Getenv("ORI_BACKUP_BROKER_HELPER") != "1" {
		return
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, 16<<10))
	if err != nil {
		os.Exit(61)
	}
	var fixture metadataSandboxFixture
	if json.Unmarshal(input, &fixture) != nil {
		os.Exit(62)
	}
	directory, err := readNativeDirectory(fixture.Repository)
	if err != nil {
		os.Exit(63)
	}
	anchor, err := newMetadataAnchor(fixture.Repository, directory.identity)
	if err != nil {
		os.Exit(64)
	}
	bundle, err := anchor.collect(t.Context())
	if err != nil || !reflect.DeepEqual(bundle, inventedMetadataBundle()) || !directory.unchanged() {
		os.Exit(65)
	}
	if names, err := anchor.root.Readdirnames(1); err == nil || len(names) != 0 {
		os.Exit(66)
	}
	for _, path := range []string{filepath.Join(fixture.Source, "invented-content"), filepath.Join(fixture.Repository, "data", "invented-content"), filepath.Join(fixture.Repository, "index", "invented-content"), filepath.Join(fixture.Repository, "locks", "invented-content")} {
		if fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW, 0); err == nil {
			_ = unix.Close(fd)
			os.Exit(67)
		}
	}
	if err := os.WriteFile(filepath.Join(fixture.Repository, "config"), []byte("must not replace"), 0600); err == nil {
		os.Exit(68)
	}
	anchor.close()
	directory.close()
	if _, err := io.WriteString(os.Stdout, "encrypted-metadata-only\n"); err != nil {
		os.Exit(69)
	}
	os.Exit(0)
}

func TestMetadataBrokerSandboxReadsFixedNamespacesAndDeniesContent(t *testing.T) {
	repository, anchor := metadataAnchorFixture(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "invented source")
	for _, dir := range []string{source, filepath.Join(repository, "data"), filepath.Join(repository, "index"), filepath.Join(repository, "locks")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "invented-content"), []byte("must not be read"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	profile, err := metadataBrokerSandboxProfile(sandboxFixtureExecutable(t), []string{source}, repository)
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(metadataSandboxFixture{Source: source, Repository: repository})
	if err != nil {
		t.Fatal(err)
	}
	out := runSandboxFixture(t, profile, input, "TestMetadataBrokerSandboxHelper")
	if string(out) != "encrypted-metadata-only\n" {
		t.Fatal("broker did not return success sentinel")
	}
	unchanged, err := anchor.collect(t.Context())
	if err != nil || !reflect.DeepEqual(unchanged, inventedMetadataBundle()) {
		t.Fatal("invented repository metadata changed")
	}
}
