package workspace

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/types"
)

// AddDynamicAgentRequest appends a dynamic agent request to the workspace.
func (w *Workspace) AddDynamicAgentRequest(req types.DynamicAgentRequest) types.DynamicAgentRequest {
	w.mu.Lock()
	defer w.mu.Unlock()

	if req.ID == "" {
		req.ID = uuid.New().String()
	}
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now()
	}
	if req.Status == "" {
		req.Status = types.DynamicAgentStatusPending
	}

	w.DynamicAgentRequests = append(w.DynamicAgentRequests, req)
	w.UpdatedAt = time.Now()

	return req
}

// GetDynamicAgentRequest finds a dynamic agent request by ID.
func (w *Workspace) GetDynamicAgentRequest(id string) (*types.DynamicAgentRequest, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	for i := range w.DynamicAgentRequests {
		if w.DynamicAgentRequests[i].ID == id {
			reqCopy := w.DynamicAgentRequests[i]
			return &reqCopy, nil
		}
	}

	return nil, fmt.Errorf("dynamic agent request %s not found", id)
}

// UpdateDynamicAgentRequestStatus updates a request status and timestamps.
func (w *Workspace) UpdateDynamicAgentRequestStatus(id string, status types.DynamicAgentStatus, approvedBy string) (*types.DynamicAgentRequest, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	for i := range w.DynamicAgentRequests {
		if w.DynamicAgentRequests[i].ID == id {
			w.DynamicAgentRequests[i].Status = status
			if status == types.DynamicAgentStatusApproved {
				w.DynamicAgentRequests[i].ApprovedAt = &now
				w.DynamicAgentRequests[i].ApprovedBy = approvedBy
			}
			if status == types.DynamicAgentStatusDenied {
				w.DynamicAgentRequests[i].DeniedAt = &now
			}
			w.UpdatedAt = now
			reqCopy := w.DynamicAgentRequests[i]
			return &reqCopy, nil
		}
	}

	return nil, fmt.Errorf("dynamic agent request %s not found", id)
}
