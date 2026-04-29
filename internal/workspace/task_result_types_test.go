package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseTaskListResultMarkdown_GroupedChecklist(t *testing.T) {
	content := `## Final Summary: Brand Kit Task List for johnj

Here is the complete task list.

### 1.0 Brand Identity Foundation
- [ ] 1.1 Finalize name/handle format (` + "`johnj`" + ` casing rules)
- [ ] 1.2 Lock tagline or positioning line

### 2.0 Visual Identity
- [ ] 2.1 Define color palette @brandtest-manager
- [ ] 2.2 Select and lock typography

One caveat: review existing assets before starting.
`

	taskList, err := ParseTaskListResultMarkdown(content)
	if err != nil {
		t.Fatalf("ParseTaskListResultMarkdown: %v", err)
	}
	if taskList.ParentTitle != "Brand Kit Task List for johnj" {
		t.Fatalf("expected parent title from task-list heading, got %q", taskList.ParentTitle)
	}
	if len(taskList.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %#v", taskList.Groups)
	}
	if taskList.Groups[0].Title != "Brand Identity Foundation" {
		t.Fatalf("expected cleaned group title, got %q", taskList.Groups[0].Title)
	}
	if taskList.Groups[0].Items[0].Title != "Finalize name/handle format (`johnj` casing rules)" {
		t.Fatalf("expected cleaned item title, got %#v", taskList.Groups[0].Items[0])
	}
	if taskList.Groups[1].Items[0].Assignee != "brandtest-manager" {
		t.Fatalf("expected assignee token, got %#v", taskList.Groups[1].Items[0])
	}
	if CountTaskListResultItems(taskList) != 4 {
		t.Fatalf("expected 4 items, got %d", CountTaskListResultItems(taskList))
	}
}

func TestParseTaskListResultMarkdown_FlatChecklist(t *testing.T) {
	taskList, err := ParseTaskListResultMarkdown(`- [ ] First task
- [x] Second task
`)
	if err != nil {
		t.Fatalf("ParseTaskListResultMarkdown: %v", err)
	}
	if taskList.ParentTitle != "Create workflow from task result" {
		t.Fatalf("expected fallback parent title, got %q", taskList.ParentTitle)
	}
	if len(taskList.Groups) != 1 || taskList.Groups[0].Title != "Tasks" {
		t.Fatalf("expected fallback group, got %#v", taskList.Groups)
	}
}

func TestParseTaskListResultMarkdown_BoldNumberedSections(t *testing.T) {
	taskList, err := ParseTaskListResultMarkdown(`## Brand Kit → Task List: johnj

**1.0 Brand Identity Foundation**
- [ ] 1.1 Finalize handle format rules (` + "`johnj`" + ` — casing, punctuation, no spaces)
- [ ] 1.2 Lock positioning line

**2.0 Visual Identity**
- [ ] 2.1 Lock color palette
- [ ] 2.2 Standardize all profile names to ` + "`johnj`" + `
`)
	if err != nil {
		t.Fatalf("ParseTaskListResultMarkdown: %v", err)
	}
	if taskList.ParentTitle != "Brand Kit → Task List: johnj" {
		t.Fatalf("expected task list parent title, got %q", taskList.ParentTitle)
	}
	if len(taskList.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %#v", taskList.Groups)
	}
	if taskList.Groups[0].Title != "Brand Identity Foundation" {
		t.Fatalf("expected first group title, got %q", taskList.Groups[0].Title)
	}
	if taskList.Groups[1].Title != "Visual Identity" {
		t.Fatalf("expected second group title, got %q", taskList.Groups[1].Title)
	}
	if taskList.Groups[1].Items[1].Title != "Standardize all profile names to `johnj`" {
		t.Fatalf("expected inline code marker to be preserved, got %q", taskList.Groups[1].Items[1].Title)
	}
}

func TestParseTaskListResultMarkdown_RejectsEmptyResult(t *testing.T) {
	_, err := ParseTaskListResultMarkdown("This is a summary without checklist items.")
	if err == nil {
		t.Fatal("expected empty task list error")
	}
}

func TestApplyTaskResultMetadata_TaskList(t *testing.T) {
	task := &Task{ID: "task-1"}
	ApplyTaskResultMetadata(task, `### Work
- [ ] Do the work
`)
	if task.ResultType != TaskResultTypeTaskList {
		t.Fatalf("expected task_list result type, got %q", task.ResultType)
	}
	if len(task.StructuredResult) == 0 {
		t.Fatal("expected structured result")
	}
	parsed, err := TaskListResultFromTask(*task)
	if err != nil {
		t.Fatalf("TaskListResultFromTask: %v", err)
	}
	if CountTaskListResultItems(parsed) != 1 {
		t.Fatalf("expected 1 parsed item, got %#v", parsed)
	}

	ApplyTaskResultMetadata(task, "")
	if task.ResultType != "" || task.StructuredResult != nil {
		t.Fatalf("expected empty result to clear metadata, got type=%q structured=%#v", task.ResultType, task.StructuredResult)
	}
}

func TestTaskListResultFromTask_ReparsesRawResultBeforeStructuredCache(t *testing.T) {
	task := Task{
		ID:         "task-stale-cache",
		ResultType: TaskResultTypeTaskList,
		Result: `## Brand Kit → Task List: johnj

**1.0 Brand Identity Foundation**
- [ ] 1.1 Finalize handle format rules

**2.0 Visual Identity**
- [ ] 2.1 Lock color palette
`,
		StructuredResult: taskListResultToMap(&TaskListResult{
			ParentTitle: "Brand Kit → Task List: johnj",
			Groups: []TaskListResultGroup{
				{
					Title: "Tasks",
					Items: []TaskListResultItem{
						{Title: "Stale flat task"},
					},
				},
			},
		}),
	}

	taskList, err := TaskListResultFromTask(task)
	if err != nil {
		t.Fatalf("TaskListResultFromTask: %v", err)
	}
	if len(taskList.Groups) != 2 {
		t.Fatalf("expected raw result to be reparsed into 2 groups, got %#v", taskList.Groups)
	}
	if taskList.Groups[0].Title != "Brand Identity Foundation" || taskList.Groups[1].Title != "Visual Identity" {
		t.Fatalf("expected grouped raw result, got %#v", taskList.Groups)
	}
}

func TestNormalizeTaskResultType(t *testing.T) {
	tests := map[string]TaskResultType{
		"":          TaskResultTypeMarkdown,
		"task_list": TaskResultTypeTaskList,
		"note":      TaskResultTypeNote,
		"decision":  TaskResultTypeDecision,
		"custom":    TaskResultTypeUnknown,
	}
	for input, want := range tests {
		if got := NormalizeTaskResultType(input); got != want {
			t.Fatalf("NormalizeTaskResultType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTaskResultFieldsAreBackwardCompatible(t *testing.T) {
	var task Task
	if err := json.Unmarshal([]byte(`{"id":"task-1","status":"completed","result":"plain result"}`), &task); err != nil {
		t.Fatalf("unmarshal legacy task: %v", err)
	}
	if task.Result != "plain result" {
		t.Fatalf("expected legacy result, got %q", task.Result)
	}
	if task.ResultType != "" {
		t.Fatalf("expected missing result_type to stay empty, got %q", task.ResultType)
	}

	data, err := json.Marshal(Task{ID: "task-2", Result: "plain result"})
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	if strings.Contains(string(data), "result_type") || strings.Contains(string(data), "structured_result") {
		t.Fatalf("expected empty result metadata to be omitted, got %s", string(data))
	}
}
