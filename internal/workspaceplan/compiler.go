package workspaceplan

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Compiling an approved Plan version into Workspace Tasks.
//
// Two properties make this safe to retry, and both come from the same idea:
// the Task tree is a pure function of the approved version.
//
//   - Task IDs are derived deterministically from the Plan, version, and item
//     ID. A second materialization of the same approval computes the same IDs,
//     so it recognizes the work already exists instead of creating a second
//     copy (FR-91).
//   - The compile itself has no side effects. It produces Tasks; something
//     else decides whether to write them. That is what lets the whole graph be
//     staged and validated before anything is committed (FR-89).
//
// Tasks are then handed to workspace.AddTasks, which validates the graph as a
// batch and rolls the whole batch back on failure. Reusing it rather than
// appending tasks directly is what keeps Plan-created work subject to the same
// invariants as every other Task (FR-92).

// planTaskNamespace scopes deterministic Task IDs so they cannot collide with
// IDs derived for any other purpose.
var planTaskNamespace = uuid.NewSHA1(uuid.NameSpaceOID, []byte("ori.workspaceplan.task"))

// DeterministicTaskID returns the Task ID for one materialized Plan element.
//
// It is a pure function of the approved work's identity, which is what makes a
// retried materialization idempotent: the same approval yields the same IDs, so
// the second attempt sees the Tasks already exist rather than creating another
// set (FR-91).
//
// The approval ID is deliberately NOT part of the key. Two approvals of the
// same version would be the same work, and hashing the approval in would let a
// re-approval duplicate a Task tree that already exists.
func DeterministicTaskID(planID string, version int, role LinkRole, groupID, itemID string) string {
	key := fmt.Sprintf("%s|%d|%s|%s|%s", planID, version, role, groupID, itemID)
	return uuid.NewSHA1(planTaskNamespace, []byte(key)).String()
}

// CompiledTask is one Task the compiler produced, with the Plan element it came
// from. Keeping the provenance beside the Task is what lets the materializer
// write both the Task and its link without re-deriving the mapping (FR-83).
type CompiledTask struct {
	Task workspace.Task
	Link TaskLink
}

// CompileInput is everything needed to compile one approved version.
type CompileInput struct {
	Plan    *Plan
	Version *Version
	// ApprovalID and ApprovedBy become Task provenance, so a Task can always
	// name the approval and the person that authorized it (FR-87, FR-88).
	ApprovalID string
	ApprovedBy string
	Now        time.Time
	// Carry maps a Plan-local group or item ID to the Task that already exists
	// for it, for a revision that retains prior work (FR-76).
	//
	// A carried element compiles to NOTHING: no Task and no link. The link from
	// the version that created it stays live and keeps representing that work,
	// which is the honest record — the Task came from version 1, and version 2
	// did not change it. Emitting a second link for the new version would leave
	// two live links for one item and make "which version does this Task belong
	// to" unanswerable.
	//
	// The Task IDs still matter here, because other items depend on carried
	// work and their input edges must point at the Task that actually exists.
	Carry map[string]string
}

