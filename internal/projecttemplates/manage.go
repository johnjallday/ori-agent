package projecttemplates

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/platform"
	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// ErrTemplateExists reports an import that would overwrite an existing
// library template.
var ErrTemplateExists = errors.New("template already exists in the library")

// ErrInvalidTemplateName reports a create/duplicate request whose name is empty
// or slugifies to nothing usable.
var ErrInvalidTemplateName = errors.New("template name is invalid")

// ImportFolder copies an arbitrary folder into the library as a new template.
// The copy is verbatim — no token substitution, since template files may
// legitimately carry {{name}}/{{date}} in their names — with symlinks skipped.
// displayName, when given, becomes the manifest display name and the basis of
// the template ID; otherwise the source folder's name is used.
func ImportFolder(libDir, srcPath, displayName string) (Template, error) {
	libDir = strings.TrimSpace(libDir)
	if libDir == "" {
		return Template{}, fmt.Errorf("templates library is not configured")
	}
	src, err := LoadFolder(srcPath)
	if err != nil {
		return Template{}, err
	}

	absLib, err := filepath.Abs(libDir)
	if err != nil {
		return Template{}, fmt.Errorf("failed to resolve templates directory: %w", err)
	}
	absLib = filepath.Clean(absLib)
	// Importing from inside the library is a no-op/duplicate; importing a
	// parent of the library would copy the library into itself.
	if src.Path == absLib || strings.HasPrefix(src.Path, absLib+string(filepath.Separator)) {
		return Template{}, fmt.Errorf("%q is already inside the templates library", src.Path)
	}
	if strings.HasPrefix(absLib, src.Path+string(filepath.Separator)) {
		return Template{}, fmt.Errorf("cannot import %q: it contains the templates library", src.Path)
	}

	idBasis := strings.TrimSpace(displayName)
	if idBasis == "" {
		idBasis = filepath.Base(src.Path)
	}
	id := workspace.Slugify(idBasis)

	if err := os.MkdirAll(absLib, 0o750); err != nil {
		return Template{}, fmt.Errorf("failed to create templates directory: %w", err)
	}
	dest := filepath.Join(absLib, id)
	if _, err := os.Lstat(dest); err == nil {
		return Template{}, fmt.Errorf("%w: %q", ErrTemplateExists, id)
	} else if !os.IsNotExist(err) {
		return Template{}, fmt.Errorf("failed to inspect %s: %w", dest, err)
	}

	if err := copyFolderVerbatim(src.Path, dest); err != nil {
		_ = os.RemoveAll(dest)
		return Template{}, err
	}

	if strings.TrimSpace(displayName) != "" {
		if _, err := UpdateManifest(absLib, id, displayName, src.Description, nil); err != nil {
			_ = os.RemoveAll(dest)
			return Template{}, err
		}
	}
	return newTemplate(dest), nil
}

// CreateBlank creates a new, empty library template from a display name: a fresh
// folder carrying only a minimal template.json (the name). The id is the
// slugified name; a name that slugifies to nothing is rejected, and a colliding
// id yields ErrTemplateExists.
func CreateBlank(libDir, name string) (Template, error) {
	libDir = strings.TrimSpace(libDir)
	if libDir == "" {
		return Template{}, fmt.Errorf("templates library is not configured")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Template{}, fmt.Errorf("%w: name is required", ErrInvalidTemplateName)
	}
	// Slugify never returns empty (it falls back to "untitled"), so any non-empty
	// name yields a usable id.
	id := workspace.Slugify(name)

	absLib, err := filepath.Abs(libDir)
	if err != nil {
		return Template{}, fmt.Errorf("failed to resolve templates directory: %w", err)
	}
	absLib = filepath.Clean(absLib)
	if err := os.MkdirAll(absLib, 0o750); err != nil {
		return Template{}, fmt.Errorf("failed to create templates directory: %w", err)
	}

	dest := filepath.Join(absLib, id)
	if _, err := os.Lstat(dest); err == nil {
		return Template{}, fmt.Errorf("%w: %q", ErrTemplateExists, id)
	} else if !os.IsNotExist(err) {
		return Template{}, fmt.Errorf("failed to inspect %s: %w", dest, err)
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return Template{}, fmt.Errorf("failed to create template folder: %w", err)
	}

	// Seed a minimal manifest so the display name is explicit from the start.
	if _, err := UpdateManifest(absLib, id, name, "", nil); err != nil {
		_ = os.RemoveAll(dest)
		return Template{}, err
	}
	return newTemplate(dest), nil
}

