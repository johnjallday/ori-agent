package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestResetMainForwardsPinnedLeaseToServerConstruction(t *testing.T) {
	if !resetstate.Supported() {
		t.Skip("native lease unsupported")
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	// #nosec G204 -- fixed helper in this test binary; the owned callback refuses all production construction.
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestResetMainProcessHelper$")
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "ORI_DATA_DIR=" + data, "ORI_RESET_MAIN_TEST_CHILD=handoff"}
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "fixture reached owned server handoff") {
		t.Fatalf("wrong launcher handoff: %v\n%s", err, output)
	}
	for _, name := range []string{"settings.json", "app_state.json", "sessions.db", "agents.json"} {
		if _, err := os.Stat(filepath.Join(data, name)); !os.IsNotExist(err) {
			t.Fatal("fixture constructed application state:", name, err)
		}
	}
	lease, err := resetstate.Acquire(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}