// CompileTaskTree turns an approved version into the Task tree it describes.
//
// The shape is one parent Task per task group and one child Task per item. A
// group is a real Task rather than a label because the Plan's dependencies are
// expressed between groups as well as between items, and the Task model
// already understands parent/child and input relationships.
//
// Dependencies map onto InputTaskIDs, which is the existing Task model's way of
// saying "this work needs that work's result first" — the same edges the Task
// graph validator already checks for cycles (FR-84).
func CompileTaskTree(input CompileInput) ([]CompiledTask, error) {
	if input.Plan == nil || input.Version == nil {
		return nil, fmt.Errorf("%w: compiling requires a plan and a version", ErrValidation)
	}
	plan := input.Plan
	version := input.Version
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	content := version.Content
	compiled := make([]CompiledTask, 0, len(content.Groups)+content.ActionableItemCount())

	// Plan-local IDs map to deterministic Task IDs first, so dependencies can
	// be resolved without caring what order elements are compiled in.
	groupTaskIDs := make(map[string]string, len(content.Groups))
	itemTaskIDs := make(map[string]string, content.ActionableItemCount())
	for _, group := range content.Groups {
		groupTaskIDs[group.ID] = carriedOr(input.Carry, group.ID,
			DeterministicTaskID(plan.ID, version.Number, LinkRoleGroup, group.ID, ""))
		for _, item := range group.Items {
			itemTaskIDs[item.ID] = carriedOr(input.Carry, item.ID,
				DeterministicTaskID(plan.ID, version.Number, LinkRoleItem, group.ID, item.ID))
		}
	}

	provenance := Provenance{
		PlanID:      plan.ID,
		WorkspaceID: plan.WorkspaceID,
		Version:     version.Number,
		ApprovalID:  input.ApprovalID,
		ApprovedBy:  input.ApprovedBy,
		ApprovedAt:  now,
	}

	for groupIndex, group := range content.Groups {
		groupProvenance := provenance
		groupProvenance.GroupID = group.ID
		groupProvenance.Role = LinkRoleGroup

		groupTask := workspace.Task{
			ID:          groupTaskIDs[group.ID],
			WorkspaceID: plan.WorkspaceID,
			From:        planTaskSource,
			Description: group.Title,
			Details:     groupDetails(group, version),
			Status:      workspace.TaskStatusPending,
			Priority:    1,
			CreatedAt:   now,
			// A group task coordinates its items in order, which is what the
			// Plan's ordering means.
			OrchestrationMode: workspace.TaskOrchestrationModeSequential,
			SubtaskIndex:      groupIndex + 1,
			Context:           planTaskContext(groupProvenance),
			// Assignment provenance says plainly that this came from an
			// approved plan and who approved it (FR-87).
			AssignedBy:       assignedByFor(input.ApprovedBy),
			AssignmentReason: fmt.Sprintf("created from approved plan version %d", version.Number),
		}
		for _, dependency := range group.DependsOn {
			if taskID, ok := groupTaskIDs[dependency]; ok {
				groupTask.InputTaskIDs = append(groupTask.InputTaskIDs, taskID)
			}
		}

		if _, carried := input.Carry[group.ID]; !carried {
			compiled = append(compiled, CompiledTask{
				Task: groupTask,
				Link: TaskLink{
					PlanID: plan.ID, WorkspaceID: plan.WorkspaceID,
					Version: version.Number, ApprovalID: input.ApprovalID,
					GroupID: group.ID, TaskID: groupTask.ID,
					Role: LinkRoleGroup, CreatedAt: now,
				},
			})
		}

		for itemIndex, item := range group.Items {
			itemProvenance := provenance
			itemProvenance.GroupID = group.ID
			itemProvenance.ItemID = item.ID
			itemProvenance.Role = LinkRoleItem

			itemTask := workspace.Task{
				ID:          itemTaskIDs[item.ID],
				WorkspaceID: plan.WorkspaceID,
				From:        planTaskSource,
				// An unassigned item creates an unassigned Task. Picking an
				// agent here would be a decision nobody approved (FR-86).
				To:                   item.Assignee,
				AssignedNodeID:       item.AssigneeNodeID,
				Description:          item.Description,
				Details:              itemDetails(item, group),
				ReferenceURL:         item.ReferenceURL,
				RequiredCapabilities: append([]string(nil), item.RequiredCapabilities...),
				Status:               workspace.TaskStatusPending,
				Priority:             itemPriority(item),
				CreatedAt:            now,
				ParentTaskID:         groupTask.ID,
				SubtaskIndex:         itemIndex + 1,
				Context:              planTaskContext(itemProvenance),
				AssignedBy:           assignedByFor(input.ApprovedBy),
				AssignmentReason:     assignmentReason(item, version.Number),
			}
			for _, dependency := range item.DependsOn {
				if taskID, ok := itemTaskIDs[dependency]; ok {
					itemTask.InputTaskIDs = append(itemTask.InputTaskIDs, taskID)
				}
			}

			if _, carried := input.Carry[item.ID]; carried {
				continue
			}
			compiled = append(compiled, CompiledTask{
				Task: itemTask,
				Link: TaskLink{
					PlanID: plan.ID, WorkspaceID: plan.WorkspaceID,
					Version: version.Number, ApprovalID: input.ApprovalID,
					GroupID: group.ID, ItemID: item.ID, TaskID: itemTask.ID,
					Role: LinkRoleItem, CreatedAt: now,
				},
			})
		}
	}

	return compiled, nil
}