// Duplicate copies an existing library template into a new one. The copy is
// verbatim (like ImportFolder), so the source's files, tags, and onboarding
// block carry over. newName, when empty, defaults to "<source name> copy"; the
// new id is the slugified result, and a collision yields ErrTemplateExists. The
// duplicate's manifest display name is always set to the resolved name so the
// two templates stay distinguishable.
func Duplicate(libDir, id, newName string) (Template, error) {
	src, err := FindLibraryTemplate(libDir, id)
	if err != nil {
		return Template{}, err
	}

	absLib, err := filepath.Abs(strings.TrimSpace(libDir))
	if err != nil {
		return Template{}, fmt.Errorf("failed to resolve templates directory: %w", err)
	}
	absLib = filepath.Clean(absLib)

	basis := strings.TrimSpace(newName)
	if basis == "" {
		basis = src.Name + " copy"
	}
	newID := workspace.Slugify(basis)

	dest := filepath.Join(absLib, newID)
	if _, err := os.Lstat(dest); err == nil {
		return Template{}, fmt.Errorf("%w: %q", ErrTemplateExists, newID)
	} else if !os.IsNotExist(err) {
		return Template{}, fmt.Errorf("failed to inspect %s: %w", dest, err)
	}

	if err := copyFolderVerbatim(src.Path, dest); err != nil {
		_ = os.RemoveAll(dest)
		return Template{}, err
	}
	// Set the duplicate's display name (tags/onboarding/unknown keys preserved).
	if _, err := UpdateManifest(absLib, newID, basis, src.Description, nil); err != nil {
		_ = os.RemoveAll(dest)
		return Template{}, err
	}
	return newTemplate(dest), nil
}

// copyFolderVerbatim copies a directory tree without name substitution,
// skipping symlinks and preserving permission bits.
func copyFolderVerbatim(srcRoot, destRoot string) error {
	if err := os.MkdirAll(destRoot, 0o750); err != nil {
		return fmt.Errorf("failed to create template folder: %w", err)
	}
	return fs.WalkDir(os.DirFS(srcRoot), ".", func(relPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("failed to read %q: %w", relPath, err)
		}
		if relPath == "." {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !filepath.IsLocal(filepath.FromSlash(relPath)) {
			return fmt.Errorf("entry %q escapes the source folder", relPath)
		}
		target := filepath.Join(destRoot, filepath.FromSlash(relPath))
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("failed to inspect %q: %w", relPath, err)
		}
		if d.IsDir() {
			return os.MkdirAll(target, normalizeDirPerm(info.Mode().Perm()))
		}
		return copyFile(filepath.Join(srcRoot, relPath), target, info.Mode().Perm())
	})
}

// UpdateManifest writes display metadata into a library template's
// template.json, preserving any unknown fields a user may have added. Empty
// name/description values remove the corresponding key (falling back to
// folder-name display).
//
// tags is tri-state so older callers can't clobber author-set tags: nil
// preserves whatever the manifest already has, a non-empty slice replaces the
// tags with the normalized set, and an explicit empty slice clears the key.
func UpdateManifest(libDir, id, name, description string, tags *[]string) (Template, error) {
	tpl, err := FindLibraryTemplate(libDir, id)
	if err != nil {
		return Template{}, err
	}

	// manifestPath is the resolved library template's folder (from
	// FindLibraryTemplate, which rejects ids outside the library) joined with
	// the fixed ManifestFileName constant — never a caller-supplied filename.
	manifestPath := filepath.Join(tpl.Path, ManifestFileName)
	raw := map[string]any{}
	if data, err := os.ReadFile(manifestPath); err == nil { // #nosec G304 -- manifestPath is libDir/<validated id>/template.json, not user-controlled
		// A malformed manifest is replaced rather than failing the edit.
		_ = json.Unmarshal(data, &raw)
	}

	setOrDelete := func(key, value string) {
		if strings.TrimSpace(value) == "" {
			delete(raw, key)
			return
		}
		raw[key] = strings.TrimSpace(value)
	}
	setOrDelete("name", name)
	setOrDelete("description", description)

	if tags != nil {
		if normalized := workspace.NormalizeWorkspaceTags(*tags); len(normalized) > 0 {
			raw["tags"] = normalized
		} else {
			delete(raw, "tags")
		}
	}

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return Template{}, fmt.Errorf("failed to encode manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o640); err != nil { // #nosec G304 -- manifestPath is libDir/<validated id>/template.json, not user-controlled
		return Template{}, fmt.Errorf("failed to write manifest: %w", err)
	}
	return newTemplate(tpl.Path), nil
}

// Delete removes a library template, preferring the system trash so the
// deletion is recoverable. It reports whether the template went to the trash
// (false means it was permanently removed on a platform without trash
// support). Note: deleting a starter template only lasts until the next
// server start, which re-materializes absent starters.
func Delete(libDir, id string) (bool, error) {
	tpl, err := FindLibraryTemplate(libDir, id)
	if err != nil {
		return false, err
	}

	if platform.TrashSupported() {
		if _, err := platform.MoveToTrash(tpl.Path); err == nil {
			return true, nil
		}
		// Fall through to permanent removal if the trash move failed.
	}
	if err := os.RemoveAll(tpl.Path); err != nil {
		return false, fmt.Errorf("failed to delete template %q: %w", id, err)
	}
	return false, nil
}
