package usagelimits

import (
	"path/filepath"
	"testing"
)

func TestCodexHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, tt := range []struct {
		name, override, want string
	}{
		{"default", "", filepath.Join(home, ".codex")},
		{"override", filepath.Join(home, "custom"), filepath.Join(home, "custom")},
		{"blank", "  ", filepath.Join(home, ".codex")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CODEX_HOME", tt.override)
			if got := CodexHome(); got != tt.want {
				t.Fatalf("CodexHome = %q, want %q", got, tt.want)
			}
		})
	}
}
