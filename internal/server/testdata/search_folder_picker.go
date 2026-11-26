// Compiled only into a disposable fixture binary by scripts/test-search-first.py.
// No production environment flag, HTTP path or native desktop effects.
package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func pickSearchFolder(context.Context) (string, bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false, err
	}
	root := filepath.Dir(cwd)
	// #nosec G304 -- fixture-only binary: fixed marker name under the harness-owned cwd, never a request path.
	marker, err := os.ReadFile(filepath.Join(root, ".search-first-fixture"))
	if err != nil || string(marker) != "synthetic-v1\n" || os.Getenv("HOME") != filepath.Join(root, "home") || os.Getenv("ORI_DATA_DIR") != filepath.Join(root, "data") || os.Getenv("ORI_NO_DESKTOP_OPEN") != "1" {
		return "", false, errors.New("not a marked isolated fixture")
	}
	albums := filepath.Join(root, "home", "Albums")
	if info, err := os.Stat(albums); err == nil && info.IsDir() {
		return albums, true, nil
	}
	return filepath.Join(root, "home", "Synthetic Project"), true, nil
}
