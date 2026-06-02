//go:build darwin

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// moveToTrash relocates abs into the user's ~/.Trash. The item shows up in
// Finder's Trash and is purged when the user empties it.
func moveToTrash(abs string) (string, error) {
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

// restoreFromTrash moves an item back out of ~/.Trash to originalPath.
func restoreFromTrash(originalPath, token string) error {
	if token == "" {
		return fmt.Errorf("missing trashed location; cannot restore %q", originalPath)
	}
	if _, err := os.Stat(token); err != nil {
		return fmt.Errorf("trashed item is no longer available (it may have been emptied from Trash): %w", err)
	}
	if err := os.Rename(token, originalPath); err != nil {
		return fmt.Errorf("failed to move item out of Trash: %w", err)
	}
	return nil
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
