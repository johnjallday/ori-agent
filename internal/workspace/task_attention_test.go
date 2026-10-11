package workspace

import (
	"encoding/json"
	"os"
	"testing"
)

func TestTaskAttentionSharedContract(t *testing.T) {
	data, err := os.ReadFile("testdata/task_attention_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name                              string
		Tasks                             []Task
		States                            []string
		Attention, Open, Backlog, Unknown int
		Active                            bool
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			for i, task := range c.Tasks {
				if got := TaskAttentionState(task); got != c.States[i] {
					t.Errorf("task %d: %s, want %s", i, got, c.States[i])
				}
			}
			got := ComputeMapSummaryFields(&Workspace{Tasks: c.Tasks})
			if !got.TaskSummaryAvailable || got.NeedsAttentionCount != c.Attention || got.OpenTaskCount != c.Open || got.BacklogCount != c.Backlog || got.UnknownTaskCount != c.Unknown || got.Active != c.Active {
				t.Errorf("summary %+v, want %+v", got, c)
			}
		})
	}
	if ComputeMapSummaryFields(nil).TaskSummaryAvailable {
		t.Error("missing workspace must be unavailable")
	}
}
