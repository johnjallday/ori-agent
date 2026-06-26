// Package projecttemplates implements domain-blind project templates: folder
// skeletons that are copied into a workspace folder to become its project
// (referenced by the workspace's project_path). The package copies bytes and
// substitutes tokens in file names; it never interprets file contents, so all
// domain specificity lives in template data, not code.
package projecttemplates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ManifestFileName is the optional per-template metadata file. It carries
// display metadata only — never behavior — and is excluded from instantiation.
const ManifestFileName = "template.json"

// Template describes one instantiable folder skeleton.
type Template struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// Path is the template folder's absolute path on disk.
	Path string `json:"-"`
	// Onboarding is the verbatim `onboarding` block from template.json, if any.
	// This package only carries the bytes; the templateonboarding package parses
	// and validates them at workspace-creation time. Excluded from API JSON.
	Onboarding json.RawMessage `json:"-"`
	// Tools are the default skills/MCP servers/plugins a workspace created from
	// this template binds (apply-if-present). Names only — bound in the
	// workspace-creation layer, not here.
	Tools ToolDefaults `json:"tools"`
}

// HasOnboarding reports whether the template carries a non-empty onboarding
// block. It does not validate the block — ParseSpec/Validate in the
// templateonboarding package do that.
func (t Template) HasOnboarding() bool {
	s := bytes.TrimSpace(t.Onboarding)
	return len(s) > 0 && !bytes.Equal(s, []byte("null"))
}

// manifest is the on-disk shape of template.json. Unknown fields are ignored
// by design. Display fields (name/description/tags) are metadata only; the
// optional onboarding block is preserved verbatim as raw JSON and parsed by the
// templateonboarding package at workspace-creation time. This package never
// interprets it, so the file-copy engine stays domain-blind.
type manifest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Tags        []string        `json:"tags,omitempty"`
	Onboarding  json.RawMessage `json:"onboarding,omitempty"`
	Tools       *ToolDefaults   `json:"tools,omitempty"`
}

// readManifest loads template.json from dir. A missing or malformed manifest
// is not an error — the template simply falls back to folder-name display.
// dir is either a library template folder or a folder the caller (an
// admin-facing, local-first tool) explicitly chose via LoadFolder; the
// filename is always the fixed ManifestFileName constant.
func readManifest(dir string) manifest {
	data, err := os.ReadFile(filepath.Join(dir, ManifestFileName)) // #nosec G304 -- dir is a library/template folder resolved by the caller; filename is the fixed ManifestFileName constant
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
	t.Tags = workspace.NormalizeWorkspaceTags(m.Tags)
	t.Onboarding = m.Onboarding
	if m.Tools != nil {
		t.Tools = normalizeToolDefaults(*m.Tools)
	}
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
