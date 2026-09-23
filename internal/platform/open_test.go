package platform

import (
	"path/filepath"
	"testing"
)

// A path that does not exist makes the native launcher fail without starting
// any application, so the error tells us whether the launcher ran at all.
func TestDesktopOpenSwitch(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.rpp")

	launches := map[string]func() error{
		"OpenFolder":          func() error { return OpenFolder(missing) },
		"OpenFile":            func() error { return OpenFile(missing) },
		"RevealInFileManager": func() error { return RevealInFileManager(missing) },
		"OpenApplication":     func() error { return OpenApplication("Ori Desktop Open Switch Test App") },
	}

	t.Run("disabled skips the launcher", func(t *testing.T) {
		t.Setenv(NoDesktopOpenEnv, "1")
		for name, launch := range launches {
			if err := launch(); err != nil {
				t.Errorf("%s with %s=1 returned %v, want nil without launching", name, NoDesktopOpenEnv, err)
			}
		}
	})

	for _, value := range []string{"", "0", "false", "yes"} {
		t.Run("value "+value+" keeps the launcher", func(t *testing.T) {
			t.Setenv(NoDesktopOpenEnv, value)
			if err := OpenFile(missing); err == nil {
				t.Errorf("OpenFile with %s=%q returned nil, want the launcher's error for a missing file", NoDesktopOpenEnv, value)
			}
		})
	}
}
