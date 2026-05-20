package workspace

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type TaskResultType string

const (
	TaskResultTypeMarkdown TaskResultType = "markdown"
	TaskResultTypeTaskList TaskResultType = "task_list"
	TaskResultTypeNote     TaskResultType = "note"
	TaskResultTypeDecision TaskResultType = "decision"
	TaskResultTypeUnknown  TaskResultType = "unknown"
)

type TaskListResult struct {
	ParentTitle   string                `json:"parent_title"`
	ParentDetails string                `json:"parent_details,omitempty"`
	Groups        []TaskListResultGroup `json:"groups"`
}

type TaskListResultGroup struct {
	Title string               `json:"title"`
	Items []TaskListResultItem `json:"items"`
}

type TaskListResultItem struct {
	Title    string `json:"title"`
	Details  string `json:"details,omitempty"`
	Assignee string `json:"assignee,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

var (
	taskResultHeadingPattern        = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.+?)\s*$`)
	taskResultSectionHeadingPattern = regexp.MustCompile(`^\s*(?:\*\*|__)?\s*\d+\.0\.?\s+(.+?)\s*(?:\*\*|__)?\s*$`)
	taskResultCheckboxPattern       = regexp.MustCompile(`^\s*[-*]\s+\[[ xX]\]\s+(.+?)\s*$`)
	taskResultNumberPrefix          = regexp.MustCompile(`^\s*\d+(?:\.\d+)*\.?\s+`)
	taskResultAgentPattern          = regexp.MustCompile(`(?:^|\s)@([A-Za-z0-9_.-]+)\s*$`)
	taskResultWhitespacePattern     = regexp.MustCompile(`\s+`)
)

func NormalizeTaskResultType(value string) TaskResultType {
	switch TaskResultType(strings.ToLower(strings.TrimSpace(value))) {
	case TaskResultTypeTaskList:
		return TaskResultTypeTaskList
	case TaskResultTypeNote:
		return TaskResultTypeNote
	case TaskResultTypeDecision:
		return TaskResultTypeDecision
	case TaskResultTypeUnknown:
		return TaskResultTypeUnknown
	case TaskResultTypeMarkdown:
		return TaskResultTypeMarkdown
	default:
		if strings.TrimSpace(value) == "" {
			return TaskResultTypeMarkdown
		}
		return TaskResultTypeUnknown
	}
}

func DetectTaskResultType(result string) TaskResultType {
	if strings.TrimSpace(result) == "" {
		return TaskResultTypeMarkdown
	}
	if taskList, err := ParseTaskListResultMarkdown(result); err == nil && CountTaskListResultItems(taskList) > 0 {
		return TaskResultTypeTaskList
	}
	return TaskResultTypeMarkdown
}

func ApplyTaskResultMetadata(task *Task, result string) {
	if task == nil {
		return
	}
	if strings.TrimSpace(result) == "" {
		task.ResultType = ""
		task.StructuredResult = nil
		return
	}
	resultType := DetectTaskResultType(result)
	task.ResultType = resultType
	if resultType != TaskResultTypeTaskList {
		task.StructuredResult = nil
		return
	}
	taskList, err := ParseTaskListResultMarkdown(result)
	if err != nil {
		task.StructuredResult = nil
		return
	}
	task.StructuredResult = taskListResultToMap(taskList)
}

func TaskListResultFromTask(task Task) (*TaskListResult, error) {
	if strings.TrimSpace(task.Result) != "" {
		if taskList, err := ParseTaskListResultMarkdown(task.Result); err == nil && CountTaskListResultItems(taskList) > 0 {
			return taskList, nil
		}
	}
	if NormalizeTaskResultType(string(task.ResultType)) == TaskResultTypeTaskList && len(task.StructuredResult) > 0 {
		if taskList, err := taskListResultFromMap(task.StructuredResult); err == nil && CountTaskListResultItems(taskList) > 0 {
			return taskList, nil
		}
	}
	return ParseTaskListResultMarkdown(task.Result)
}

