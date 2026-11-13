package plugin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// ProjectFactsRef is the manifest's `project_facts`: the one read-only service
// operation that reads project files the host names and returns the facts the
// host keeps for each song (assistant-reads-projects). Formats are the host's
// project format IDs this reader covers, the same opaque IDs the host's folder
// scan gives each song, so the host never names an application or a file
// extension to match a song to its reader.
type ProjectFactsRef struct {
	ServiceID string   `json:"service_id"`
	Operation string   `json:"operation"`
	Formats   []string `json:"formats"`
}

// ErrProjectFactsUnavailable means the installed plugin has no usable project
// reader right now: it declares none, it is disabled, or its service artifact is
// not available on this computer.
var ErrProjectFactsUnavailable = errors.New("plugin has no project facts operation")

// Bounds of a project_facts declaration and of one call.
const (
	// MaxProjectFactsFormats bounds the formats one reader covers.
	MaxProjectFactsFormats = 8
	// ProjectFactsMaxFilesPerCall is the most files one call carries.
	ProjectFactsMaxFilesPerCall = 8
	// ProjectFactsBytesPerFile is the output a reader must allow per file: a
	// song read with every name at its caps is about 31 KB.
	ProjectFactsBytesPerFile = 32768
)

// validateProjectFacts requires the reference to name a declared service and
// one of its read_only operations with the long timeout class and room for at
// least one file's output, and 1 to 8 distinct canonical formats. Reading files
// never changes anything, so no other policy can be named here.
func validateProjectFacts(ref *ProjectFactsRef, services map[string]*ContributedService) error {
	if ref == nil {
		return nil
	}
	const component = "project_facts"
	if len(ref.Formats) == 0 || len(ref.Formats) > MaxProjectFactsFormats {
		return contributionError(CodeContributionInvalid, component, "formats", "a project reader covers 1 to 8 formats", nil)
	}
	seen := make(map[string]struct{}, len(ref.Formats))
	for _, format := range ref.Formats {
		if !canonicalIDValue(format) {
			return contributionError(CodeContributionInvalid, component, "formats", "a format is not a canonical identifier", nil)
		}
		if _, duplicate := seen[format]; duplicate {
			return contributionError(CodeContributionInvalid, component, "formats", "formats must be unique", nil)
		}
		seen[format] = struct{}{}
	}
	service := services[ref.ServiceID]
	if service == nil {
		return contributionError(CodeComponentUnknown, component, "service_id", "project reader references an unknown service", nil)
	}
	for _, operation := range service.Operations {
		if operation.ID != ref.Operation {
			continue
		}
		if operation.Policy != string(workspacesurface.PolicyReadOnly) {
			return contributionError(CodeOperationPolicyInvalid, component, "operation", "project reader must be read_only", nil)
		}
		if operation.TimeoutClass != string(workspacesurface.TimeoutLong) {
			return contributionError(CodeContributionInvalid, component, "operation", "project reader must use the long timeout class", nil)
		}
		if operation.MaxOutputBytes < ProjectFactsBytesPerFile {
			return contributionError(CodeContributionInvalid, component, "operation", "project reader must allow at least 32768 output bytes", nil)
		}
		if len(operation.Scopes) > 0 {
			return contributionError(CodeContributionInvalid, component, "operation", "project reader must request no scopes", nil)
		}
		return nil
	}
	return contributionError(CodeComponentUnknown, component, "operation", "project reader is not declared by that service", nil)
}

// DeclaresProjectFacts reports whether an installed, enabled plugin names a
// project reader. It runs nothing, and says nothing about whether the host may
// call it: that is the reviewed list's decision, made by the caller.
func DeclaresProjectFacts(installed InstalledPlugin) bool {
	return installed.Enabled && installed.WorkspaceSurfaces != nil && installed.WorkspaceSurfaces.ProjectFacts != nil
}

// ProjectFactsFormats are the formats an installed plugin's reader covers, or
// nil when it declares none.
func ProjectFactsFormats(installed InstalledPlugin) []string {
	if !DeclaresProjectFacts(installed) {
		return nil
	}
	return append([]string(nil), installed.WorkspaceSurfaces.ProjectFacts.Formats...)
}

// ProjectFactsBatchFiles is how many files one call to the plugin's reader may
// carry: what its declared output limit leaves room for, at most 8; 0 when it
// declares no usable reader.
func ProjectFactsBatchFiles(installed InstalledPlugin) int {
	if !DeclaresProjectFacts(installed) {
		return 0
	}
	ref := installed.WorkspaceSurfaces.ProjectFacts
	for _, service := range installed.WorkspaceSurfaces.Services {
		if service.ID != ref.ServiceID {
			continue
		}
		for _, operation := range service.Operations {
			if operation.ID == ref.Operation {
				return min(ProjectFactsMaxFilesPerCall, operation.MaxOutputBytes/ProjectFactsBytesPerFile)
			}
		}
	}
	return 0
}

// ProjectFacts calls the installed plugin's declared project reader once and
// returns its output after the host's checks: the declared input schema,
// timeout class, output byte limit and output schema. The call is
// machine-level: no workspace, project, grant or scope. Which files to read,
// whether the plugin may be called at all, and what to keep of the output are
// the caller's.
func (l *SurfaceLifecycle) ProjectFacts(ctx context.Context, installed InstalledPlugin, input json.RawMessage) (json.RawMessage, error) {
	if l == nil || l.services == nil || !DeclaresProjectFacts(installed) {
		return nil, ErrProjectFactsUnavailable
	}
	ref := installed.WorkspaceSurfaces.ProjectFacts
	output, err := l.machineReadOnlyCall(ctx, installed, ref.ServiceID, ref.Operation, input)
	if errors.Is(err, errMachineOperationUnavailable) {
		return nil, ErrProjectFactsUnavailable
	}
	return output, err
}
