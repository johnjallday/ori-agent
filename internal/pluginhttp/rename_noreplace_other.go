//go:build !darwin && !linux

package pluginhttp

import (
	"errors"
	"os"
)

// Platforms without an atomic no-replace directory rename fail closed if the
// destination is already visible. The supported packaged-integration target is
// Darwin; this fallback preserves existing installs on other development hosts.
func renameNoReplace(source, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(source, destination)
}
