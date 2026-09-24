package workspace

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type taskReviewedReader struct{ calls int }

func (s *taskReviewedReader) Section(_ context.Context, userID, workspaceID, instanceID string) (string, error) {
	s.calls++
	if userID == userprofile.LocalUserID && workspaceID == "hq" && instanceID == "entry" {
		return "## Personal HQ reviewed facts\n\n- user approved: \"trusted\"", nil
	}
	return "", nil
}

func TestTaskReviewedMemoryUsesResolvedAssigneeAndRawVariableWithoutDuplicate(t *testing.T) {
	folders, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Home"})
	ws.ID = "hq"
	ws.OwnerUserID = userprofile.LocalUserID
	ws.AgentInstances = []AgentInstance{{ID: "entry", Name: "Atlas", EntryPoint: true}, {ID: "other", Name: "Helper"}}
	if err := folders.Save(ws); err != nil {
		t.Fatal(err)
	}
	spy := &taskReviewedReader{}
	h := &LLMTaskHandler{workspaceStore: folders, reviewedMemory: spy}
	task := Task{ID: "task-1", WorkspaceID: ws.ID, To: "Atlas", Description: "Read the workspace."}
	if section := h.buildTaskMemorySection(context.Background(), task, false); strings.Count(section, `user approved: "trusted"`) != 1 {
		t.Fatalf("task reviewed memory missing/duplicated: %q", section)
	}
	ag := &resolvedTaskAgent{Agent: &agent.Agent{Settings: types.Settings{SystemPrompt: "Data: {{workspace.memory}}"}}}
	raw, hadVars := h.resolveTaskAgentBasePrompt(context.Background(), ag, "Atlas", task)
	if !hadVars || strings.Count(raw, `user approved: "trusted"`) != 1 {
		t.Fatalf("raw task variable missing approved fact: %q", raw)
	}
	if section := h.buildTaskMemorySection(context.Background(), task, true); strings.Contains(section, "trusted") {
		t.Fatalf("raw variable and structured section would duplicate: %q", section)
	}
	untrusted := task
	untrusted.To = "Helper"
	if section := h.buildTaskMemorySection(context.Background(), untrusted, false); strings.Contains(section, "trusted") {
		t.Fatalf("other HQ agent received reviewed memory: %q", section)
	}
	untrusted.To = "Atlas"
	untrusted.AssignedNodeID = "missing-node"
	before := spy.calls
	if section := h.buildTaskMemorySection(context.Background(), untrusted, false); strings.Contains(section, "trusted") || spy.calls != before {
		t.Fatalf("unresolved assignment reached reader: %q calls=%d", section, spy.calls-before)
	}
}
