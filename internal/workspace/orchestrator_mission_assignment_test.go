package workspace

import "testing"

func TestAssignMissionTask(t *testing.T) {
	t.Run("member assignee keeps assignee and stamps static_plan", func(t *testing.T) {
		ws := memberWorkspace("Manager", "Writer")
		task := &Task{ID: "t", To: "Writer"}
		assignMissionTask(ws, task, "Manager")
		if task.To != "Writer" || task.AssignmentMode != TaskAssignmentModeStaticPlan || task.AssignedBy != "Manager" {
			t.Fatalf("unexpected: %+v", task)
		}
	})

	t.Run("non-member assignee reassigned to coordinator", func(t *testing.T) {
		ws := memberWorkspace("Manager", "Writer")
		task := &Task{ID: "t", To: "Ghost"}
		assignMissionTask(ws, task, "Manager")
		if task.To != "Manager" || task.AssignmentMode != TaskAssignmentModeStaticPlan {
			t.Fatalf("expected reassignment to coordinator, got %+v", task)
		}
	})

	t.Run("non-member assignee without coordinator keeps assignee but stamps provenance", func(t *testing.T) {
		ws := memberWorkspace("Writer", "Researcher") // multi-agent, no entry -> coordinator ""
		task := &Task{ID: "t", To: "Ghost"}
		assignMissionTask(ws, task, "")
		if task.To != "Ghost" {
			t.Fatalf("expected non-member assignee preserved as last resort, got To=%q", task.To)
		}
		if task.AssignmentMode != TaskAssignmentModeStaticPlan || task.AssignedBy != "orchestrator" {
			t.Fatalf("expected static_plan/orchestrator provenance, got %+v", task)
		}
	})

	t.Run("empty coordinator attributes to orchestrator", func(t *testing.T) {
		ws := memberWorkspace("Writer")
		task := &Task{ID: "t", To: "Writer"}
		assignMissionTask(ws, task, "")
		if task.AssignedBy != "orchestrator" || task.AssignmentMode != TaskAssignmentModeStaticPlan {
			t.Fatalf("unexpected: %+v", task)
		}
	})
}
