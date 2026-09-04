package projecttemplates

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ProjectEntry identifies the one scaffolded file that represents a template
// project. RelativePath is portable manifest data, not an executable hook or
// application name.
type ProjectEntry struct {
	RelativePath           string `json:"relative_path"`
	OpenAfterCreateDefault bool   `json:"open_after_create_default"`
}

// ProjectEntryEdit gives manifest updates tri-state semantics: an omitted edit
// preserves the current value, Set with a nil Value clears it, and Set with a
// Value validates and replaces it.
type ProjectEntryEdit struct {
	Set   bool
	Value *ProjectEntry
}

// ErrInvalidProjectEntry reports unsafe or unusable project-entry metadata.
var ErrInvalidProjectEntry = errors.New("invalid project entry")

// ProjectEntryPathKey aliases the canonical workspace shared_data key so older
// callers importing projecttemplates keep one stable metadata contract.
const (
	ProjectEntryPathKey              = workspace.ProjectEntryPathKey
	ProjectEntryLocatorKey           = workspace.ProjectEntryLocatorKey
	ProjectEntryLocatorSchemaVersion = workspace.ProjectEntryLocatorSchemaVersion
)

type ProjectEntryLocator = workspace.ProjectEntryLocator

const (
	ProjectEntryManagedWorkspace   = workspace.ProjectEntryManagedWorkspace
	ProjectEntryDirectoryReference = workspace.ProjectEntryDirectoryReference
)

