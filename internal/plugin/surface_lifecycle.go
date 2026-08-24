package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// SurfaceLifecycle resolves trusted installed-plugin records into the one
// process-wide owner-aware registry. Workspace files never call this API and
// never supply a Binding.
type SurfaceLifecycle struct {
	registry    *workspacesurface.Registry
	services    *workspacesurface.ServiceManager
	state       *workspacesurface.StateStore
	invalidator func(string, uint64)
}

func NewSurfaceLifecycle(registry *workspacesurface.Registry, services *workspacesurface.ServiceManager) *SurfaceLifecycle {
	if registry == nil {
		registry = workspacesurface.NewRegistry()
	}
	if services == nil {
		services = workspacesurface.NewServiceManager(nil)
	}
	return &SurfaceLifecycle{registry: registry, services: services}
}

func (l *SurfaceLifecycle) SetStateStore(store *workspacesurface.StateStore) {
	if l != nil {
		l.state = store
	}
}

func (l *SurfaceLifecycle) SetSessionInvalidator(invalidator func(string, uint64)) {
	if l != nil {
		l.invalidator = invalidator
	}
}

func (l *SurfaceLifecycle) RegisterInstalled(installed InstalledPlugin) error {
	if l == nil || installed.WorkspaceSurfaces == nil {
		return nil
	}
	registration, err := l.registration(installed)
	if err != nil {
		return err
	}
	return l.registry.RegisterTrusted(registration)
}

// Unregister stops service calls/processes before removing trusted bindings.
// Callers may change component files only after it returns.
func (l *SurfaceLifecycle) Unregister(pluginID string, generation uint64) error {
	if l == nil {
		return nil
	}
	if l.invalidator != nil {
		l.invalidator(pluginID, generation)
	}
	if err := l.services.StopPlugin(pluginID, generation); err != nil {
		return err
	}
	return l.registry.UnregisterOwner(workspacesurface.OwnerPlugin, pluginID, generation)
}

// Replace performs stop/unregister/register in lifecycle order. If the new
// trusted registration fails, it restores the old descriptor/bindings so an
// update never silently leaves an enabled plugin absent.
func (l *SurfaceLifecycle) Replace(previous, next InstalledPlugin) error {
	if l == nil {
		return nil
	}
	if previous.WorkspaceSurfaces != nil {
		if err := l.Unregister(previous.Name, previous.Generation); err != nil {
			return err
		}
	}
	if err := l.RegisterInstalled(next); err != nil {
		if previous.WorkspaceSurfaces != nil {
			_ = l.RegisterInstalled(previous)
		}
		return err
	}
	return nil
}

func (l *SurfaceLifecycle) DeleteState(pluginID string) error {
	if l == nil || l.state == nil {
		return nil
	}
	return l.state.DeletePlugin(pluginID)
}

func (l *SurfaceLifecycle) Shutdown() error {
	if l == nil || l.services == nil {
		return nil
	}
	return l.services.Shutdown()
}

func (l *SurfaceLifecycle) Restore(installed []InstalledPlugin) error {
	var joined error
	for _, plugin := range installed {
		if err := l.RegisterInstalled(plugin); err != nil {
			joined = errors.Join(joined, fmt.Errorf("restore plugin %q surfaces: %w", plugin.Name, err))
		}
	}
	return joined
}

