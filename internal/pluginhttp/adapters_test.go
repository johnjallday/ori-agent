package pluginhttp

import (
	"os"
	"path/filepath"
	"testing"
)

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

	if err := inst.RemoveSkill("plug", "myskill"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "myskill")); !os.IsNotExist(err) {
		t.Errorf("skill dir should be gone after RemoveSkill")
	}
}
