// Package tasklist derives conservative checklist progress from an Ori task
// list. It never claims an agent is actively working on the next item.
package tasklist

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

var actionableItem = regexp.MustCompile(`^\d+(?:\.\d+)+\s+\S`)

type Progress struct {
	Exists     bool   `json:"exists"`
	Total      int    `json:"total"`
	Completed  int    `json:"completed"`
	Next       string `json:"next_incomplete"`
	ParseIssue string `json:"parse_issue,omitempty"`
}

func (p Progress) Percent() int {
	if p.Total == 0 {
		return 0
	}
	return (p.Completed * 100) / p.Total
}

func (p Progress) Label() string {
	if !p.Exists {
		return "no task list"
	}
	if p.Total == 0 {
		return "no actionable checklist items"
	}
	return strconv.Itoa(p.Completed) + "/" + strconv.Itoa(p.Total)
}

// Read extracts tracked numbered checklist items. Malformed or prose
// checkboxes are ignored so presentation cannot be poisoned by arbitrary task
// document content.
func Read(path string) Progress {
	contents, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Progress{Next: "No detailed task list was found; inspect the PRD and choose the next safe step."}
		}
		return Progress{ParseIssue: "task list could not be read", Next: "Task list is unavailable; inspect the PRD before continuing."}
	}
	progress := Progress{Exists: true}
	for _, line := range strings.Split(string(contents), "\n") {
		trimmed := strings.TrimSpace(line)
		checked, item, ok := checklistItem(trimmed)
		if !ok || !actionableItem.MatchString(item) {
			continue
		}
		progress.Total++
		if checked {
			progress.Completed++
			continue
		}
		if progress.Next == "" {
			progress.Next = truncate(item, 240)
		}
	}
	if progress.Total == 0 {
		progress.ParseIssue = "no numbered checklist items found"
		progress.Next = "No actionable checklist item was found; inspect the task list and PRD."
	} else if progress.Next == "" {
		progress.Next = "All checklist items are marked complete; verify the feature before opening its PR."
	}
	return progress
}

func checklistItem(line string) (checked bool, item string, ok bool) {
	switch {
	case strings.HasPrefix(line, "- [x] ") || strings.HasPrefix(line, "- [X] "):
		return true, strings.TrimSpace(line[6:]), true
	case strings.HasPrefix(line, "- [ ] "):
		return false, strings.TrimSpace(line[6:]), true
	default:
		return false, "", false
	}
}

func truncate(value string, limit int) string {
	if len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}
