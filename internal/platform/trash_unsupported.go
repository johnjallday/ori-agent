//go:build !darwin && !linux && !windows

package platform

import (
	"fmt"
	"runtime"
)

// moveToTrash is unsupported on this platform; callers fall back to a permanent
// delete (see TrashSupported, which returns false here).
func moveToTrash(abs string) (string, error) {
	return "", fmt.Errorf("move to trash is not supported on %s", runtime.GOOS)
}

func restoreFromTrash(originalPath, _ string) error {
	return fmt.Errorf("restore from trash is not supported on %s", runtime.GOOS)
}
