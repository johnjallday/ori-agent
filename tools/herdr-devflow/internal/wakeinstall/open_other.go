//go:build !darwin

package wakeinstall

import (
	"fmt"
	"os"
)

func openNoFollow(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("staged wake artifact must not be a symlink")
	}
	return os.Open(path) // #nosec G304 -- non-macOS builds always refuse the root lifecycle.
}
