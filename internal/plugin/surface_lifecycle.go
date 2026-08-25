package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// SurfaceLifecycle resolves trusted installed-plugin records into the one
// process-wide owner-aware registry. Workspace files never call this API and
// never supply a Binding.
type SurfaceLifecycle struct {
	registry     *workspacesurface.Registry
	services     *workspacesurface.ServiceManager
	state        *workspacesurface.StateStore
	capabilities *workspacecapability.Registry
	runtimes     *runtimecapability.Registry
	contexts     RuntimeContextResolver
	scopes       SymbolicScopeResolver
	invalidator  func(string, uint64)
}

func NewSurfaceLifecycle(registry *workspacesurface.Registry, services *workspacesurface.ServiceManager) *SurfaceLifecycle {
	if registry == nil {
		registry = workspacesurface.NewRegistry()
	}
	if services == nil {
		services = workspacesurface.NewServiceManager(nil)
	}
	return &SurfaceLifecycle{registry: registry, services: services, scopes: HostSymbolicScopeResolver{}}
}

func (l *SurfaceLifecycle) SetCapabilityRegistry(registry *workspacecapability.Registry) {
	if l != nil {
		l.capabilities = registry
	}
}

func (l *SurfaceLifecycle) SetRuntimeRegistry(registry *runtimecapability.Registry) {
	if l != nil {
		l.runtimes = registry
	}
}

func (l *SurfaceLifecycle) SetRuntimeContextResolver(resolver RuntimeContextResolver) {
	if l != nil {
		l.contexts = resolver
	}
}

func (l *SurfaceLifecycle) SetSymbolicScopeResolver(resolver SymbolicScopeResolver) {
	if l != nil && resolver != nil {
		l.scopes = resolver
	}
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
	capabilitiesRegistered := false
	if installed.Enabled && l.capabilities != nil {
		owner := workspace.CapabilityOwner{
			Kind: workspace.CapabilityOwnerPlugin, PluginID: installed.Name, PluginVersion: installed.Version,
		}
		if err := l.capabilities.RegisterPluginDefinitions(owner, contributedCapabilityDefinitions(installed)); err != nil {
			return err
		}
		capabilitiesRegistered = true
	}
	providers, err := l.runtimeProviders(installed)
	if err != nil {
		if capabilitiesRegistered {
			l.capabilities.UnregisterPluginDefinitions(installed.Name)
		}
		return err
	}
	providersRegistered := false
	if len(providers) > 0 && l.runtimes != nil {
		if l.contexts == nil {
			if capabilitiesRegistered {
				l.capabilities.UnregisterPluginDefinitions(installed.Name)
			}
			return fmt.Errorf("plugin runtime provider context resolver is unavailable")
		}
		for _, provider := range providers {
			if err := l.runtimes.Register(provider); err != nil {
				l.runtimes.UnregisterPlugin(installed.Name)
				if capabilitiesRegistered {
					l.capabilities.UnregisterPluginDefinitions(installed.Name)
				}
				return err
			}
		}
		providersRegistered = true
	}
	if err := l.registry.RegisterTrusted(registration); err != nil {
		if providersRegistered {
			l.runtimes.UnregisterPlugin(installed.Name)
		}
		if capabilitiesRegistered {
			l.capabilities.UnregisterPluginDefinitions(installed.Name)
		}
		return err
	}
	return nil
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
	if err := l.registry.UnregisterOwner(workspacesurface.OwnerPlugin, pluginID, generation); err != nil {
		return err
	}
	if l.capabilities != nil {
		l.capabilities.UnregisterPluginDefinitions(pluginID)
	}
	if l.runtimes != nil {
		l.runtimes.UnregisterPlugin(pluginID)
	}
	return nil
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

func contributedCapabilityDefinitions(installed InstalledPlugin) []workspacecapability.Definition {
	if installed.WorkspaceSurfaces == nil {
		return nil
	}
	definitions := make([]workspacecapability.Definition, 0, len(installed.WorkspaceSurfaces.Capabilities))
	for _, capability := range installed.WorkspaceSurfaces.Capabilities {
		owner := workspace.CapabilityOwner{
			Kind: workspace.CapabilityOwnerPlugin, PluginID: installed.Name, PluginVersion: installed.Version,
		}
		definition := workspacecapability.Definition{
			ID: capability.ID, Version: capability.Version, Owner: &owner,
			Display: workspacecapability.Display{
				Name: capability.Display.Name, Summary: capability.Display.Description,
			},
			Requirements: workspacecapability.Requirements{MaxInstallsPerWorkspace: 1},
			Station:      workspacecapability.StationDescriptor{Title: capability.Display.Name},
		}
		if capability.RuntimeProvider != nil {
			definition.Setup = workspacecapability.SetupDescriptor{
				AdapterID:               "plugin:" + workspace.NormalizeCapabilityID(installed.Name) + ":" + capability.RuntimeProvider.ID,
				DirectoryRequirementKey: capability.RuntimeProvider.RequirementKey,
			}
		}
		definitions = append(definitions, definition)
	}
	return definitions
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
			Display:           workspacesurface.Display{Name: capability.Display.Name, Description: capability.Display.Description},
			AgentOperationIDs: append([]string(nil), capability.AgentOperations...),
		}
		if capability.RuntimeProvider != nil {
			inert.RuntimeRequirementKey = capability.RuntimeProvider.RequirementKey
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
				SetupProviderID: func() string {
					if surface.HostIntents.OpenSetup.ProviderID == "" {
						return ""
					}
					return "plugin:" + owner.ID + ":" + surface.HostIntents.OpenSetup.ProviderID
				}(),
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
			operations := make(map[string]workspacesurface.Operation, len(surface.Operations)+len(capability.AgentOperations))
			serviceOperations := make(map[string]ContributedOperation, len(service.Operations))
			for _, operation := range service.Operations {
				serviceOperations[operation.ID] = operation
			}
			declaredOperations := append(append([]string(nil), surface.Operations...), capability.AgentOperations...)
			for _, operationID := range declaredOperations {
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