func ParseTaskListResultMarkdown(markdown string) (*TaskListResult, error) {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	result := &TaskListResult{
		ParentTitle: inferTaskListParentTitle(lines),
		Groups:      []TaskListResultGroup{},
	}

	currentGroupIndex := -1
	for _, line := range lines {
		if heading := parseTaskResultHeading(line); heading != "" {
			if shouldUseTaskResultHeadingAsGroup(heading) {
				result.Groups = append(result.Groups, TaskListResultGroup{Title: heading})
				currentGroupIndex = len(result.Groups) - 1
			}
			continue
		}

		rawItem := parseTaskResultCheckbox(line)
		if rawItem == "" {
			continue
		}
		item := parseTaskListResultItem(rawItem)
		if item.Title == "" {
			continue
		}
		if currentGroupIndex < 0 {
			result.Groups = append(result.Groups, TaskListResultGroup{Title: "Tasks"})
			currentGroupIndex = len(result.Groups) - 1
		}
		result.Groups[currentGroupIndex].Items = append(result.Groups[currentGroupIndex].Items, item)
	}

	result.Groups = compactTaskListResultGroups(result.Groups)
	if strings.TrimSpace(result.ParentTitle) == "" {
		result.ParentTitle = "Create workflow from task result"
	}
	if CountTaskListResultItems(result) == 0 {
		return nil, fmt.Errorf("task result does not contain checklist items")
	}
	return result, nil
}

func ValidateTaskListResult(taskList *TaskListResult) error {
	if taskList == nil {
		return fmt.Errorf("task list result is required")
	}
	if strings.TrimSpace(taskList.ParentTitle) == "" {
		return fmt.Errorf("parent title is required")
	}
	if CountTaskListResultItems(taskList) == 0 {
		return fmt.Errorf("task list result must include at least one subtask")
	}
	for groupIndex, group := range taskList.Groups {
		for itemIndex, item := range group.Items {
			if strings.TrimSpace(item.Title) == "" {
				return fmt.Errorf("task list item %d.%d title is required", groupIndex+1, itemIndex+1)
			}
		}
	}
	return nil
}

func CountTaskListResultItems(taskList *TaskListResult) int {
	if taskList == nil {
		return 0
	}
	count := 0
	for _, group := range taskList.Groups {
		count += len(group.Items)
	}
	return count
}

