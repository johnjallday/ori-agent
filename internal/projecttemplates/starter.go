package projecttemplates

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// starterFS embeds the starter templates shipped with the app. The all:
// prefix is required so dot-files (e.g. chapters/.keep) are included.
//
//go:embed all:starter
var starterFS embed.FS

const starterRoot = "starter"

// builtinStarterIDs is the set of shipped starter ids whose embedded manifest
// declares "builtin": true. Derived from starterFS so the embedded templates
// stay the single source of truth for what ships built-in.
var builtinStarterIDs = func() map[string]struct{} {
	ids := map[string]struct{}{}
	entries, err := starterFS.ReadDir(starterRoot)
	if err != nil {
		return ids
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := starterFS.ReadFile(path.Join(starterRoot, entry.Name(), ManifestFileName))
		if err != nil {
			continue
		}
		var m struct {
			Builtin bool `json:"builtin"`
		}
		if json.Unmarshal(data, &m) == nil && m.Builtin {
			ids[entry.Name()] = struct{}{}
		}
	}
	return ids
}()

// IsBuiltinStarterID reports whether id is a shipped built-in starter template.
// It lets the listing layer mark a shipped template as built-in even on installs
// whose on-disk copy predates the builtin flag (EnsureLibrary never overwrites
// an existing folder, so an older built-in starter won't carry it).
func IsBuiltinStarterID(id string) bool {
	_, ok := builtinStarterIDs[id]
	return ok
}

// EnsureLibrary creates the templates library directory if missing and
// materializes each starter template whose folder is absent. An existing folder
// is left as-is, with one exception: a shipped built-in whose embedded
// builtin_version exceeds the on-disk copy has its template.json refreshed in
// place, so shipped metadata changes (agent rosters, tags, behavior, …) reach
// existing installs. Only the manifest is refreshed — scaffold seed files and
// any other folder contents are never touched, so user edits survive. User
// templates are never in starterFS, so this loop only ever sees shipped
// starters.
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
		name := starter.Name()
		dest := filepath.Join(dir, name)
		if _, err := os.Lstat(dest); err == nil {
			// Folder exists: refresh only a built-in whose shipped manifest is
			// newer than the on-disk copy; otherwise leave it untouched.
			if IsBuiltinStarterID(name) && embeddedBuiltinVersion(name) > diskBuiltinVersion(dest) {
				if err := refreshBuiltinManifest(name, dest); err != nil {
					return err
				}
				// A dashboard shipped with a built-in would otherwise reach only
				// fresh installs, since the manifest refresh above rewrites just
				// template.json.
				if err := refreshBuiltinDashboard(name, dest); err != nil {
					return err
				}
			}
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("failed to inspect %s: %w", dest, err)
		}
		if err := materializeStarter(name, dest); err != nil {
			// Leave no half-written starter behind: a partial folder would
			// block re-materialization on the next start.
			_ = os.RemoveAll(dest)
			return err
		}
	}
	return nil
}

// refreshBuiltinManifest overwrites dest/template.json with the embedded
// starter's manifest. It propagates shipped metadata changes to an existing
// install without disturbing scaffold seed files or any other folder contents.
func refreshBuiltinManifest(name, dest string) error {
	data, err := starterFS.ReadFile(path.Join(starterRoot, name, ManifestFileName))
	if err != nil {
		return fmt.Errorf("failed to read embedded manifest for %s: %w", name, err)
	}
	// target is the configured templates dir joined with a shipped starter name
	// and the fixed ManifestFileName constant — not influenced by user input.
	target := filepath.Join(dest, ManifestFileName)
	if err := os.WriteFile(target, data, 0o640); err != nil { // #nosec G304 G306 -- target is templatesDir/<shipped starter>/template.json; 0o640 matches the package manifest-write convention
		return fmt.Errorf("failed to refresh built-in manifest %s: %w", target, err)
	}
	return nil
}

// refreshBuiltinDashboard installs a built-in's shipped dashboard into an
// existing on-disk template that does not have one yet.
//
// It never overwrites an existing `dashboard/` directory. Templates are editable
// in the authoring UI, so a dashboard already on disk may be the user's own
// work; a shipped revision must not silently replace it. The all-or-nothing
// check is the directory's presence, not individual files, so a partially
// customized dashboard is left entirely alone.
func refreshBuiltinDashboard(name, dest string) error {
	root := path.Join(starterRoot, name, DashboardDirName)
	if _, err := fs.Stat(starterFS, path.Join(root, workspace.CustomDashboardEntryAsset)); err != nil {
		return nil // this built-in ships no dashboard
	}
	target := filepath.Join(dest, DashboardDirName)
	if _, err := os.Lstat(target); err == nil {
		return nil // the on-disk template already has one; leave it be
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to inspect %s: %w", target, err)
	}

	err := fs.WalkDir(starterFS, root, func(entryPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("failed to read embedded dashboard %s: %w", entryPath, err)
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(entryPath, root), "/")
		// target derives from the configured templates dir plus a relative path
		// produced by walking the embedded starterFS; no user input reaches it.
		dashboardPath := filepath.Join(target, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dashboardPath, 0o750)
		}
		data, readErr := starterFS.ReadFile(entryPath)
		if readErr != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", entryPath, readErr)
		}
		return os.WriteFile(dashboardPath, data, 0o640) // #nosec G304 G306 -- templates dir + embedded starter relative path; 0o640 matches this package's convention
	})
	if err != nil {
		// Leave no half-written dashboard: a partial one would render broken.
		_ = os.RemoveAll(target)
		return err
	}
	return nil
}

// embeddedBuiltinVersion returns the builtin_version an embedded starter's
// manifest declares (0 when absent or unparseable).
func embeddedBuiltinVersion(name string) int {
	data, err := starterFS.ReadFile(path.Join(starterRoot, name, ManifestFileName))
	if err != nil {
		return 0
	}
	return parseBuiltinVersion(data)
}

// diskBuiltinVersion returns the builtin_version an on-disk template's manifest
// declares (0 when missing or unparseable).
func diskBuiltinVersion(dest string) int {
	data, err := os.ReadFile(filepath.Join(dest, ManifestFileName)) // #nosec G304 -- dest is templatesDir/<shipped starter>; filename is the fixed ManifestFileName constant
	if err != nil {
		return 0
	}
	return parseBuiltinVersion(data)
}

func parseBuiltinVersion(data []byte) int {
	var m struct {
		BuiltinVersion int `json:"builtin_version"`
	}
	_ = json.Unmarshal(data, &m)
	return m.BuiltinVersion
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
