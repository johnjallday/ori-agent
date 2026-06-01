package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// TrashSupported reports whether MoveToTrash can move items to the system trash
// on the current platform. Callers should fall back to a permanent delete when
// this returns false.
func TrashSupported() bool {
	return runtime.GOOS == "darwin"
}

// MoveToTrash moves the file or directory at path into the system trash and
// returns its new location so the item can be restored later. On success the
// original path no longer exists.
//
// Unlike a permanent delete (os.RemoveAll), a trashed item remains on disk until
// the user empties the trash, which makes the operation recoverable.
//
// Platform support:
//   - macOS: moves the item into ~/.Trash (same volume as the workspace root).
//   - other: not yet supported; callers should fall back accordingly.
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

	switch runtime.GOOS {
	case "darwin":
		return moveToMacTrash(abs)
	default:
		return "", fmt.Errorf("move to trash is not supported on %s", runtime.GOOS)
	}
}

// moveToMacTrash relocates abs into the user's ~/.Trash. The item shows up in
// Finder's Trash and is purged when the user empties it.
func moveToMacTrash(abs string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	trashDir := filepath.Join(home, ".Trash")
	if err := os.MkdirAll(trashDir, 0700); err != nil {
		return "", fmt.Errorf("cannot access Trash: %w", err)
	}

	dest := uniqueTrashPath(trashDir, filepath.Base(abs))
	if err := os.Rename(abs, dest); err != nil {
		return "", fmt.Errorf("failed to move %q to Trash: %w", abs, err)
	}
	return dest, nil
}

// uniqueTrashPath returns a destination inside trashDir that does not collide
// with an existing item, mirroring how Finder disambiguates same-named items.
func uniqueTrashPath(trashDir, name string) string {
	dest := filepath.Join(trashDir, name)
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		return dest
	}

	ext := filepath.Ext(name)
	stem := name[:len(name)-len(ext)]
	stamp := time.Now().Format("2006-01-02 15.04.05")

	candidate := filepath.Join(trashDir, fmt.Sprintf("%s %s%s", stem, stamp, ext))
	for i := 1; ; i++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
		candidate = filepath.Join(trashDir, fmt.Sprintf("%s %s (%d)%s", stem, stamp, i, ext))
	}
}
