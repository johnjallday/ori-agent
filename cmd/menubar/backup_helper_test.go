//go:build darwin

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestBackupMenuProcessHelper(t *testing.T) {
	if os.Getenv("ORI_BACKUP_MENU_TEST_CHILD") != "1" {
		return
	}
	menuBeforeStores = func(context.Context, string) (*resetstate.Lease, error) {
		panic("metadata helper initialized shell persistence")
	}
	os.Args = []string{os.Args[0], "--ori-backup-helper=reader-v1"}
	main()
	t.Fatal("metadata helper returned to native UI startup")
}

func TestBackupHelperExitsBeforeMenubarStartup(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	// #nosec G204 -- current test binary and one compiled helper function.
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestBackupMenuProcessHelper$")
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + home, "ORI_DISABLE_NATIVE_SECRET_STORE=1", "ORI_BACKUP_MENU_TEST_CHILD=1"}
	out, err := cmd.CombinedOutput()
	if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 || len(out) != 0 {
		t.Fatal("metadata helper reached shell logging/UI or did not reject the empty frame")
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("metadata helper created shell state")
	}
}