func taskListResultToMap(taskList *TaskListResult) map[string]any {
	if taskList == nil {
		return nil
	}
	data, err := json.Marshal(taskList)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func taskListResultFromMap(payload map[string]any) (*TaskListResult, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("structured task list result is empty")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var taskList TaskListResult
	if err := json.Unmarshal(data, &taskList); err != nil {
		return nil, err
	}
	if err := ValidateTaskListResult(&taskList); err != nil {
		return nil, err
	}
	return &taskList, nil
}

func parseTaskResultHeading(line string) string {
	match := taskResultHeadingPattern.FindStringSubmatch(line)
	if match != nil {
		return cleanTaskResultLine(match[2])
	}
	match = taskResultSectionHeadingPattern.FindStringSubmatch(line)
	if match != nil {
		return cleanTaskResultLine(match[1])
	}
	return ""
}

func parseTaskResultCheckbox(line string) string {
	match := taskResultCheckboxPattern.FindStringSubmatch(line)
	if match == nil {
		return ""
	}
	return cleanTaskResultLine(match[1])
}

func parseTaskListResultItem(raw string) TaskListResultItem {
	raw = strings.TrimSpace(raw)
	assignee := ""
	if match := taskResultAgentPattern.FindStringSubmatch(raw); match != nil {
		assignee = strings.TrimSpace(match[1])
		raw = strings.TrimSpace(taskResultAgentPattern.ReplaceAllString(raw, ""))
	}
	title := strings.TrimSpace(taskResultNumberPrefix.ReplaceAllString(raw, ""))
	title = strings.TrimSpace(strings.Trim(title, "-"))
	title = cleanTaskResultLine(title)
	return TaskListResultItem{
		Title:    title,
		Assignee: assignee,
	}
}

func inferTaskListParentTitle(lines []string) string {
	for _, line := range lines {
		heading := parseTaskResultHeading(line)
		if heading == "" {
			continue
		}
		normalized := strings.ToLower(heading)
		if strings.Contains(normalized, "task list") {
			return cleanParentTaskTitle(heading)
		}
	}
	for _, line := range lines {
		heading := parseTaskResultHeading(line)
		if heading == "" || shouldSkipTaskListHeading(heading) {
			continue
		}
		return cleanParentTaskTitle(heading)
	}
	return "Create workflow from task result"
}

func shouldUseTaskResultHeadingAsGroup(heading string) bool {
	if heading == "" || shouldSkipTaskListHeading(heading) {
		return false
	}
	normalized := strings.ToLower(heading)
	if strings.Contains(normalized, "task list") || strings.Contains(normalized, "final summary") {
		return false
	}
	return true
}

func shouldSkipTaskListHeading(heading string) bool {
	normalized := strings.ToLower(strings.TrimSpace(heading))
	if normalized == "" {
		return true
	}
	skipPhrases := []string{
		"final summary",
		"summary",
		"one caveat",
		"caveat",
		"note",
	}
	for _, phrase := range skipPhrases {
		if normalized == phrase || strings.HasPrefix(normalized, phrase+":") {
			return true
		}
	}
	return false
}

func cleanParentTaskTitle(title string) string {
	title = strings.TrimSpace(strings.TrimPrefix(title, "Final Summary:"))
	title = strings.TrimSpace(strings.TrimPrefix(title, "Task List:"))
	title = strings.TrimSpace(taskResultNumberPrefix.ReplaceAllString(title, ""))
	if strings.TrimSpace(title) == "" {
		return "Create workflow from task result"
	}
	if !strings.Contains(strings.ToLower(title), "task") {
		return title
	}
	return title
}

func cleanTaskResultLine(value string) string {
	value = strings.TrimSpace(value)
	value = stripTaskResultWrapping(value)
	value = strings.TrimSpace(value)
	value = taskResultWhitespacePattern.ReplaceAllString(value, " ")
	return value
}

func stripTaskResultWrapping(value string) string {
	for {
		trimmed := strings.TrimSpace(value)
		switch {
		case len(trimmed) >= 4 && strings.HasPrefix(trimmed, "**") && strings.HasSuffix(trimmed, "**"):
			value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "**"), "**"))
		case len(trimmed) >= 4 && strings.HasPrefix(trimmed, "__") && strings.HasSuffix(trimmed, "__"):
			value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "__"), "__"))
		case len(trimmed) >= 2 && strings.HasPrefix(trimmed, "`") && strings.HasSuffix(trimmed, "`"):
			value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "`"), "`"))
		default:
			return trimmed
		}
	}
}

func compactTaskListResultGroups(groups []TaskListResultGroup) []TaskListResultGroup {
	out := make([]TaskListResultGroup, 0, len(groups))
	for _, group := range groups {
		if len(group.Items) == 0 {
			continue
		}
		title := strings.TrimSpace(taskResultNumberPrefix.ReplaceAllString(group.Title, ""))
		if title == "" {
			title = "Tasks"
		}
		items := make([]TaskListResultItem, 0, len(group.Items))
		for _, item := range group.Items {
			item.Title = cleanTaskResultLine(item.Title)
			item.Details = cleanTaskResultLine(item.Details)
			item.Assignee = cleanTaskResultLine(item.Assignee)
			if item.Priority < 0 {
				item.Priority = 0
			}
			items = append(items, item)
		}
		out = append(out, TaskListResultGroup{
			Title: title,
			Items: items,
		})
	}
	return out
}