func (l *SurfaceLifecycle) registration(installed InstalledPlugin) (workspacesurface.Registration, error) {
	contribution := installed.WorkspaceSurfaces
	owner := workspacesurface.Owner{
		Kind: workspacesurface.OwnerPlugin, ID: strings.ToLower(strings.TrimSpace(installed.Name)),
		Version: strings.TrimSpace(installed.Version), Generation: installed.Generation,
		ProtocolMin: contribution.Protocol.Min, ProtocolMax: contribution.Protocol.Max,
	}
	registration := workspacesurface.Registration{Owner: owner}
	serviceByID := make(map[string]ContributedService, len(contribution.Services))
	artifactByService := make(map[string]ResolvedArtifact, len(installed.ResolvedArtifacts))
	for _, service := range contribution.Services {
		serviceByID[service.ID] = service
	}
	for _, artifact := range installed.ResolvedArtifacts {
		artifactByService[artifact.ServiceID] = artifact
	}
	unavailable := ""
	if !installed.Enabled {
		unavailable = "plugin_disabled"
	}

	for _, capability := range contribution.Capabilities {
		inert := workspacesurface.Capability{
			ID: capability.ID, Version: capability.Version,
			Display: workspacesurface.Display{Name: capability.Display.Name, Description: capability.Display.Description},
		}
		for _, surface := range capability.Surfaces {
			inert.Surfaces = append(inert.Surfaces, workspacesurface.Surface{
				ID: surface.ID, Label: surface.Label, Description: surface.Description,
				Icon:         workspacesurface.Icon{Kind: surface.Icon.Kind, Value: surface.Icon.Value},
				Placement:    surface.Placement,
				Modal:        workspacesurface.Modal{Width: surface.Modal.Width, Height: surface.Modal.Height},
				Polling:      workspacesurface.Polling{MapSeconds: surface.Polling.MapSeconds, OpenSeconds: surface.Polling.OpenSeconds},
				OperationIDs: append([]string(nil), surface.Operations...), StatusOperation: surface.StatusOperation,
				StateEnabled: surface.HostIntents.State, ConfirmationEnabled: surface.HostIntents.Confirmation,
				CloseEnabled:       surface.HostIntents.Close,
				AskOriCapabilities: append([]string(nil), surface.HostIntents.AskOri.RequiredCapabilities...),
				SetupProviderID:    surface.HostIntents.OpenSetup.ProviderID,
			})
			if unavailable != "" {
				continue
			}
			service, exists := serviceByID[capability.ServiceID]
			if !exists {
				return workspacesurface.Registration{}, fmt.Errorf("capability %q service is unavailable", capability.ID)
			}
			artifact := artifactByService[service.ID]
			if !artifact.Available {
				unavailable = artifact.Unavailable
				if unavailable == "" {
					unavailable = "platform_unsupported"
				}
				continue
			}
			spec := workspacesurface.ServiceSpec{
				PluginID: owner.ID, PluginGeneration: owner.Generation, ServiceID: service.ID,
				Command: artifact.ManagedPath, Args: append([]string(nil), service.Entrypoint.Args...),
				MaxConcurrency: 8, StartupTimeout: 10 * time.Second, ShutdownTimeout: 5 * time.Second,
			}
			operations := make(map[string]workspacesurface.Operation, len(surface.Operations))
			serviceOperations := make(map[string]ContributedOperation, len(service.Operations))
			for _, operation := range service.Operations {
				serviceOperations[operation.ID] = operation
			}
			for _, operationID := range surface.Operations {
				operation, exists := serviceOperations[operationID]
				if !exists {
					return workspacesurface.Registration{}, fmt.Errorf("surface %q operation %q is unavailable", surface.ID, operationID)
				}
				operations[operationID] = runtimeOperation(operation)
			}
			registration.Bindings = append(registration.Bindings, workspacesurface.Binding{
				CapabilityID: capability.ID, SurfaceID: surface.ID,
				AssetRoot: installed.InstallDir, AssetVersion: installed.ComponentFingerprint,
				EntryAsset: surface.EntryAsset, Operations: operations,
				Runtime: &serviceRuntime{manager: l.services, spec: spec, statusOperation: surface.StatusOperation},
			})
		}
		registration.Capabilities = append(registration.Capabilities, inert)
	}
	if unavailable != "" {
		registration.Bindings = nil
		registration.UnavailableCode = unavailable
	}
	return registration, nil
}

func runtimeOperation(operation ContributedOperation) workspacesurface.Operation {
	return workspacesurface.Operation{
		ID: operation.ID, InputSchema: append(json.RawMessage(nil), operation.InputSchema...),
		OutputSchema:   append(json.RawMessage(nil), operation.OutputSchema...),
		MaxOutputBytes: operation.MaxOutputBytes,
		Timeout:        workspacesurface.TimeoutClass(operation.TimeoutClass),
		Policy:         workspacesurface.PolicyClass(operation.Policy), Scopes: append([]string(nil), operation.Scopes...),
	}
}

type serviceRuntime struct {
	manager         *workspacesurface.ServiceManager
	spec            workspacesurface.ServiceSpec
	statusOperation string
}

func (r *serviceRuntime) Status(ctx context.Context, workspace workspacesurface.WorkspaceContext) (workspacesurface.StationStatus, error) {
	if r.statusOperation == "" {
		return workspacesurface.StationStatus{State: workspacesurface.StationReady, Value: "Available"}, nil
	}
	output, err := r.invoke(ctx, workspace, r.statusOperation, json.RawMessage(`{}`), 3*time.Second)
	if err != nil {
		return workspacesurface.StationStatus{}, err
	}
	var status workspacesurface.StationStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return workspacesurface.StationStatus{}, workspacesurface.ErrServiceUnavailable
	}
	return status, nil
}

func (r *serviceRuntime) Invoke(ctx context.Context, invocation workspacesurface.Invocation) (workspacesurface.Result, error) {
	timeout := 15 * time.Second
	output, err := r.invoke(ctx, invocation.Workspace, invocation.Operation, invocation.Input, timeout)
	return workspacesurface.Result{Output: output}, err
}

func (r *serviceRuntime) invoke(ctx context.Context, workspace workspacesurface.WorkspaceContext, operation string, input json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	var inputValue any
	if err := json.Unmarshal(input, &inputValue); err != nil {
		return nil, workspacesurface.ErrInputInvalid
	}
	arguments := map[string]any{
		"protocol_version": workspacesurface.ProtocolVersion,
		"operation_id":     operation,
		"context": map[string]any{
			"workspace_id":     workspace.WorkspaceID,
			"workspace_root":   workspace.WorkspaceRoot,
			"project_entry":    workspace.ProjectEntry,
			"plugin_data_root": workspace.PluginDataRoot,
			"scopes":           workspace.Scopes,
		},
		"input": inputValue,
	}
	return r.manager.Call(ctx, r.spec, workspacesurface.ServiceCall{Operation: operation, Arguments: arguments, Timeout: timeout})
}
