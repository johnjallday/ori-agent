package projecttemplates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const (
	ProjectConnectionSchemaVersion = 1
	maxProjectConnectionBytes      = 8 << 10
	maxAttachEntryExtensions       = 8
)

type ProjectConnectionMode string

const (
	ProjectConnectionNewProject      ProjectConnectionMode = "new_project"
	ProjectConnectionExistingProject ProjectConnectionMode = "existing_project"
)

// ProjectConnectionDeclaration is inert blueprint data. It declares which
// generic host-owned project connection modes are compatible; it cannot select
// a picker, scanner, path resolver, adapter, route, or implementation.
type ProjectConnectionDeclaration struct {
	SchemaVersion  int                        `json:"schema_version"`
	SupportedModes []ProjectConnectionMode    `json:"supported_modes"`
	AttachExisting *AttachExistingDeclaration `json:"attach_existing,omitempty"`
}

// AttachExistingDeclaration constrains only portable filename extensions. The
// host owns bounded discovery, exact selection, containment, and commit.
type AttachExistingDeclaration struct {
	EntryExtensions []string `json:"entry_extensions"`
}

var ErrInvalidProjectConnection = errors.New("invalid project connection declaration")

var entryExtensionPattern = regexp.MustCompile(`^\.[a-z0-9]{1,15}$`)

func normalizeProjectConnection(raw json.RawMessage) (*ProjectConnectionDeclaration, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if len(trimmed) > maxProjectConnectionBytes {
		return nil, fmt.Errorf("%w: declaration exceeds size limit", ErrInvalidProjectConnection)
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var declaration ProjectConnectionDeclaration
	if err := decoder.Decode(&declaration); err != nil {
		return nil, fmt.Errorf("%w: malformed declaration", ErrInvalidProjectConnection)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalidProjectConnection)
	}
	if declaration.SchemaVersion != ProjectConnectionSchemaVersion ||
		len(declaration.SupportedModes) == 0 || len(declaration.SupportedModes) > 2 {
		return nil, fmt.Errorf("%w: unsupported schema or mode count", ErrInvalidProjectConnection)
	}
	seenModes := make(map[ProjectConnectionMode]struct{}, len(declaration.SupportedModes))
	for index, mode := range declaration.SupportedModes {
		mode = ProjectConnectionMode(strings.ToLower(strings.TrimSpace(string(mode))))
		if mode != ProjectConnectionNewProject && mode != ProjectConnectionExistingProject {
			return nil, fmt.Errorf("%w: unsupported connection mode", ErrInvalidProjectConnection)
		}
		if _, duplicate := seenModes[mode]; duplicate {
			return nil, fmt.Errorf("%w: duplicate connection mode", ErrInvalidProjectConnection)
		}
		seenModes[mode] = struct{}{}
		declaration.SupportedModes[index] = mode
	}
	_, supportsExisting := seenModes[ProjectConnectionExistingProject]
	if supportsExisting != (declaration.AttachExisting != nil) {
		return nil, fmt.Errorf("%w: attach declaration must match existing-project support", ErrInvalidProjectConnection)
	}
	if declaration.AttachExisting != nil {
		extensions := declaration.AttachExisting.EntryExtensions
		if len(extensions) == 0 || len(extensions) > maxAttachEntryExtensions {
			return nil, fmt.Errorf("%w: entry extension count is invalid", ErrInvalidProjectConnection)
		}
		seenExtensions := make(map[string]struct{}, len(extensions))
		for index, extension := range extensions {
			extension = strings.ToLower(strings.TrimSpace(extension))
			if !entryExtensionPattern.MatchString(extension) {
				return nil, fmt.Errorf("%w: entry extension is invalid", ErrInvalidProjectConnection)
			}
			if _, duplicate := seenExtensions[extension]; duplicate {
				return nil, fmt.Errorf("%w: duplicate entry extension", ErrInvalidProjectConnection)
			}
			seenExtensions[extension] = struct{}{}
			extensions[index] = extension
		}
		sort.Strings(extensions)
		declaration.AttachExisting.EntryExtensions = extensions
	}
	sort.Slice(declaration.SupportedModes, func(i, j int) bool {
		return declaration.SupportedModes[i] < declaration.SupportedModes[j]
	})
	return cloneProjectConnection(&declaration), nil
}

