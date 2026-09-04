package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/johnjallday/ori-agent/internal/runtimecapability"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

type RuntimeContextResolver func(context.Context, string, workspacesurface.Owner) (workspacesurface.WorkspaceContext, error)

type pluginRuntimeProvider struct {
	id         string
	owner      workspacesurface.Owner
	manager    *workspacesurface.ServiceManager
	spec       workspacesurface.ServiceSpec
	provider   ContributedRuntimeProvider
	operations map[string]workspacesurface.Operation
	contexts   RuntimeContextResolver
	scopes     SymbolicScopeResolver
}

func (p *pluginRuntimeProvider) ID() string { return p.id }

func (p *pluginRuntimeProvider) EvaluateDurable(ctx context.Context, request runtimecapability.EvaluationRequest) (runtimecapability.DurableResult, error) {
	prerequisites, err := p.readyResult(ctx, request.WorkspaceID, p.provider.Operations.Prerequisites)
	if err != nil {
		return runtimecapability.DurableResult{}, err
	}
	if !prerequisites.Ready {
		return runtimecapability.DurableResult{
			State: runtimecapability.DurableNeedsAttention, ReasonCode: "provider_prerequisites_missing",
			Summary: prerequisites.Summary, Action: repairAction(prerequisites.RepairReview),
		}, nil
	}
	readiness, err := p.readyResult(ctx, request.WorkspaceID, p.provider.Operations.Readiness)
	if err != nil {
		return runtimecapability.DurableResult{}, err
	}
	if !readiness.Ready {
		return runtimecapability.DurableResult{
			State: runtimecapability.DurableInProgress, ReasonCode: "provider_setup_required",
			Summary: readiness.Summary, Action: repairAction(readiness.RepairReview), VerificationRequired: true,
		}, nil
	}
	return runtimecapability.DurableResult{
		State: runtimecapability.DurableConfigured, Summary: readiness.Summary, VerificationRequired: true,
	}, nil
}

func (p *pluginRuntimeProvider) CheckLive(ctx context.Context, request runtimecapability.EvaluationRequest) (runtimecapability.LiveResult, error) {
	var output struct {
		Available bool   `json:"available"`
		Summary   string `json:"summary"`
	}
	if err := p.call(ctx, request.WorkspaceID, p.provider.Operations.LiveStatus, &output); err != nil {
		return runtimecapability.LiveResult{}, err
	}
	if output.Available {
		return runtimecapability.LiveResult{State: runtimecapability.LiveAvailable, Summary: output.Summary}, nil
	}
	return runtimecapability.LiveResult{
		State: runtimecapability.LiveOffline, ReasonCode: "provider_offline", Summary: output.Summary, Action: repairAction(nil),
	}, nil
}

func (p *pluginRuntimeProvider) Verify(ctx context.Context, request runtimecapability.VerificationRequest) (runtimecapability.VerificationResult, error) {
	var output struct {
		Verified   bool   `json:"verified"`
		Summary    string `json:"summary"`
		ReasonCode string `json:"reason_code,omitempty"`
	}
	if err := p.call(ctx, request.WorkspaceID, p.provider.Operations.Verify, &output); err != nil {
		return runtimecapability.VerificationResult{}, err
	}
	state := runtimecapability.LiveOffline
	if output.Verified {
		state = runtimecapability.LiveAvailable
	} else if output.ReasonCode == "wrong_project" {
		state = runtimecapability.LiveWrongTarget
	} else if output.ReasonCode != "offline" {
		state = runtimecapability.LiveUnavailable
	}
	return runtimecapability.VerificationResult{
		Succeeded: output.Verified, LiveState: state, Summary: output.Summary,
		ReasonCode: func() string {
			if output.Verified {
				return ""
			}
			if normalized := workspace.NormalizeRuntimeIdentifier(output.ReasonCode); normalized != "" {
				return normalized
			}
			return "provider_verification_failed"
		}(),
		Action: func() *runtimecapability.Action {
			if output.Verified {
				return nil
			}
			return repairAction(nil)
		}(),
	}, nil
}

func (p *pluginRuntimeProvider) ConfirmAction(ctx context.Context, request runtimecapability.ConfirmedActionRequest) error {
	if request.ActionToken != "repair" {
		return runtimecapability.ErrUnknownAction
	}
	var output struct {
		Repaired bool   `json:"repaired"`
		Summary  string `json:"summary"`
	}
	if err := p.call(ctx, request.WorkspaceID, p.provider.Operations.Repair, &output); err != nil {
		return err
	}
	if !output.Repaired {
		return errors.New("provider repair did not complete")
	}
	return nil
}

func (p *pluginRuntimeProvider) ResolveExecutionScope(ctx context.Context, request runtimecapability.ExecutionScopeRequest) (runtimecapability.CapabilityExecutionScope, error) {
	if p.contexts == nil || p.scopes == nil {
		return runtimecapability.CapabilityExecutionScope{}, runtimecapability.ErrExecutionScopeUnavailable
	}
	workspaceContext, err := p.contexts(ctx, request.WorkspaceID, p.owner)
	if err != nil || workspaceContext.WorkspaceID != request.WorkspaceID {
		return runtimecapability.CapabilityExecutionScope{}, runtimecapability.ErrExecutionScopeUnavailable
	}
	scope, err := p.scopes.Resolve(ctx, workspaceContext, p.provider.Scopes)
	if err != nil {
		return runtimecapability.CapabilityExecutionScope{}, runtimecapability.ErrExecutionScopeUnavailable
	}
	return scope, nil
}

