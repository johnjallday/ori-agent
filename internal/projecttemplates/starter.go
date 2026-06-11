package projecttemplates

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// starterFS embeds the starter templates shipped with the app. The all:
// prefix is required so dot-files (e.g. chapters/.keep) are included.
//
//go:embed all:starter
var starterFS embed.FS

const starterRoot = "starter"

// EnsureLibrary creates the templates library directory if missing and
// materializes each starter template whose folder is absent. A folder that
// already exists is never touched, so user edits (or deletions replaced by
// their own content) survive restarts.
func EnsureLibrary(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("failed to create templates directory %s: %w", dir, err)
	}

	starters, err := starterFS.ReadDir(starterRoot)
	if err != nil {
		return fmt.Errorf("failed to read embedded starter templates: %w", err)
	}

	for _, starter := range starters {
		if !starter.IsDir() {
			continue
		}
		dest := filepath.Join(dir, starter.Name())
		if _, err := os.Lstat(dest); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("failed to inspect %s: %w", dest, err)
		}
		if err := materializeStarter(starter.Name(), dest); err != nil {
			// Leave no half-written starter behind: a partial folder would
			// block re-materialization on the next start.
			_ = os.RemoveAll(dest)
			return err
		}
	}
	return nil
}

// materializeStarter copies one embedded starter template to dest.
func materializeStarter(name, dest string) error {
	root := path.Join(starterRoot, name)
	return fs.WalkDir(starterFS, root, func(entryPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("failed to read embedded template %s: %w", entryPath, err)
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(entryPath, root), "/")
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, err := starterFS.ReadFile(entryPath)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", entryPath, err)
		}
		// target is the admin-configured templates directory joined with a
		// relative path produced by walking the embedded starterFS, so it is
		// not influenced by external/user input.
		return os.WriteFile(target, data, 0o640) // #nosec G304 -- target derives from the configured templates dir + an embedded starter's relative path
	})
}
