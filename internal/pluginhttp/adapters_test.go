package pluginhttp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
)

func TestRenameNoReplacePreservesExistingDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(source, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "owned.txt"), []byte("independent"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := renameNoReplace(source, destination); err == nil {
		t.Fatal("no-replace rename overwrote an existing destination")
	}
	if contents, err := os.ReadFile(filepath.Join(destination, "owned.txt")); err != nil || string(contents) != "independent" {
		t.Fatalf("existing destination changed: %q, %v", contents, err)
	}
}

func TestSkillDirInstaller(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// nested supporting file (scripts/) should be copied too
	if err := os.MkdirAll(filepath.Join(src, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	inst := newSkillDirInstaller(dest)

	if err := inst.InstallSkill("plug", "myskill", src); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "myskill", "SKILL.md")); err != nil {
		t.Fatalf("installed SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "myskill", "scripts", "run.sh")); err != nil {
		t.Errorf("nested supporting file not copied: %v", err)
	}
	if err := inst.VerifySkill("plug", "myskill"); err != nil {
		t.Fatalf("verify owned copy: %v", err)
	}

	if err := inst.RemoveSkill("plug", "myskill"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "myskill")); !os.IsNotExist(err) {
		t.Errorf("skill dir should be gone after RemoveSkill")
	}
}

func TestSkillDirInstallerRollbackRestoresImmutableInstalledBytes(t *testing.T) {
	oldSource := t.TempDir()
	if err := os.WriteFile(filepath.Join(oldSource, "SKILL.md"), []byte("old installed bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	newSource := t.TempDir()
	if err := os.WriteFile(filepath.Join(newSource, "SKILL.md"), []byte("new candidate bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	installer := newSkillDirInstaller(t.TempDir())
	if err := installer.InstallSkill("plug", "managed", oldSource); err != nil {
		t.Fatal(err)
	}
	snapshot, err := installer.PrepareSkillRollback("plug", []string{"managed"})
	if err != nil {
		t.Fatal(err)
	}
	// A mutable source can change after backup preparation. Rollback must use
	// the installed copy, not whichever bytes that source now exposes.
	if err := os.WriteFile(filepath.Join(oldSource, "SKILL.md"), []byte("mutated source bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installer.RemoveSkill("plug", "managed"); err != nil {
		t.Fatal(err)
	}
	if err := installer.InstallSkill("plug", "managed", newSource); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Restore([]string{"managed"}); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Discard(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(installer.skillsDir, "managed", "SKILL.md"))
	if err != nil || string(contents) != "old installed bytes\n" {
		t.Fatalf("restored bytes = %q, %v", contents, err)
	}
	if err := installer.VerifySkill("plug", "managed"); err != nil {
		t.Fatalf("restored ownership = %v", err)
	}
}

func TestSkillDirInstallerRefusesIndependentAndEditedCopies(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: protected\n---\noriginal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	independent := filepath.Join(dest, "protected")
	if err := os.MkdirAll(independent, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(independent, "SKILL.md"), []byte("user copy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inst := newSkillDirInstaller(dest)
	if err := inst.InstallSkill("plug", "protected", src); !errors.Is(err, plugin.ErrSkillDestinationConflict) {
		t.Fatalf("independent collision error = %v", err)
	}
	contents, _ := os.ReadFile(filepath.Join(independent, "SKILL.md"))
	if string(contents) != "user copy\n" {
		t.Fatalf("independent copy changed: %q", contents)
	}

	if err := os.RemoveAll(independent); err != nil {
		t.Fatal(err)
	}
	if err := inst.InstallSkill("plug", "protected", src); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(independent, "SKILL.md"), []byte("user edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := inst.RemoveSkill("plug", "protected"); !errors.Is(err, plugin.ErrSkillOwnershipChanged) {
		t.Fatalf("edited removal error = %v", err)
	}
	if contents, err := os.ReadFile(filepath.Join(independent, "SKILL.md")); err != nil || string(contents) != "user edit\n" {
		t.Fatalf("edited copy was not preserved: %q, %v", contents, err)
	}

	if err := os.RemoveAll(independent); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, independent); err != nil {
		t.Fatal(err)
	}
	if err := inst.RemoveSkill("plug", "protected"); !errors.Is(err, plugin.ErrSkillDestinationConflict) {
		t.Fatalf("symlinked destination removal error = %v", err)
	}
	if info, err := os.Lstat(independent); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlinked destination was changed: %#v, %v", info, err)
	}
}
