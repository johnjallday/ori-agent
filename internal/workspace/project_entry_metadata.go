package workspace

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// ProjectEntryPathKey is the stable shared_data key containing a resolved,
// portable entry path relative to Workspace.ProjectPath.
const ProjectEntryPathKey = "project_entry_path"

// ErrInvalidProjectEntryPath reports malformed persisted entry metadata.
var ErrInvalidProjectEntryPath = errors.New("invalid project entry path")

// SetProjectEntryPath validates and stores a resolved portable entry path. An
// empty value clears the key.
func SetProjectEntryPath(sharedData map[string]any, relativePath string) error {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" {
		ClearProjectEntryPath(sharedData)
		return nil
	}
	if sharedData == nil {
		return fmt.Errorf("%w: workspace shared_data is unavailable", ErrInvalidProjectEntryPath)
	}
	clean, err := validateResolvedProjectEntryPath(relativePath)
	if err != nil {
		return err
	}
	sharedData[ProjectEntryPathKey] = clean
	return nil
}

// GetProjectEntryPath returns a validated path. A missing key is not an error;
// malformed or wrongly typed stored data is rejected.
func GetProjectEntryPath(sharedData map[string]any) (string, error) {
	if sharedData == nil {
		return "", nil
	}
	raw, ok := sharedData[ProjectEntryPathKey]
	if !ok || raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%w: stored %s must be a string", ErrInvalidProjectEntryPath, ProjectEntryPathKey)
	}
	return validateResolvedProjectEntryPath(value)
}

// ClearProjectEntryPath removes persisted entry metadata.
func ClearProjectEntryPath(sharedData map[string]any) {
	if sharedData != nil {
		delete(sharedData, ProjectEntryPathKey)
	}
}

func validateResolvedProjectEntryPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: path is empty", ErrInvalidProjectEntryPath)
	}
	if strings.ContainsRune(value, '\x00') || strings.Contains(value, `\`) {
		return "", fmt.Errorf("%w: path must use portable forward-slash segments", ErrInvalidProjectEntryPath)
	}
	if path.IsAbs(value) || strings.HasPrefix(value, "//") || hasPortableWindowsVolumePrefix(value) {
		return "", fmt.Errorf("%w: path must be relative", ErrInvalidProjectEntryPath)
	}
	if strings.Contains(value, "{{") || strings.Contains(value, "}}") {
		return "", fmt.Errorf("%w: resolved path cannot contain template tokens", ErrInvalidProjectEntryPath)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("%w: path cannot contain traversal segments", ErrInvalidProjectEntryPath)
		}
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: path escapes its project", ErrInvalidProjectEntryPath)
	}
	return clean, nil
}

func hasPortableWindowsVolumePrefix(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':'
}
