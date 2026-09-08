//go:build darwin

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestMenubarDataDirectoryConvergesShellAndServer(t *testing.T) {
	for _, override := range []string{"", "relative-installation", "alias"} {
		t.Run(override, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", filepath.Join(root, "home"))
			t.Setenv("ORI_DATA_DIR", override)
			t.Chdir(root)
			want := filepath.Join(root, "home", "Library", "Application Support", "OriAgent")
			if override != "" {
				want = filepath.Join(root, override)
			}
			if override == "alias" {
				want = filepath.Join(root, "physical")
				if err := os.Mkdir(want, 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(want, filepath.Join(root, override)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := activateMenubarDataDirectory()
			if err != nil {
				t.Fatal(err)
			}
			want, err = filepath.EvalSymlinks(want)
			if err != nil {
				t.Fatal(err)
			}
			cwd, err := os.Getwd()
			if err != nil || got != want || cwd != want || config.DefaultDataDir() != want {
				t.Fatal("shell and server disagree about installation:", err)
			}
			if _, err := os.Stat(filepath.Join(want, "app_state.json")); !os.IsNotExist(err) {
				t.Fatal("activation constructed settings:", err)
			}
			// Exercise refusal without starting systray, external authentication,
			// the normal builder, or pinning a lease in this parent test process.
			if err := os.Mkdir(filepath.Join(want, resetstate.Directory), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(want, resetstate.Directory, "operation.json"), []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if lease, err := resetstate.BeforeStores(got); lease != nil || !errors.Is(err, resetstate.ErrRecoveryRequired) {
				t.Fatal("recovery did not fence shell startup:", err)
			}
		})
	}
}
