//go:build linux

package platform

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// moveToTrash implements the FreeDesktop.org trash specification: the item is
// moved into $XDG_DATA_HOME/Trash/files and a matching .trashinfo record is
// written so desktop file managers (GNOME, KDE, …) show and can restore it.
func moveToTrash(abs string) (string, error) {
	trashDir := xdgTrashDir()
	filesDir := filepath.Join(trashDir, "files")
	infoDir := filepath.Join(trashDir, "info")
	if err := os.MkdirAll(filesDir, 0700); err != nil {
		return "", fmt.Errorf("cannot access Trash: %w", err)
	}
	if err := os.MkdirAll(infoDir, 0700); err != nil {
		return "", fmt.Errorf("cannot access Trash: %w", err)
	}

	name := uniqueTrashName(filesDir, infoDir, filepath.Base(abs))
	dest := filepath.Join(filesDir, name)
	infoPath := filepath.Join(infoDir, name+".trashinfo")

	// Write the .trashinfo record first (per spec), then move the item in.
	info := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		(&url.URL{Path: abs}).EscapedPath(), time.Now().Format("2006-01-02T15:04:05"))
	if err := os.WriteFile(infoPath, []byte(info), 0600); err != nil {
		return "", fmt.Errorf("cannot write trash info: %w", err)
	}

	if err := os.Rename(abs, dest); err != nil {
		_ = os.Remove(infoPath)
		return "", fmt.Errorf("failed to move %q to Trash: %w", abs, err)
	}
	return dest, nil
}

// restoreFromTrash moves an item back out of the trash to originalPath and
// removes its .trashinfo record.
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

	// Best-effort cleanup of the matching info/<name>.trashinfo record.
	infoPath := filepath.Join(filepath.Dir(filepath.Dir(token)), "info", filepath.Base(token)+".trashinfo")
	_ = os.Remove(infoPath)
	return nil
}

// xdgTrashDir returns $XDG_DATA_HOME/Trash, defaulting to ~/.local/share/Trash.
func xdgTrashDir() string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "Trash")
}

// uniqueTrashName returns a base name that is free in both the files and info
// directories, so the item and its .trashinfo record stay paired.
func uniqueTrashName(filesDir, infoDir, name string) string {
	free := func(n string) bool {
		if _, err := os.Stat(filepath.Join(filesDir, n)); err == nil {
			return false
		}
		if _, err := os.Stat(filepath.Join(infoDir, n+".trashinfo")); err == nil {
			return false
		}
		return true
	}

	if free(name) {
		return name
	}

	ext := filepath.Ext(name)
	stem := name[:len(name)-len(ext)]
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s.%d%s", stem, i, ext)
		if free(candidate) {
			return candidate
		}
	}
}
