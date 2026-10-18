package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/testutil/processfixture"
)

func TestBackupMainProcessHelper(t *testing.T) {
	if os.Getenv("ORI_BACKUP_MAIN_TEST_CHILD") != "1" {
		return
	}
	startupBeforeStores = func(context.Context, string) (*resetstate.Lease, error) {
		panic("backup helper reached application persistence")
	}
	startupPortCheck = func(int) error { panic("backup helper inspected an application port") }
	os.Args = []string{os.Args[0], "--ori-backup-helper=reader-v1"}
	main()
	t.Fatal("private helper returned to normal server startup")
}

func TestBackupHelperExitsBeforeApplicationStartup(t *testing.T) {
	root := t.TempDir()
	home, data := filepath.Join(root, "home"), filepath.Join(root, "data")
	for _, path := range []string{home, data} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	executable := processfixture.Executable(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	// #nosec G204 -- this package's compiled test binary and one fixed process helper.
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestBackupMainProcessHelper$")
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + home, "ORI_DATA_DIR=" + data, "ORI_DISABLE_NATIVE_SECRET_STORE=1", "ORI_BACKUP_MAIN_TEST_CHILD=1"}
	output, err := cmd.CombinedOutput()
	if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 || len(output) != 0 {
		t.Fatal("empty private frame did not fail silently before startup")
	}
	for _, path := range []string{home, data} {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			t.Fatal("private helper constructed application state")
		}
	}
}