func normalizeStarterTaskConnectionModes(tasks []StarterTask, declaration *ProjectConnectionDeclaration) error {
	supported := make(map[ProjectConnectionMode]struct{})
	if declaration != nil {
		for _, mode := range declaration.SupportedModes {
			supported[mode] = struct{}{}
		}
	}
	for index := range tasks {
		seen := make(map[ProjectConnectionMode]struct{}, len(tasks[index].ConnectionModes))
		for modeIndex, rawMode := range tasks[index].ConnectionModes {
			mode := ProjectConnectionMode(strings.ToLower(strings.TrimSpace(string(rawMode))))
			if mode != ProjectConnectionNewProject && mode != ProjectConnectionExistingProject {
				return fmt.Errorf("%w: starter task has an unsupported connection mode", ErrInvalidProjectConnection)
			}
			if _, duplicate := seen[mode]; duplicate {
				return fmt.Errorf("%w: starter task repeats a connection mode", ErrInvalidProjectConnection)
			}
			if _, available := supported[mode]; !available {
				return fmt.Errorf("%w: starter task references an unavailable connection mode", ErrInvalidProjectConnection)
			}
			seen[mode] = struct{}{}
			tasks[index].ConnectionModes[modeIndex] = mode
		}
		sort.Slice(tasks[index].ConnectionModes, func(i, j int) bool {
			return tasks[index].ConnectionModes[i] < tasks[index].ConnectionModes[j]
		})
	}
	return nil
}

// StarterTasksForConnection projects only tasks allowed for one reviewed
// connection mode. It does not seed or run them.
func StarterTasksForConnection(template Template, mode ProjectConnectionMode) ([]StarterTask, error) {
	if template.ProjectConnection == nil || template.HasInvalidProjectConnection() ||
		(mode != ProjectConnectionNewProject && mode != ProjectConnectionExistingProject) ||
		!projectConnectionSupports(template.ProjectConnection, mode) {
		return nil, ErrInvalidProjectConnection
	}
	result := make([]StarterTask, 0, len(template.StarterTasks))
	for _, task := range template.StarterTasks {
		if len(task.ConnectionModes) != 0 && !starterTaskSupports(task, mode) {
			continue
		}
		clone := task
		clone.Requires = append([]string(nil), task.Requires...)
		clone.FileFallbackFor = append([]string(nil), task.FileFallbackFor...)
		clone.ConnectionModes = append([]ProjectConnectionMode(nil), task.ConnectionModes...)
		result = append(result, clone)
	}
	return result, nil
}

// Supports reports whether this normalized declaration offers a fixed host-
// owned mode. It grants no authority and selects no implementation.
func (declaration *ProjectConnectionDeclaration) Supports(mode ProjectConnectionMode) bool {
	return projectConnectionSupports(declaration, mode)
}

func projectConnectionSupports(declaration *ProjectConnectionDeclaration, mode ProjectConnectionMode) bool {
	if declaration == nil {
		return false
	}
	for _, supported := range declaration.SupportedModes {
		if supported == mode {
			return true
		}
	}
	return false
}

func starterTaskSupports(task StarterTask, mode ProjectConnectionMode) bool {
	for _, supported := range task.ConnectionModes {
		if supported == mode {
			return true
		}
	}
	return false
}

func cloneProjectConnection(source *ProjectConnectionDeclaration) *ProjectConnectionDeclaration {
	if source == nil {
		return nil
	}
	clone := *source
	clone.SupportedModes = append([]ProjectConnectionMode(nil), source.SupportedModes...)
	if source.AttachExisting != nil {
		attach := *source.AttachExisting
		attach.EntryExtensions = append([]string(nil), source.AttachExisting.EntryExtensions...)
		clone.AttachExisting = &attach
	}
	return &clone
}
