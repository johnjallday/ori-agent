package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode"
)

const (
	// ProjectEntryPathKey is the legacy managed-workspace relative path key.
	ProjectEntryPathKey = "project_entry_path"
	// ProjectEntryLocatorKey is the typed authoritative locator. It contains no
	// absolute path; directory reference IDs are resolved from the workspace.
	ProjectEntryLocatorKey           = "project_entry"
	ProjectEntryLocatorSchemaVersion = 1
	maxProjectEntryLocatorBytes      = 1024
)

type ProjectEntryLocatorKind string

const (
	ProjectEntryManagedWorkspace   ProjectEntryLocatorKind = "managed_workspace"
	ProjectEntryDirectoryReference ProjectEntryLocatorKind = "directory_reference"
)

type ProjectEntryLocator struct {
	SchemaVersion        int                     `json:"schema_version"`
	Kind                 ProjectEntryLocatorKind `json:"kind"`
	DirectoryReferenceID string                  `json:"directory_reference_id,omitempty"`
	RelativePath         string                  `json:"relative_path"`
}

var ErrInvalidProjectEntryPath = errors.New("invalid project entry path")

// SetProjectEntryLocator writes a normalized typed locator. Managed locators
// also maintain the legacy string for older readers; external-reference
// locators remove it so no caller can accidentally reinterpret the path under
// the managed workspace root.
func SetProjectEntryLocator(sharedData map[string]any, locator ProjectEntryLocator) error {
	if sharedData == nil {
		return fmt.Errorf("%w: workspace shared_data is unavailable", ErrInvalidProjectEntryPath)
	}
	normalized, err := normalizeProjectEntryLocator(locator)
	if err != nil {
		return err
	}
	sharedData[ProjectEntryLocatorKey] = map[string]any{
		"schema_version": normalized.SchemaVersion,
		"kind":           string(normalized.Kind),
		"relative_path":  normalized.RelativePath,
	}
	if normalized.Kind == ProjectEntryDirectoryReference {
		sharedData[ProjectEntryLocatorKey].(map[string]any)["directory_reference_id"] = normalized.DirectoryReferenceID
		delete(sharedData, ProjectEntryPathKey)
	} else {
		sharedData[ProjectEntryPathKey] = normalized.RelativePath
	}
	return nil
}

// GetProjectEntryLocator strictly reads the typed locator. When only the legacy
// string exists it returns the equivalent managed locator without mutating.
func GetProjectEntryLocator(sharedData map[string]any) (*ProjectEntryLocator, error) {
	if sharedData == nil {
		return nil, nil
	}
	raw, hasTyped := sharedData[ProjectEntryLocatorKey]
	if !hasTyped || raw == nil {
		legacy, err := getLegacyProjectEntryPath(sharedData)
		if err != nil || legacy == "" {
			return nil, err
		}
		return &ProjectEntryLocator{
			SchemaVersion: ProjectEntryLocatorSchemaVersion,
			Kind:          ProjectEntryManagedWorkspace, RelativePath: legacy,
		}, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil || len(encoded) > maxProjectEntryLocatorBytes {
		return nil, fmt.Errorf("%w: typed locator is malformed", ErrInvalidProjectEntryPath)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var locator ProjectEntryLocator
	if err := decoder.Decode(&locator); err != nil {
		return nil, fmt.Errorf("%w: typed locator is malformed", ErrInvalidProjectEntryPath)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: typed locator has trailing data", ErrInvalidProjectEntryPath)
	}
	normalized, err := normalizeProjectEntryLocator(locator)
	if err != nil {
		return nil, err
	}
	legacy, legacyErr := getLegacyProjectEntryPath(sharedData)
	if legacyErr != nil {
		return nil, legacyErr
	}
	if legacy != "" && (normalized.Kind != ProjectEntryManagedWorkspace || legacy != normalized.RelativePath) {
		return nil, fmt.Errorf("%w: typed and legacy locators disagree", ErrInvalidProjectEntryPath)
	}
	return &normalized, nil
}

func normalizeProjectEntryLocator(locator ProjectEntryLocator) (ProjectEntryLocator, error) {
	locator.DirectoryReferenceID = strings.TrimSpace(locator.DirectoryReferenceID)
	relativePath, err := validateResolvedProjectEntryPath(locator.RelativePath)
	if err != nil {
		return ProjectEntryLocator{}, err
	}
	locator.RelativePath = relativePath
	if locator.SchemaVersion != ProjectEntryLocatorSchemaVersion {
		return ProjectEntryLocator{}, fmt.Errorf("%w: unsupported locator schema", ErrInvalidProjectEntryPath)
	}
	switch locator.Kind {
	case ProjectEntryManagedWorkspace:
		if locator.DirectoryReferenceID != "" {
			return ProjectEntryLocator{}, fmt.Errorf("%w: managed locator cannot name a directory reference", ErrInvalidProjectEntryPath)
		}
	case ProjectEntryDirectoryReference:
		if !validProjectEntryReferenceID(locator.DirectoryReferenceID) {
			return ProjectEntryLocator{}, fmt.Errorf("%w: directory reference ID is invalid", ErrInvalidProjectEntryPath)
		}
	default:
		return ProjectEntryLocator{}, fmt.Errorf("%w: locator kind is unsupported", ErrInvalidProjectEntryPath)
	}
	return locator, nil
}

func validProjectEntryReferenceID(value string) bool {
	if value == "" || len(value) > 128 || strings.ContainsAny(value, `/\\`) {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) || unicode.IsSpace(char) {
			return false
		}
	}
	return true
}

// SetProjectEntryPath preserves the legacy API while making new writes typed.
func SetProjectEntryPath(sharedData map[string]any, relativePath string) error {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" {
		ClearProjectEntryPath(sharedData)
		return nil
	}
	return SetProjectEntryLocator(sharedData, ProjectEntryLocator{
		SchemaVersion: ProjectEntryLocatorSchemaVersion,
		Kind:          ProjectEntryManagedWorkspace, RelativePath: relativePath,
	})
}

// GetProjectEntryPath is retained for legacy managed-only consumers. Typed
// directory-reference entries fail closed rather than returning a path that a
// caller could resolve under the wrong root.
func GetProjectEntryPath(sharedData map[string]any) (string, error) {
	locator, err := GetProjectEntryLocator(sharedData)
	if err != nil || locator == nil {
		return "", err
	}
	if locator.Kind != ProjectEntryManagedWorkspace {
		return "", fmt.Errorf("%w: entry requires the typed resolver", ErrInvalidProjectEntryPath)
	}
	return locator.RelativePath, nil
}

func getLegacyProjectEntryPath(sharedData map[string]any) (string, error) {
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

func ClearProjectEntryPath(sharedData map[string]any) {
	if sharedData != nil {
		delete(sharedData, ProjectEntryPathKey)
		delete(sharedData, ProjectEntryLocatorKey)
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
