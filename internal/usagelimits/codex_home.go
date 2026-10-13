package usagelimits

import (
	"os"
	"path/filepath"
	"strings"
)

// CodexHome follows the CLI provider's CODEX_HOME override, then ~/.codex.
func CodexHome() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}
