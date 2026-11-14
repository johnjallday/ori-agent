// Package knownfolder locates the user's catch-all folders. Folder names are
// interpreted only here; callers recognize a chosen folder by disk identity.
package knownfolder

import (
	"errors"
	"os"
	"path/filepath"
)

var ErrUnavailable = errors.New("known folder is not present")

// Lookup is the platform location seam. It returns a location, not authority;
// Resolve still requires an existing directory distinct from the user's home.
type Lookup func(home, id string) (string, error)

func englishPath(home, id string) (string, error) {
	name := map[string]string{"desktop": "Desktop", "documents": "Documents", "downloads": "Downloads"}[id]
	if name == "" {
		return "", ErrUnavailable
	}
	return filepath.Join(home, name), nil
}

// Resolve locates Desktop, Documents or Downloads without listing contents.
// A missing/disabled folder is absent, never a reason to reject other folders.
// Tests inject the platform lookup and a synthetic home.
func Resolve(home, id string, lookup Lookup) (string, error) {
	if !filepath.IsAbs(home) || (id != "desktop" && id != "documents" && id != "downloads") {
		return "", ErrUnavailable
	}
	if lookup == nil {
		lookup = platformPath
	}
	path, err := lookup(home, id)
	if err != nil || !filepath.IsAbs(path) {
		return "", ErrUnavailable
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", ErrUnavailable
	}
	info, err := os.Stat(path) // #nosec G304 G703 -- host platform lookup, not browser input
	if err != nil || !info.IsDir() {
		return "", ErrUnavailable
	}
	homeInfo, err := os.Stat(home) // #nosec G304 G703 -- configured host home
	if err != nil || os.SameFile(homeInfo, info) {
		return "", ErrUnavailable
	}
	return path, nil
}