// carriedOr returns the existing Task ID for a retained element, or the
// deterministic ID for work that is about to be created.
func carriedOr(carry map[string]string, id, fallback string) string {
	if taskID, found := carry[id]; found && taskID != "" {
		return taskID
	}
	return fallback
}

// planTaskSource marks a Task as created by the planning workflow rather than
// by a user or an agent directly.
const planTaskSource = "workspace-plan"

// PlanProvenanceContextKey is where a Task carries its originating Plan. It is
// a single structured entry rather than scattered keys so a reader can find the
// whole provenance in one place (FR-88).
const PlanProvenanceContextKey = "workspace_plan"

// planTaskContext embeds the Plan provenance in the Task's context.
func planTaskContext(provenance Provenance) map[string]any {
	return map[string]any{
		PlanProvenanceContextKey: map[string]any{
			"plan_id":     provenance.PlanID,
			"studio_id":   provenance.WorkspaceID,
			"version":     provenance.Version,
			"approval_id": provenance.ApprovalID,
			"group_id":    provenance.GroupID,
			"item_id":     provenance.ItemID,
			"role":        string(provenance.Role),
			"approved_by": provenance.ApprovedBy,
			"approved_at": provenance.ApprovedAt.Format(time.RFC3339),
		},
	}
}

// ProvenanceFromTaskContext reads a Task's Plan provenance back out, which is
// the reverse half of the bidirectional link (FR-10).
func ProvenanceFromTaskContext(context map[string]any) (Provenance, bool) {
	raw, ok := context[PlanProvenanceContextKey].(map[string]any)
	if !ok {
		return Provenance{}, false
	}
	provenance := Provenance{
		PlanID:      stringField(raw, "plan_id"),
		WorkspaceID: stringField(raw, "studio_id"),
		ApprovalID:  stringField(raw, "approval_id"),
		GroupID:     stringField(raw, "group_id"),
		ItemID:      stringField(raw, "item_id"),
		Role:        LinkRole(stringField(raw, "role")),
		ApprovedBy:  stringField(raw, "approved_by"),
	}
	switch version := raw["version"].(type) {
	case int:
		provenance.Version = version
	case float64:
		provenance.Version = int(version)
	}
	if parsed, err := time.Parse(time.RFC3339, stringField(raw, "approved_at")); err == nil {
		provenance.ApprovedAt = parsed
	}
	return provenance, provenance.PlanID != ""
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func assignedByFor(approvedBy string) string {
	if strings.TrimSpace(approvedBy) == "" {
		return planTaskSource
	}
	return approvedBy
}

// assignmentReason states where the assignment came from. Every variant names
// the approved plan version, so a Task can always answer "who decided this?"
// with something more useful than a system name (FR-87).
func assignmentReason(item TaskItem, version int) string {
	if item.Assignee == "" {
		return fmt.Sprintf("left unassigned in approved plan version %d", version)
	}
	return fmt.Sprintf("assigned in approved plan version %d", version)
}

func itemPriority(item TaskItem) int {
	if item.Priority > 0 {
		return item.Priority
	}
	return 1
}

// groupDetails and itemDetails carry the Plan's own words onto the Task, so
// someone reading the Task can see what it was meant to achieve without
// opening the Plan.
func groupDetails(group TaskGroup, version *Version) string {
	var b strings.Builder
	if outcome := strings.TrimSpace(group.Outcome); outcome != "" {
		fmt.Fprintf(&b, "Outcome: %s\n", outcome)
	}
	fmt.Fprintf(&b, "From plan %q, version %d.", version.Title, version.Number)
	return strings.TrimSpace(b.String())
}

func itemDetails(item TaskItem, group TaskGroup) string {
	var b strings.Builder
	if details := strings.TrimSpace(item.Details); details != "" {
		b.WriteString(details)
		b.WriteString("\n\n")
	}
	if expected := strings.TrimSpace(item.ExpectedResult); expected != "" {
		fmt.Fprintf(&b, "Expected result: %s\n", expected)
	}
	fmt.Fprintf(&b, "Part of: %s", group.Title)
	return strings.TrimSpace(b.String())
}
