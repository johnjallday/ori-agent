package samplelibrary

import (
	"context"
	"errors"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// CapabilityRuntime binds removal and derived status to the inert compiled
// definition. It starts no watcher, schedule, scan, or agent.
type CapabilityRuntime struct{ service *Service }

func NewCapabilityRuntime(service *Service) *CapabilityRuntime {
	return &CapabilityRuntime{service: service}
}
func (r *CapabilityRuntime) CapabilityStatus(homeID string) (workspacecapability.Status, error) {
	state, roots, err := r.service.Snapshot(context.Background(), homeID)
	if errors.Is(err, ErrNotFound) {
		return workspacecapability.Status{State: workspacecapability.StatusSetupNeeded, Detail: "Connect a sample folder when you are ready.", Configured: false}, nil
	}
	if err != nil {
		return workspacecapability.Status{State: workspacecapability.StatusNeedsAttention, Detail: "Sample Library needs attention.", Configured: false}, nil
	}
	active := 0
	for _, root := range roots {
		if root.State == "active" {
			active++
		}
	}
	if state.Lifecycle != "active" {
		return workspacecapability.Status{State: workspacecapability.StatusSetupNeeded, Detail: "Sample Library is not configured.", Configured: false}, nil
	}
	if active == 0 {
		return workspacecapability.Status{State: workspacecapability.StatusSetupNeeded, Detail: "Connect a sample folder when you are ready.", Configured: false}, nil
	}
	return workspacecapability.Status{State: workspacecapability.StatusPaused, Detail: "Catalog ready. Refresh runs only when requested.", Configured: true}, nil
}
func (r *CapabilityRuntime) DescribeCapabilityRemoval(homeID string) (workspacecapability.RemovalFacts, error) {
	if r == nil || r.service == nil {
		return workspacecapability.RemovalFacts{}, ErrOperationFailed
	}
	impact, err := r.service.store.RemovalImpact(context.Background(), homeID)
	if err != nil {
		return workspacecapability.RemovalFacts{}, ErrOperationFailed
	}
	return workspacecapability.RemovalFacts{Impacts: []string{fmt.Sprintf("%d connected sample folders lose access", impact.Roots), fmt.Sprintf("%d catalog entries are redacted", impact.Entries), fmt.Sprintf("%d collections keep %d unavailable memberships", impact.Collections, impact.CollectionMembers), fmt.Sprintf("%d derived facts and %d user annotations are deleted", impact.DerivedFacts, impact.Annotations)}, RetainedAudit: []string{"Review and operation receipts", "Collection membership tombstones", "Managed child-copy provenance"}}, nil
}

func (r *CapabilityRuntime) OnCapabilityRemove(homeID string) error {
	if r == nil || r.service == nil {
		return ErrOperationFailed
	}
	_, err := r.service.store.Get(context.Background(), homeID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return ErrOperationFailed
	}
	r.service.cancelHomeScans(homeID)
	if _, err = r.service.store.Disable(context.Background(), homeID, r.service.now()); err != nil {
		return ErrOperationFailed
	}
	if err = r.service.workspaces.Update(homeID, func(ws *workspace.Workspace) error {
		kept := ws.DirectoryReferences[:0]
		for _, ref := range ws.DirectoryReferences {
			if ref.Purpose == "sample_library" {
				ws.ForgetCapabilityResource(workspace.CapabilitySampleLibrary, workspace.ResourceDirectoryReference, ref.ID)
				continue
			}
			kept = append(kept, ref)
		}
		ws.DirectoryReferences = kept
		return nil
	}); err != nil {
		return ErrOperationFailed
	}
	return nil
}
