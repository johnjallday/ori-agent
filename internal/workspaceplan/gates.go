package workspaceplan

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Execution gates: the stops that automatic dispatch must respect (FR-105,
// FR-117, FR-118).
//
// A gate is evaluated immediately before a Task would be dispatched, never once
// at start. The world changes while a Plan runs — an agent is removed, a
// capability goes away — and a gate decided minutes ago would authorize work
// against conditions that no longer hold.

// GateKind names why dispatch stopped. The set matches the enforced fields the
// planning policy can require (FR-105, FR-126).
type GateKind string

const (
	// GateApproval is a stop that requires a person to authorize this specific
	// work beyond the Plan-level approval.
	GateApproval GateKind = "approval"
	// GateBranch is a repository-state precondition, such as running only on a
	// safe branch.
	GateBranch GateKind = "branch"
	// GateCapability is a required agent or capability that is not available.
	GateCapability GateKind = "capability"
	// GateHandoff is a required specialist handoff (FR-117).
	GateHandoff GateKind = "handoff"
	// GateDestructive is a destructive action awaiting explicit confirmation.
	GateDestructive GateKind = "destructive_action"
	// GateValidation is a required validation checkpoint covering this work
	// (FR-119).
	GateValidation GateKind = "validation"
	// GatePrecondition is an enforced precondition with no more specific kind,
	// such as a repository scan.
	GatePrecondition GateKind = "precondition"
)

// GateClass distinguishes a stop a person can pass by acting deliberately from
// one that no amount of clicking can pass.
//
// This distinction is the whole point of gating. An unavailable agent is not
// waiting for permission — starting its Task by hand would fail exactly as
// automatic dispatch would, so the resolution is to change the world. A
// required validation checkpoint is the opposite: it is asking for a human to
// look, and a human deliberately starting that item IS the look it asked for.
type GateClass string

const (
	// GateAutomation stops automatic dispatch only. A deliberate user start of
	// that specific Task passes it, because the deliberate act is the
	// authorization the gate was asking for.
	GateAutomation GateClass = "automation"
	// GateBlocking stops every dispatch, automatic or manual.
	GateBlocking GateClass = "blocking"
)

// Gate is one stop standing between a Task and its dispatch.
type Gate struct {
	Kind  GateKind  `json:"kind"`
	Class GateClass `json:"class"`
	// ItemID and TaskID identify the work held. ItemID is empty for a
	// Plan-wide gate such as an execution precondition.
	ItemID string `json:"item_id,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	Title  string `json:"title,omitempty"`
	// Reason states what is stopping the work, in the words a user reads.
	Reason string `json:"reason"`
	// Resolution states what would clear it. Every gate carries one: a stop
	// with no stated way forward is a dead end, and FR-118 requires the path
	// to be shown rather than inferred.
	Resolution string `json:"resolution"`
}

// PreconditionChecker resolves the compiled enforcement adapters a Plan's
// execution policy names (FR-47, FR-126).
//
// It lives outside this package because the adapters are application concerns —
// repository state, handoff policy, destructive-action confirmation — and the
// planning package must not grow its own opinion about any of them.
type PreconditionChecker interface {
	// CheckPrecondition reports whether the named precondition is satisfied
	// right now. A returned Gate explains a stop; nil means satisfied.
	CheckPrecondition(ctx context.Context, workspaceID, planID, name string) (*Gate, error)
}

// preconditionKinds maps the enforcement adapter keys the policy can require
// onto the gate kind a user would recognize (FR-126). An unknown key is still
// enforced — it is simply reported as a generic precondition rather than
// guessing at a category it might not belong to.
var preconditionKinds = map[string]GateKind{
	"repo_scan":                GatePrecondition,
	"safe_branch":              GateBranch,
	"plan_approval":            GateApproval,
	"handoff_confirmation":     GateHandoff,
	"destructive_confirmation": GateDestructive,
	"artifact_write":           GateApproval,
	"note_creation":            GateApproval,
}

// gateKindFor classifies an enforcement adapter key.
func gateKindFor(name string) GateKind {
	if kind, known := preconditionKinds[strings.ToLower(strings.TrimSpace(name))]; known {
		return kind
	}
	return GatePrecondition
}

// GateInput is everything gate evaluation needs about one candidate dispatch.
type GateInput struct {
	WorkspaceID string
	Plan        *Plan
	// Content is the approved version's content that produced this Task. Gates
	// are read from the version the work was approved under, never from the
	// working draft — a draft edit must not change what an already-approved
	// Task is allowed to do (FR-103).
	Content PlanContent
	// ItemID is the Plan-local item the Task came from, empty when the Task
	// has no surviving link.
	ItemID string
	TaskID string
	// Availability is what exists right now. A nil slice inside it means that
	// dimension was not checked, which is deliberately different from an empty
	// one meaning nothing is available (FR-46, FR-48).
	Availability ValidationContext
}

// EvaluateGates returns every gate standing in front of one Task, in a stable
// order.
//
// It returns all of them rather than the first, because a user who clears one
// stop only to meet another has been told the truth in the least useful
// possible order.
func EvaluateGates(ctx context.Context, checker PreconditionChecker, input GateInput) ([]Gate, error) {
	var gates []Gate

	item, group, found := findItem(input.Content, input.ItemID)
	if found {
		gates = append(gates, capabilityGates(item, input)...)
		gates = append(gates, validationGates(input.Content, item, group, input)...)
	}

	// Execution preconditions gate the whole Plan, so they are evaluated for
	// every dispatch rather than once at start.
	for _, name := range input.Content.Execution.Preconditions {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		gate, err := checkPrecondition(ctx, checker, input, name)
		if err != nil {
			return nil, err
		}
		if gate != nil {
			gates = append(gates, *gate)
		}
	}

	return gates, nil
}

// checkPrecondition asks the configured checker about one enforced
// precondition.
//
// With no checker configured the precondition fails closed. The Plan was
// approved on the promise that this condition would be enforced; running the
// work because nothing can confirm it would break exactly the promise the
// approval was given on.
func checkPrecondition(ctx context.Context, checker PreconditionChecker, input GateInput, name string) (*Gate, error) {
	if checker == nil {
		return &Gate{
			Kind:   gateKindFor(name),
			Class:  GateAutomation,
			TaskID: input.TaskID,
			ItemID: input.ItemID,
			Title:  name,
			Reason: fmt.Sprintf(
				"this plan requires the %q precondition, and nothing is configured to check it", name),
			Resolution: "start this step yourself once you have confirmed the precondition holds",
		}, nil
	}

	gate, err := checker.CheckPrecondition(ctx, input.WorkspaceID, input.Plan.ID, name)
	if err != nil {
		return nil, fmt.Errorf("check precondition %q: %w", name, err)
	}
	if gate == nil {
		return nil, nil
	}
	// The checker owns the reason; the dispatch owns which Task it stopped.
	gate.TaskID = input.TaskID
	gate.ItemID = input.ItemID
	if gate.Kind == "" {
		gate.Kind = gateKindFor(name)
	}
	if gate.Class == "" {
		gate.Class = GateAutomation
	}
	if gate.Title == "" {
		gate.Title = name
	}
	return gate, nil
}

// capabilityGates reports required agents and capabilities that are not
// available right now (FR-118).
//
// These are blocking: an absent agent does not appear because the user clicked
// start, so offering a manual override would only move the same failure later.
func capabilityGates(item TaskItem, input GateInput) []Gate {
	var gates []Gate

	if agents := input.Availability.AvailableAgents; agents != nil {
		assignee := strings.TrimSpace(item.Assignee)
		switch {
		case assignee == "":
			gates = append(gates, Gate{
				Kind:       GateCapability,
				Class:      GateBlocking,
				ItemID:     item.ID,
				TaskID:     input.TaskID,
				Title:      item.Description,
				Reason:     "this step has no assignee",
				Resolution: "assign an agent to this step, then start it",
			})
		case !containsFold(agents, assignee):
			gates = append(gates, Gate{
				Kind:   GateCapability,
				Class:  GateBlocking,
				ItemID: item.ID,
				TaskID: input.TaskID,
				Title:  item.Description,
				Reason: fmt.Sprintf("%q is not in this workspace", assignee),
				Resolution: fmt.Sprintf(
					"add %q to the workspace or reassign this step to an agent that is here", assignee),
			})
		}
	}

	if available := input.Availability.AvailableCapabilities; available != nil {
		var missing []string
		for _, capability := range item.RequiredCapabilities {
			capability = strings.TrimSpace(capability)
			if capability != "" && !containsFold(available, capability) {
				missing = append(missing, capability)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			gates = append(gates, Gate{
				Kind:   GateCapability,
				Class:  GateBlocking,
				ItemID: item.ID,
				TaskID: input.TaskID,
				Title:  item.Description,
				Reason: fmt.Sprintf("this step needs %s, which this workspace does not have",
					strings.Join(missing, ", ")),
				Resolution: "install or enable the missing capability, or revise the plan to work without it",
			})
		}
	}

	return gates
}

// validationGates reports required checkpoints covering this item (FR-119).
//
// A checkpoint with no AppliesTo covers the whole Plan, so it gates every item
// in it. Scoping "applies to everything" down to "applies to nothing" would
// make the most sweeping checkpoint the weakest one.
func validationGates(content PlanContent, item TaskItem, group TaskGroup, input GateInput) []Gate {
	var gates []Gate
	for _, checkpoint := range content.Validations {
		if !checkpoint.Required {
			continue
		}
		if !checkpointCovers(checkpoint, item.ID, group.ID) {
			continue
		}
		reason := checkpoint.Title
		if reason == "" {
			reason = "a required validation checkpoint covers this step"
		}
		gates = append(gates, Gate{
			Kind:       GateValidation,
			Class:      GateAutomation,
			ItemID:     item.ID,
			TaskID:     input.TaskID,
			Title:      checkpoint.Title,
			Reason:     reason,
			Resolution: "review the checkpoint, then start this step yourself to confirm it passed",
		})
	}
	return gates
}

// checkpointCovers reports whether a checkpoint gates the given item or its
// group.
func checkpointCovers(checkpoint ValidationCheckpoint, itemID, groupID string) bool {
	if len(checkpoint.AppliesTo) == 0 {
		return true
	}
	for _, target := range checkpoint.AppliesTo {
		target = strings.TrimSpace(target)
		if target != "" && (target == itemID || target == groupID) {
			return true
		}
	}
	return false
}

// findItem locates a Plan-local item and the group holding it.
func findItem(content PlanContent, itemID string) (TaskItem, TaskGroup, bool) {
	if strings.TrimSpace(itemID) == "" {
		return TaskItem{}, TaskGroup{}, false
	}
	for _, group := range content.Groups {
		for _, item := range group.Items {
			if item.ID == itemID {
				return item, group, true
			}
		}
	}
	return TaskItem{}, TaskGroup{}, false
}

// containsFold reports case-insensitive membership. Agent and capability names
// are compared the way a user would read them, not byte for byte.
func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

// blockingGates returns only the stops a deliberate user start cannot pass.
func blockingGates(gates []Gate) []Gate {
	var blocking []Gate
	for _, gate := range gates {
		if gate.Class == GateBlocking {
			blocking = append(blocking, gate)
		}
	}
	return blocking
}

// GateSummary renders gates for a reason line, keeping the blocking ones first
// because those are the stops the user cannot simply step past.
func GateSummary(gates []Gate) string {
	if len(gates) == 0 {
		return ""
	}
	ordered := make([]Gate, len(gates))
	copy(ordered, gates)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Class == GateBlocking && ordered[j].Class != GateBlocking
	})
	reasons := make([]string, 0, len(ordered))
	for _, gate := range ordered {
		reasons = append(reasons, gate.Reason)
	}
	return strings.Join(reasons, "; ")
}
