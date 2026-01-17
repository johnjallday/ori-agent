package types

import "time"

// DynamicAgentStatus tracks approval status for a dynamic agent request.
type DynamicAgentStatus string

const (
	DynamicAgentStatusPending  DynamicAgentStatus = "pending"
	DynamicAgentStatusApproved DynamicAgentStatus = "approved"
	DynamicAgentStatusDenied   DynamicAgentStatus = "denied"
)

// DynamicAgentRequest represents a user-approved dynamic agent creation.
type DynamicAgentRequest struct {
	ID           string             `json:"id"`
	WorkspaceID  string             `json:"workspace_id,omitempty"`
	PlanID       string             `json:"plan_id,omitempty"`
	Name         string             `json:"name"`
	Role         AgentRole          `json:"role,omitempty"`
	Capabilities []string           `json:"capabilities,omitempty"`
	Description  string             `json:"description,omitempty"`
	Rationale    string             `json:"rationale,omitempty"`
	Status       DynamicAgentStatus `json:"status"`
	CreatedAt    time.Time          `json:"created_at"`
	ApprovedAt   *time.Time         `json:"approved_at,omitempty"`
	DeniedAt     *time.Time         `json:"denied_at,omitempty"`
	ApprovedBy   string             `json:"approved_by,omitempty"`
}