func (p *pluginRuntimeProvider) ValidateGrant(ctx context.Context, request runtimecapability.GrantValidationRequest) error {
	result, err := p.EvaluateDurable(ctx, runtimecapability.EvaluationRequest{
		WorkspaceID: request.WorkspaceID, Mode: request.Mode, Requirement: request.Requirement,
	})
	if err != nil {
		return err
	}
	if result.State != runtimecapability.DurableConfigured {
		return runtimecapability.ErrGrantNotAllowed
	}
	return nil
}

type providerRepairReview struct {
	Destination        string `json:"destination"`
	ManualRegistration string `json:"manual_registration"`
}

type providerReadyResult struct {
	Ready        bool                  `json:"ready"`
	Summary      string                `json:"summary"`
	RepairReview *providerRepairReview `json:"repair_review,omitempty"`
}

func (p *pluginRuntimeProvider) readyResult(ctx context.Context, workspaceID, operationID string) (providerReadyResult, error) {
	var output providerReadyResult
	err := p.call(ctx, workspaceID, operationID, &output)
	return output, err
}

func (p *pluginRuntimeProvider) call(ctx context.Context, workspaceID, operationID string, target any) error {
	operation, declared := p.operations[operationID]
	if !declared || p.manager == nil || p.contexts == nil {
		return workspacesurface.ErrServiceUnavailable
	}
	workspaceContext, err := p.contexts(ctx, workspaceID, p.owner)
	if err != nil || workspaceContext.WorkspaceID != workspaceID {
		return workspacesurface.ErrServiceUnavailable
	}
	arguments := map[string]any{
		"protocol_version": workspacesurface.ProtocolVersion,
		"operation_id":     operationID,
		"context": map[string]any{
			"workspace_id": workspaceContext.WorkspaceID, "workspace_root": workspaceContext.WorkspaceRoot,
			"project_entry": workspaceContext.ProjectEntry, "plugin_data_root": workspaceContext.PluginDataRoot,
			"scopes": workspaceContext.Scopes,
		},
		"input": map[string]any{},
	}
	output, err := p.manager.Call(ctx, p.spec, workspacesurface.ServiceCall{
		Operation: operationID, Arguments: arguments, Timeout: providerTimeout(operation.Timeout),
	})
	if err != nil {
		return err
	}
	if err := workspacesurface.ValidateOperationOutput(operation, output); err != nil {
		return workspacesurface.ErrServiceUnavailable
	}
	if err := json.Unmarshal(output, target); err != nil {
		return workspacesurface.ErrServiceUnavailable
	}
	return nil
}

func providerTimeout(class workspacesurface.TimeoutClass) time.Duration {
	switch class {
	case workspacesurface.TimeoutLong:
		return 60 * time.Second
	case workspacesurface.TimeoutNormal:
		return 15 * time.Second
	default:
		return 3 * time.Second
	}
}

func repairAction(review *providerRepairReview) *runtimecapability.Action {
	if review == nil {
		return nil
	}
	return &runtimecapability.Action{
		Token: "repair", Code: "repair_provider", Label: "Stage trusted runner",
		Disclosure: []runtimecapability.ActionDisclosure{
			{Label: "Trusted destination", Value: review.Destination},
			{Label: "Manual registration", Value: review.ManualRegistration},
		},
	}
}

func (l *SurfaceLifecycle) runtimeProviders(installed InstalledPlugin) ([]*pluginRuntimeProvider, error) {
	if installed.WorkspaceSurfaces == nil || !installed.Enabled {
		return nil, nil
	}
	owner := workspacesurface.Owner{
		Kind: workspacesurface.OwnerPlugin, ID: installed.Name, Version: installed.Version,
		Generation: installed.Generation, ProtocolMin: installed.WorkspaceSurfaces.Protocol.Min,
		ProtocolMax: installed.WorkspaceSurfaces.Protocol.Max,
	}
	serviceByID := make(map[string]ContributedService, len(installed.WorkspaceSurfaces.Services))
	artifactByService := make(map[string]ResolvedArtifact, len(installed.ResolvedArtifacts))
	for _, service := range installed.WorkspaceSurfaces.Services {
		serviceByID[service.ID] = service
	}
	for _, artifact := range installed.ResolvedArtifacts {
		artifactByService[artifact.ServiceID] = artifact
	}
	var providers []*pluginRuntimeProvider
	for _, capability := range installed.WorkspaceSurfaces.Capabilities {
		if capability.RuntimeProvider == nil {
			continue
		}
		service, ok := serviceByID[capability.ServiceID]
		artifact := artifactByService[capability.ServiceID]
		if !ok {
			return nil, fmt.Errorf("runtime provider %q service is unavailable", capability.RuntimeProvider.ID)
		}
		if !artifact.Available {
			continue
		}
		operations := make(map[string]workspacesurface.Operation, len(service.Operations))
		for _, operation := range service.Operations {
			operations[operation.ID] = runtimeOperation(operation)
		}
		provider := *capability.RuntimeProvider
		providers = append(providers, &pluginRuntimeProvider{
			id: "plugin:" + installed.Name + ":" + provider.ID, owner: owner,
			manager: l.services, contexts: l.contexts, scopes: l.scopes, provider: provider, operations: operations,
			spec: workspacesurface.ServiceSpec{
				PluginID: installed.Name, PluginGeneration: installed.Generation, ServiceID: service.ID,
				Command: artifact.ManagedPath, Args: append([]string(nil), service.Entrypoint.Args...),
				MaxConcurrency: 8, StartupTimeout: 10 * time.Second, ShutdownTimeout: 5 * time.Second,
			},
		})
	}
	return providers, nil
}
