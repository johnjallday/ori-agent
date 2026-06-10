// Package projecttemplates implements domain-blind project templates: folder
// skeletons that are copied into a workspace folder to become its project
// (referenced by the workspace's project_path). The package copies bytes and
// substitutes tokens in file names; it never interprets file contents, so all
// domain specificity lives in template data, not code.
package projecttemplates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ManifestFileName is the optional per-template metadata file. It carries
// display metadata only — never behavior — and is excluded from instantiation.
const ManifestFileName = "template.json"

// Template describes one instantiable folder skeleton.
type Template struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Path is the template folder's absolute path on disk.
	Path string `json:"-"`
}

// manifest is the on-disk shape of template.json. Unknown fields are ignored
// by design: the manifest must stay metadata-only.
type manifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// readManifest loads template.json from dir. A missing or malformed manifest
// is not an error — the template simply falls back to folder-name display.
func readManifest(dir string) manifest {
	data, err := os.ReadFile(filepath.Join(dir, ManifestFileName))
	if err != nil {
		return manifest{}
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest{}
	}
	return m
}

// newTemplate builds a Template for the folder at path, applying manifest
// display overrides when present.
func newTemplate(path string) Template {
	t := Template{
		ID:   filepath.Base(filepath.Clean(path)),
		Path: filepath.Clean(path),
	}
	m := readManifest(t.Path)
	t.Name = strings.TrimSpace(m.Name)
	if t.Name == "" {
		t.Name = t.ID
	}
	t.Description = strings.TrimSpace(m.Description)
	return t
}

// ListLibrary returns the templates in the library directory: every immediate
// subfolder is one template, identified by its folder name. Hidden folders
// (dot-prefixed) are skipped. A missing library directory yields an empty
// list, not an error, so a fresh install works before anything is authored.
func ListLibrary(dir string) ([]Template, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read templates directory %s: %w", dir, err)
	}

	templates := make([]Template, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		templates = append(templates, newTemplate(filepath.Join(dir, entry.Name())))
	}

	sort.Slice(templates, func(i, j int) bool { return templates[i].ID < templates[j].ID })
	return templates, nil
}

// FindLibraryTemplate resolves a template ID against the library directory.
func FindLibraryTemplate(dir, id string) (Template, error) {
	id = strings.TrimSpace(id)
	if id == "" || id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return Template{}, fmt.Errorf("%w: %q", ErrTemplateNotFound, id)
	}

	path := filepath.Join(dir, id)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return Template{}, fmt.Errorf("%w: %q", ErrTemplateNotFound, id)
	}
	return newTemplate(path), nil
}

// LoadFolder treats an arbitrary folder on disk as a template (the
// "Choose folder…" escape hatch). It is handled identically to a library
// template — including the optional manifest — but is not part of the library.
func LoadFolder(path string) (Template, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Template{}, fmt.Errorf("%w: empty path", ErrTemplateNotFound)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return Template{}, fmt.Errorf("%w: %q", ErrTemplateNotFound, path)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return Template{}, fmt.Errorf("%w: %q is not a folder", ErrTemplateNotFound, path)
	}
	return newTemplate(abs), nil
}
