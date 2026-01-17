package types

import "time"

// PlannerOutput is the structured output from the planner-first step.
type PlannerOutput struct {
	ComplexityScore float64        `json:"complexity_score"`
	Rationale       string         `json:"rationale"`
	Tasks           []PlannerTask  `json:"tasks"`
	DynamicAgents   []PlannerAgent `json:"dynamic_agents,omitempty"`
}

// PlannerTask describes a single subtask in a planner output.
type PlannerTask struct {
	ID                   string    `json:"id,omitempty"`
	Description          string    `json:"description"`
	RequiredRole         AgentRole `json:"required_role,omitempty"`
	RequiredCapabilities []string  `json:"required_capabilities,omitempty"`
	DependsOn            []string  `json:"depends_on,omitempty"`
	SuggestedAgent       string    `json:"suggested_agent,omitempty"`
}

// PlannerAgent defines a proposed dynamic agent from the planner.
type PlannerAgent struct {
	Name         string    `json:"name"`
	Role         AgentRole `json:"role,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	Description  string    `json:"description,omitempty"`
	Rationale    string    `json:"rationale,omitempty"`
}

// PlannerDecision captures the routing decision for observability.
type PlannerDecision struct {
	ComplexityScore float64   `json:"complexity_score"`
	Threshold       float64   `json:"threshold"`
	Mode            string    `json:"mode"`
	MultiAgent      bool      `json:"multi_agent"`
	Rationale       string    `json:"rationale,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// PendingPlan stores a plan that is waiting on user approval.
type PendingPlan struct {
	ID        string          `json:"id"`
	Request   string          `json:"request"`
	Plan      PlannerOutput   `json:"plan"`
	Decision  PlannerDecision `json:"decision"`
	CreatedAt time.Time       `json:"created_at"`
}
