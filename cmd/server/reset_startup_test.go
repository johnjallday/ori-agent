package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/server"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
)

func TestResetMainProcessHelper(t *testing.T) {
	mode := os.Getenv("ORI_RESET_MAIN_TEST_CHILD")
	if mode == "" {
		return
	}
	// Even an ordering regression cannot inspect/kill an inherited port owner,
	// build native secret stores, authenticate, or open a browser in this child.
	startupPortCheck = func(int) error { return errors.New("fixture forbids port inspection before recovery") }
	startupServer = func(*resetstate.Lease) (*server.Server, error) {
		return nil, errors.New("fixture forbids construction before recovery")
	}
	if mode == "handoff" {
		startupPortCheck = func(int) error { return nil }
		startupServer = func(lease *resetstate.Lease) (*server.Server, error) {
			cwd, err := os.Getwd()
			if err != nil || lease == nil || lease.Path() != cwd || lease.Path() != os.Getenv("ORI_DATA_DIR") {
				t.Fatal("main did not forward the activated installation lease")
			}
			if err := lease.Close(); !errors.Is(err, resetstate.ErrPinned) {
				t.Fatal("main forwarded unpinned ownership:", err)
			}
			return nil, errors.New("fixture reached owned server handoff")
		}
	}
	os.Args = []string{os.Args[0], "-no-browser", "-port=0"}
	main()
	t.Fatal("main returned instead of refusing startup")
}

func TestResetMainRefusesBeforePortAndRuntimeConstruction(t *testing.T) {
	for _, mode := range []string{"recovery", "owned"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "owned" && !resetstate.Supported() {
				t.Skip("native lease unavailable")
			}
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
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestResetMainProcessHelper$")
			cmd.Dir = root // No inherited .env; parent owns this brand-new directory.
			cmd.Env = []string{"HOME=" + home, "ORI_DATA_DIR=" + dataDir, "ORI_RESET_MAIN_TEST_CHILD=1"}
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), want) {
				t.Fatalf("wrong startup boundary: %v\n%s", err, output)
			}
			for _, name := range []string{"app_state.json", "settings.json", "sessions.db", "agents.json"} {
				if _, err := os.Stat(filepath.Join(dataDir, name)); !os.IsNotExist(err) {
					t.Fatal("runtime constructed before refusal:", name, err)
				}
			}
		})
	}
}
