package sessionfiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	maxResetSessions    = 10000
	maxResetManifestLen = 1024 * 1024
)

// ResetOwnedUploads removes only copied files enumerated by an owned session
// manifest, then removes that manifest. Linked original paths and unclassified
// files are never opened or removed. The base must already exist; this pre-start
// operation never creates an uploads tree merely to reset it.
func ResetOwnedUploads(base string) (int, error) {
	root, err := os.OpenRoot(base)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open uploads root: %w", err)
	}
	defer func() { _ = root.Close() }()

	directory, err := root.Open(".")
	if err != nil {
		return 0, fmt.Errorf("inspect uploads root: %w", err)
	}
	entries, readErr := directory.ReadDir(maxResetSessions + 1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return 0, fmt.Errorf("list uploads root: %w", readErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("close uploads root: %w", closeErr)
	}
	if len(entries) > maxResetSessions {
		return 0, errors.New("uploads reset exceeds the bounded session limit")
	}

	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() || !filepath.IsLocal(entry.Name()) || entry.Name() == "." {
			continue
		}
		manifestPath := filepath.Join(entry.Name(), "manifest.json")
		data, err := root.ReadFile(manifestPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("read owned upload manifest: %w", err)
		}
		if len(data) == 0 || len(data) > maxResetManifestLen {
			return removed, errors.New("owned upload manifest exceeds reset limits")
		}
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return removed, errors.New("owned upload manifest is invalid")
		}
		if manifest.SessionID != entry.Name() || len(manifest.Files) > MaxFilesPerSession {
			return removed, errors.New("owned upload manifest identity or file count is invalid")
		}
		for _, file := range manifest.Files {
			if !filepath.IsLocal(file.Path) || file.Path == "." {
				return removed, errors.New("owned upload path is unsafe")
			}
			if file.IsLink {
				info, err := root.Lstat(filepath.Join(entry.Name(), "files", file.Path))
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return removed, fmt.Errorf("inspect linked upload registration: %w", err)
				}
				if err == nil && info.Mode()&os.ModeSymlink == 0 {
					return removed, errors.New("linked upload registration changed type")
				}
			}
		}
		for _, file := range manifest.Files {
			ownedPath := filepath.Join(entry.Name(), "files", file.Path)
			if err := root.Remove(ownedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return removed, fmt.Errorf("remove owned upload registration: %w", err)
			}
			if !file.IsLink {
				removed++
			}
		}
		if err := root.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, fmt.Errorf("remove upload registration: %w", err)
		}
		// Empty owner-created directories can be retired. A failure means an
		// unclassified file remains, so preserve the directory without error.
		_ = root.Remove(filepath.Join(entry.Name(), "files"))
		_ = root.Remove(entry.Name())
	}
	return removed, nil
}