var projectEntryTokenPattern = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// ValidateProjectEntryPath validates and normalizes the portable, slash-based
// path stored in template manifests. It deliberately does not use host-only
// filepath rules so a manifest accepted on macOS cannot become an absolute or
// traversing path on Windows.
func ValidateProjectEntryPath(relativePath string) (string, error) {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" {
		return "", fmt.Errorf("%w: relative_path is required", ErrInvalidProjectEntry)
	}
	if strings.ContainsRune(relativePath, '\x00') {
		return "", fmt.Errorf("%w: relative_path contains a NUL byte", ErrInvalidProjectEntry)
	}
	if strings.Contains(relativePath, `\`) {
		return "", fmt.Errorf("%w: relative_path must use forward slashes", ErrInvalidProjectEntry)
	}
	if path.IsAbs(relativePath) || strings.HasPrefix(relativePath, "//") || hasWindowsVolumePrefix(relativePath) {
		return "", fmt.Errorf("%w: relative_path must be relative to the generated project", ErrInvalidProjectEntry)
	}

	for _, segment := range strings.Split(relativePath, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("%w: relative_path cannot contain traversal segments", ErrInvalidProjectEntry)
		}
	}

	for _, token := range projectEntryTokenPattern.FindAllString(relativePath, -1) {
		if token != "{{name}}" && token != "{{date}}" {
			return "", fmt.Errorf("%w: unsupported token %q (use only {{name}} or {{date}})", ErrInvalidProjectEntry, token)
		}
	}
	withoutSupported := strings.NewReplacer("{{name}}", "", "{{date}}", "").Replace(relativePath)
	if strings.Contains(withoutSupported, "{{") || strings.Contains(withoutSupported, "}}") {
		return "", fmt.Errorf("%w: malformed or unsupported filename token", ErrInvalidProjectEntry)
	}

	clean := path.Clean(relativePath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: relative_path must name a file inside the generated project", ErrInvalidProjectEntry)
	}
	return clean, nil
}

func hasWindowsVolumePrefix(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':'
}

// normalizeManifestProjectEntry decodes and verifies a hand-authored manifest
// entry. Invalid metadata is returned as a warning by the caller and is never
// exposed through the template API.
func normalizeManifestProjectEntry(templateDir string, raw json.RawMessage) (*ProjectEntry, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}

	var entry ProjectEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, fmt.Errorf("%w: project_entry must be an object: %v", ErrInvalidProjectEntry, err)
	}
	return normalizeProjectEntry(templateDir, &entry)
}

// normalizeProjectEntry validates an authoring value and confirms that its
// literal manifest path names a regular, non-symlink scaffold source file.
func normalizeProjectEntry(templateDir string, entry *ProjectEntry) (*ProjectEntry, error) {
	if entry == nil {
		return nil, nil
	}
	clean, err := ValidateProjectEntryPath(entry.RelativePath)
	if err != nil {
		return nil, err
	}
	if err := verifyTemplateEntrySource(templateDir, clean); err != nil {
		return nil, err
	}
	return &ProjectEntry{
		RelativePath:           clean,
		OpenAfterCreateDefault: entry.OpenAfterCreateDefault,
	}, nil
}

func verifyTemplateEntrySource(templateDir, portablePath string) error {
	current := filepath.Clean(templateDir)
	segments := strings.Split(portablePath, "/")
	for index, segment := range segments {
		current = filepath.Join(current, filepath.FromSlash(segment))
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%w: relative_path %q does not name a scaffolded file", ErrInvalidProjectEntry, portablePath)
			}
			return fmt.Errorf("%w: failed to inspect relative_path %q: %v", ErrInvalidProjectEntry, portablePath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: relative_path %q cannot contain symlinks", ErrInvalidProjectEntry, portablePath)
		}
		if index < len(segments)-1 {
			if !info.IsDir() {
				return fmt.Errorf("%w: relative_path %q has a non-directory parent", ErrInvalidProjectEntry, portablePath)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: relative_path %q must name a regular file", ErrInvalidProjectEntry, portablePath)
		}
	}
	return nil
}

// ResolveProjectEntryForName computes the portable entry expected from one
// normalized template and project name without creating files. Commit paths
// must still verify the materialized regular file.
func ResolveProjectEntryForName(template Template, projectName string) (string, error) {
	if template.ProjectEntry == nil {
		return "", ErrInvalidProjectEntry
	}
	slug, err := SanitizeProjectName(projectName)
	if err != nil {
		return "", ErrInvalidProjectEntry
	}
	values := newTemplateTokenValues(slug)
	clean, err := ValidateProjectEntryPath(template.ProjectEntry.RelativePath)
	if err != nil {
		return "", err
	}
	resolved, err := substituteRelPathWithValues(clean, values)
	if err != nil {
		return "", fmt.Errorf("%w: failed to resolve project entry", ErrInvalidProjectEntry)
	}
	portable := filepath.ToSlash(resolved)
	if strings.Contains(portable, "{{") || strings.Contains(portable, "}}") {
		return "", fmt.Errorf("%w: project entry contains an unresolved token", ErrInvalidProjectEntry)
	}
	return ValidateProjectEntryPath(portable)
}

func resolveInstantiatedProjectEntry(projectRoot string, entry *ProjectEntry, values templateTokenValues) (string, error) {
	if entry == nil {
		return "", nil
	}
	clean, err := ValidateProjectEntryPath(entry.RelativePath)
	if err != nil {
		return "", err
	}
	resolved, err := substituteRelPathWithValues(clean, values)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidProjectEntry, err)
	}
	portable := filepath.ToSlash(resolved)
	if strings.Contains(portable, "{{") || strings.Contains(portable, "}}") {
		return "", fmt.Errorf("%w: project entry contains an unresolved token", ErrInvalidProjectEntry)
	}

	root := filepath.Clean(projectRoot)
	target := filepath.Clean(filepath.Join(root, filepath.FromSlash(portable)))
	if target == root || !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: resolved project entry escapes the generated project", ErrInvalidProjectEntry)
	}
	if err := verifyTemplateEntrySource(root, portable); err != nil {
		return "", err
	}
	return portable, nil
}

// SetProjectEntryPath validates and stores a resolved portable entry path. An
// empty value clears the key. The map is intentionally supplied by callers so
// canonical folder and mirrored session metadata use the same implementation.
func SetProjectEntryPath(sharedData map[string]any, relativePath string) error {
	if err := workspace.SetProjectEntryPath(sharedData, relativePath); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProjectEntry, err)
	}
	return nil
}

func SetProjectEntryLocator(sharedData map[string]any, locator ProjectEntryLocator) error {
	return workspace.SetProjectEntryLocator(sharedData, locator)
}

func GetProjectEntryLocator(sharedData map[string]any) (*ProjectEntryLocator, error) {
	return workspace.GetProjectEntryLocator(sharedData)
}

// GetProjectEntryPath returns a validated resolved path from shared_data. A
// missing key is not an error; a malformed/wrongly typed stored value is.
func GetProjectEntryPath(sharedData map[string]any) (string, error) {
	value, err := workspace.GetProjectEntryPath(sharedData)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidProjectEntry, err)
	}
	return value, nil
}

// ClearProjectEntryPath removes persisted project-entry metadata.
func ClearProjectEntryPath(sharedData map[string]any) {
	workspace.ClearProjectEntryPath(sharedData)
}
