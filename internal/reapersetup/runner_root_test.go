package reapersetup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalRunnerRootAcceptsOnlyRealBoundedDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), runnerDirectoryName)
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "runner.id"), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := canonicalRunnerRoot(root)
	if err != nil {
		t.Fatalf("canonicalRunnerRoot: %v", err)
	}
	absolute, _ := filepath.EvalSymlinks(root)
	if resolved != absolute {
		t.Fatalf("resolved root = %q, want %q", resolved, absolute)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := canonicalRunnerRoot(missing); !errors.Is(err, ErrRunnerRootUnavailable) {
		t.Fatalf("missing root error = %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalRunnerRoot(file); !errors.Is(err, ErrRunnerRootUnavailable) {
		t.Fatalf("file root error = %v", err)
	}
}

func TestCanonicalRunnerRootRejectsSymlinkRootAndEscape(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real-runner")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(realRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	linkedRoot := filepath.Join(base, runnerDirectoryName)
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalRunnerRoot(linkedRoot); !errors.Is(err, ErrRunnerRootUnsafe) {
		t.Fatalf("symlink root error = %v", err)
	}

	if err := os.Symlink(filepath.Join(outside, "escaped.lua"), filepath.Join(realRoot, "inbox.lua")); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalRunnerRoot(realRoot); !errors.Is(err, ErrRunnerRootUnsafe) {
		t.Fatalf("nested symlink escape error = %v", err)
	}
}

func TestDefaultRunnerRootComesOnlyFromTrustedHomeResolver(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, runnerDirectoryName)
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	resolver := &defaultRunnerRootResolver{homeDir: func() (string, error) { return home, nil }}
	resolved, err := resolver.Resolve()
	canonical, _ := filepath.EvalSymlinks(root)
	if err != nil || resolved != canonical {
		t.Fatalf("Resolve = %q, %v", resolved, err)
	}
}
