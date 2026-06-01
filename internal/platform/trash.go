package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// TrashSupported reports whether MoveToTrash can move items to the system trash
// on the current platform. Callers should fall back to a permanent delete when
// this returns false.
func TrashSupported() bool {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		return true
	default:
		return false
	}
}

// MoveToTrash moves the file or directory at path into the system trash and
// returns a token that RestoreFromTrash can use to move it back. On success the
// original path no longer exists.
//
// Unlike a permanent delete (os.RemoveAll), a trashed item remains recoverable
// until the user empties the trash.
//
// The returned token is the new on-disk location on platforms that expose one
// (macOS ~/.Trash, the FreeDesktop trash on Linux). It is empty on Windows,
// where the Recycle Bin has no stable path and restore works by original path.
func MoveToTrash(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("cannot trash %q: %w", abs, err)
	}

	return moveToTrash(abs)
}

// RestoreFromTrash moves a previously trashed item back to originalPath. token is
// the value returned by MoveToTrash (the trashed location where applicable).
func RestoreFromTrash(originalPath, token string) error {
	if originalPath == "" {
		return fmt.Errorf("original path is required")
	}
	if _, err := os.Stat(originalPath); err == nil {
		return fmt.Errorf("cannot restore: %q already exists", originalPath)
	}

	return restoreFromTrash(originalPath, token)
}
