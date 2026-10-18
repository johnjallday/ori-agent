package backuprecovery

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// CapabilityRuntime reads stored facts only. It intentionally has no install
// hook: installation cannot create a key, permission, connection or check.
type CapabilityRuntime struct{ service *Service }

func NewCapabilityRuntime(service *Service) *CapabilityRuntime { return &CapabilityRuntime{service} }
func (r *CapabilityRuntime) CapabilityStatus(workspaceID string) (workspacecapability.Status, error) {
	unavailable := workspacecapability.Status{State: workspacecapability.StatusUnavailable, Detail: "Backup metadata is unavailable."}
	if r == nil || r.service == nil {
		return unavailable, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, err := r.service.GetStatus(ctx, "local", workspaceID)
	if err != nil {
		return unavailable, nil
	}
	return backupCapabilityStatus(status), nil
}

func backupCapabilityStatus(status Status) workspacecapability.Status {
	if status.RemovalPending {
		return workspacecapability.Status{State: workspacecapability.StatusNeedsAttention, Detail: "Finish removing this capability. Metadata access is fenced."}
	}
	if !status.RuntimeAvailable {
		return workspacecapability.Status{State: workspacecapability.StatusUnavailable, Detail: "Backup metadata is unavailable."}
	}
	if status.Connection != "configured" {
		return workspacecapability.Status{State: workspacecapability.StatusSetupNeeded, Detail: "Review external setup and local metadata access."}
	}
	if len(status.Health.Problems) > 0 || status.Monitoring == "review_required" {
		return workspacecapability.Status{State: workspacecapability.StatusNeedsAttention, Configured: true, Detail: "Stored metadata evidence needs review. Backup success and recovery are not proven."}
	}
	if status.Monitoring == "active" {
		return workspacecapability.Status{State: workspacecapability.StatusMonitoring, Configured: true, Detail: "Periodic metadata checks enabled while Ori is running; not proof of protection."}
	}
	if status.Monitoring == "paused" {
		return workspacecapability.Status{State: workspacecapability.StatusPaused, Configured: true, Detail: "Ori periodic checks paused. External backup schedules are unchanged."}
	}
	if status.Monitoring == "unavailable" {
		return workspacecapability.Status{State: workspacecapability.StatusUnavailable, Configured: true, Detail: "Periodic metadata checks are unavailable."}
	}
	return workspacecapability.Status{State: workspacecapability.StatusReady, Configured: true, Detail: "Metadata connection observed; backup success and recovery are not proven."}
}
func (r *CapabilityRuntime) OnCapabilityRemove(workspaceID string) error {
	if r == nil || r.service == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.service.removeCapability(ctx, workspaceID, nil)
}

// PrepareCapabilityRemoval is called only by the guarded local HTTP user
// control. The generic context-free remover cannot initiate authority changes.
func (r *CapabilityRuntime) PrepareCapabilityRemoval(ctx context.Context, owner, workspaceID string, request ControlRequest) error {
	if r == nil || r.service == nil {
		return ErrUnavailable
	}
	if owner != "local" {
		return ErrWorkspaceUnavailable
	}
	return r.service.removeCapability(ctx, workspaceID, &request)
}
func (r *CapabilityRuntime) ValidateCapabilityRemoval(workspaceID string) error {
	if r == nil || r.service == nil {
		return ErrUnavailable
	}
	release, err := r.service.enter()
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	record, err := r.service.store.load(ctx, "local", workspaceID)
	if err != nil {
		return err
	}
	if !pendingRemoval(record) {
		return ErrConfigurationChanged
	}
	return nil
}

func (r *CapabilityRuntime) DescribeCapabilityRemoval(workspaceID string) (workspacecapability.RemovalFacts, error) {
	if r == nil || r.service == nil {
		return workspacecapability.RemovalFacts{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := r.service.GetStatus(ctx, "local", workspaceID); err != nil {
		return workspacecapability.RemovalFacts{}, err
	}
	return workspacecapability.RemovalFacts{RetainedAudit: []string{"Bounded, generation-labelled local metadata history and user reports. External backups, source files and backup schedules are unchanged."}}, nil
}
func removalDigest(authority localAuthority) string {
	return localDigest([]any{"backup-capability-removal-v1", authority})
}
func pendingRemoval(record localRecord) bool {
	if record.state == nil {
		return false
	}
	for _, operation := range record.state.Operations {
		if operation.Action == "uninstall" && operation.Status == "pending" && operation.RemovalAuthority == removalDigest(record.authority) {
			return true
		}
	}
	return false
}
func metadataAdmission(record localRecord) error {
	if pendingRemoval(record) {
		return ErrRemovalPending
	}
	return nil
}

// The pending marker is the durable admission barrier across the generic
// lifecycle's later install-record write. Without it, another connect could
// start between this hook's drain and that write. Retry drains without probing;
// a later installation has a different live authority epoch, not inherited
// permission. Expiring an idempotency outcome must never expire this barrier.
func (s *Service) removeCapability(ctx context.Context, workspaceID string, request *ControlRequest) error {
	release, err := s.enter()
	if err != nil {
		return err
	}
	defer release()
	slot := s.slot(workspaceID)
	unlock, err := lockSlot(ctx, slot)
	if err != nil {
		return err
	}
	defer unlock()
	record, err := s.store.load(ctx, "local", workspaceID)
	if err != nil {
		return err
	}
	digest := ""
	if request != nil {
		if !validOperationID(request.OperationID) {
			return ErrInvalidRequest
		}
		digest = localDigest([]any{"local", workspaceID, "uninstall", *request})
		if prior, found := findOperation(record, request.OperationID); found {
			if prior.Action != "uninstall" || prior.RequestDigest != digest || prior.RemovalAuthority != removalDigest(record.authority) || !pendingRemoval(record) {
				return ErrConfigurationChanged
			}
		} else {
			if pendingRemoval(record) {
				return ErrRemovalPending
			}
			if !expectedRequest(record, request.Generation, request.Revision) {
				return ErrConfigurationChanged
			}
		}
	} else if !pendingRemoval(record) {
		return ErrLocalReviewRequired
	}
	if !pendingRemoval(record) {
		now := s.clock().UTC()
		_, err = s.store.update(ctx, "local", workspaceID, record.expected(), changeUser, func(current *localRecord) error {
			current.state.Generation = uuid.NewString()
			current.state.Configuration = nil
			current.state.Monitoring = localMonitoring{Intent: "manual_only", Revision: current.state.Monitoring.Revision + 1, Policy: current.state.Monitoring.Policy}
			operation := pendingOperation(current.state, request.OperationID, "uninstall", digest, now)
			operation.RemovalAuthority = removalDigest(current.authority)
			appendOperation(current.state, operation, now)
			return nil
		})
		if err != nil {
			return err
		}
	}
	s.clearReviews(workspaceID)
	s.mu.Lock()
	active := slot.active
	if active != nil {
		active.cancel()
	}
	s.mu.Unlock()
	if active != nil {
		return s.waitDrain(ctx, active.done)
	}
	return nil
}
