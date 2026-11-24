//go:build darwin

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/testutil/resetincident"
)

func TestResetMenubarMainProcessHelper(t *testing.T) {
	if os.Getenv("ORI_RESET_MENUBAR_TEST_CHILD") != "1" {
		return
	}
	newShellOnboarding = func(string) *onboarding.Manager {
		panic("fixture forbids shell construction before recovery; no systray or native authentication")
	}
	main()
	t.Fatal("main returned instead of refusing startup")
}

// A canceled reset carries the next menubar launch past the reset gate to shell
// construction, where this fixture stops it. This is a process test of the
// startup decision only: no systray, native window or relaunch is exercised.
func TestResetMenubarMainStartsNormallyAfterCancellation(t *testing.T) {
	incident := resetincident.New(t)
	if _, err := settingsreset.NewCoordinator(incident.Lease, nil, nil).Cancel(t.Context(), settingsreset.CancelRequest{OperationID: incident.OperationID, Revision: 3}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := incident.Lease.Close(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var receipt []byte
	for launch := 1; launch <= 2; launch++ {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		// #nosec G204 -- only the current test binary and a fixed helper test.
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestResetMenubarMainProcessHelper$")
		cmd.Dir = incident.DataDir
		cmd.Env = []string{"HOME=" + incident.Fixture.Paths().Home, "ORI_DATA_DIR=" + incident.DataDir, "ORI_RESET_MENUBAR_TEST_CHILD=1"}
		output, _ := cmd.CombinedOutput()
		cancel()
		// Reaching shell construction is exactly what the fixture's hook reports.
		if !strings.Contains(string(output), "fixture forbids shell construction") {
			t.Fatalf("launch %d did not reach the shell after the cancellation:\n%s", launch, output)
		}
		if strings.Contains(string(output), "Cannot open installation") || strings.Contains(string(output), "reset recovery") {
			t.Fatalf("launch %d was held in recovery:\n%s", launch, output)
		}
		data, err := os.ReadFile(filepath.Join(incident.DataDir, resetstate.Directory, "operation.json")) // #nosec G304 -- disposable fixture path
		if err != nil {
			t.Fatal(err)
		}
		if launch > 1 && string(receipt) != string(data) {
			t.Fatal("a later launch rewrote the canceled receipt")
		}
		receipt = data
	}
}

func TestResetMenubarMainRefusesBeforeShellConstruction(t *testing.T) {
	for _, mode := range []string{"recovery", "owned"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			dataDir, home := filepath.Join(root, "data"), filepath.Join(root, "home")
			for _, dir := range []string{dataDir, home, filepath.Join(dataDir, resetstate.Directory)} {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var want string
			if mode == "recovery" {
				want = settingsreset.ErrJournalInvalid.Error()
				if err := os.WriteFile(filepath.Join(dataDir, resetstate.Directory, "operation.json"), []byte(`{"future":99}`), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				lease, err := resetstate.Acquire(dataDir)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := lease.Close(); err != nil {
						t.Error(err)
					}
				})
				want = resetstate.ErrInUse.Error()
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			// #nosec G204 -- only the current test binary and a fixed helper test.
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestResetMenubarMainProcessHelper$")
			cmd.Dir = root
			cmd.Env = []string{"HOME=" + home, "ORI_DATA_DIR=" + dataDir, "ORI_RESET_MENUBAR_TEST_CHILD=1"}
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), want) {
				t.Fatalf("wrong startup boundary: %v\n%s", err, output)
			}
			for _, dir := range []string{dataDir, filepath.Join(home, "Library", "Application Support", "OriAgent")} {
				for _, name := range []string{"app_state.json", "settings.json", "sessions.db"} {
					if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
						t.Fatal("shell/runtime constructed before refusal:", name, err)
					}
				}
			}
		})
	}
}
