package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// HomeProfileFactsRef is the manifest's `home_profile_facts`: which service
// operation reports facts about the plugin's own application for a Home
// profile. The host calls only this one operation for that purpose, so it does
// not need to know the application.
type HomeProfileFactsRef struct {
	ServiceID string `json:"service_id"`
	Operation string `json:"operation"`
}

// ErrHomeProfileFactsUnavailable means the installed plugin has no usable
// facts operation right now: it declares none, it is disabled, or its service
// artifact is not available on this computer.
var ErrHomeProfileFactsUnavailable = errors.New("plugin has no Home profile facts operation")

// validateHomeProfileFacts requires the reference to name a declared service
// and one of its read-only operations. A facts read never changes anything, so
// an operation with any other policy cannot be named here.
func validateHomeProfileFacts(ref *HomeProfileFactsRef, services map[string]*ContributedService) error {
	if ref == nil {
		return nil
	}
	const component = "home_profile_facts"
	service := services[ref.ServiceID]
	if service == nil {
		return contributionError(CodeComponentUnknown, component, "service_id", "facts operation references an unknown service", nil)
	}
	for _, operation := range service.Operations {
		if operation.ID != ref.Operation {
			continue
		}
		if operation.Policy != string(workspacesurface.PolicyReadOnly) {
			return contributionError(CodeOperationPolicyInvalid, component, "operation", "facts operation must be read_only", nil)
		}
		return nil
	}
	return contributionError(CodeComponentUnknown, component, "operation", "facts operation is not declared by that service", nil)
}

// DeclaresHomeProfileFacts reports whether an installed, enabled plugin names a
// facts operation. It runs nothing.
func DeclaresHomeProfileFacts(installed InstalledPlugin) bool {
	return installed.Enabled && installed.WorkspaceSurfaces != nil && installed.WorkspaceSurfaces.HomeProfileFacts != nil
}

// HomeProfileFacts calls the installed plugin's declared facts operation once
// and returns its output after the host's checks: the declared timeout class,
// the declared output byte limit and the declared output schema. The call is
// machine-level, like a prerequisites check: it carries no workspace, project,
// grant or scope. Interpreting the output is the caller's job.
func (l *SurfaceLifecycle) HomeProfileFacts(ctx context.Context, installed InstalledPlugin, input json.RawMessage) (json.RawMessage, error) {
	if l == nil || l.services == nil || !DeclaresHomeProfileFacts(installed) {
		return nil, ErrHomeProfileFactsUnavailable
	}
	contribution := installed.WorkspaceSurfaces
	ref := contribution.HomeProfileFacts
	var service *ContributedService
	for index := range contribution.Services {
		if contribution.Services[index].ID == ref.ServiceID {
			service = &contribution.Services[index]
		}
	}
	if service == nil {
		return nil, ErrHomeProfileFactsUnavailable
	}
	var declared *ContributedOperation
	for index := range service.Operations {
		if service.Operations[index].ID == ref.Operation {
			declared = &service.Operations[index]
		}
	}
	if declared == nil || declared.Policy != string(workspacesurface.PolicyReadOnly) {
		return nil, ErrHomeProfileFactsUnavailable
	}
	var artifact *ResolvedArtifact
	for index := range installed.ResolvedArtifacts {
		if installed.ResolvedArtifacts[index].ServiceID == service.ID {
			artifact = &installed.ResolvedArtifacts[index]
		}
	}
	if artifact == nil || !artifact.Available {
		return nil, ErrHomeProfileFactsUnavailable
	}
	operation := runtimeOperation(*declared)
	if err := workspacesurface.ValidateOperationInput(operation, input); err != nil {
		return nil, err
	}
	var inputValue any
	if err := json.Unmarshal(input, &inputValue); err != nil {
		return nil, workspacesurface.ErrInputInvalid
	}
	// The same identity the plugin's surfaces are registered under, so this
	// call shares their service process instead of starting a second one.
	spec := workspacesurface.ServiceSpec{
		PluginID: strings.ToLower(strings.TrimSpace(installed.Name)), PluginGeneration: installed.Generation, ServiceID: service.ID,
		Command: artifact.ManagedPath, Args: append([]string(nil), service.Entrypoint.Args...),
		MaxConcurrency: 8, StartupTimeout: 10 * time.Second, ShutdownTimeout: 5 * time.Second,
	}
	// Input schemas may require the context keys even for a machine-only
	// operation. Empty values satisfy the envelope without inventing authority.
	output, err := l.services.Call(ctx, spec, workspacesurface.ServiceCall{
		Operation: operation.ID, Timeout: providerTimeout(operation.Timeout),
		Arguments: map[string]any{
			"protocol_version": workspacesurface.ProtocolVersion, "operation_id": operation.ID,
			"context": map[string]any{"workspace_id": "", "workspace_root": "", "project_entry": "", "plugin_data_root": "", "scopes": []string{}},
			"input":   inputValue,
		},
	})
	if err != nil {
		return nil, err
	}
	if len(output) > operation.MaxOutputBytes {
		return nil, workspacesurface.ErrServiceUnavailable
	}
	if err := workspacesurface.ValidateOperationOutput(operation, output); err != nil {
		return nil, workspacesurface.ErrServiceUnavailable
	}
	return output, nil
}
