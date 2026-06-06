package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRuntimeInputsNotPersisted guards the canonical-plan invariant (FR14): the
// persisted task graph carries InputTaskIDs (the edges), while RuntimeInputs is
// derived at execution time and must never be serialized.
func TestRuntimeInputsNotPersisted(t *testing.T) {
	task := Task{
		ID:           "child",
		InputTaskIDs: []string{"upstream-1"},
		RuntimeInputs: &TaskRuntimeInputs{
			TaskResults:       map[string]string{"upstream-1": "secret runtime value"},
			StructuredOutputs: map[string]map[string]any{"upstream-1": {"k": "v"}},
		},
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(data)

	if !strings.Contains(out, `"input_task_ids"`) {
		t.Fatalf("expected input_task_ids to persist, got: %s", out)
	}
	for _, leak := range []string{"RuntimeInputs", "runtime_inputs", "secret runtime value", "StructuredOutputs"} {
		if strings.Contains(out, leak) {
			t.Fatalf("RuntimeInputs leaked into persisted JSON via %q: %s", leak, out)
		}
	}
}
