package tasklist

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	// MaxFileBytes bounds how much of a task list is parsed.
	MaxFileBytes = 1 << 20
	// MaxLines bounds how many lines are inspected.
	MaxLines = 20000
	// MaxItems bounds how many checklist items are retained.
	MaxItems = 2000
	// MaxItemRunes bounds one item's displayed text.
	MaxItemRunes = 240
	// MaxCheckpoints bounds the reported delivery-checkpoint list.
	MaxCheckpoints = 12
)

// PlanState separates a plan that is absent, one that exists but could not be
// understood, and one that could not be read at all. None of these is zero
// progress, and the renderer must never present them as 0/0.
type PlanState string

const (
	// PlanAvailable means the task list parsed into at least one item.
	PlanAvailable PlanState = "available"
	// PlanAbsent means no task list exists.
	PlanAbsent PlanState = "absent"
	// PlanMalformed means the file exists but yielded no usable ordinals.
	PlanMalformed PlanState = "malformed"
	// PlanUnavailable means the file could not be read.
	PlanUnavailable PlanState = "unavailable"
)

// Item is one checklist entry with its ordinal and provenance preserved.
type Item struct {
	// Ordinal is the literal numbering, for example "5.0" or "5.1".
	Ordinal string `json:"ordinal"`
	// Parent is the <N> component; Index is the <M> component.
	Parent int `json:"parent"`
	Index  int `json:"index"`
	// Text is the bounded, sanitized display text.
	Text string `json:"text"`
	// Completed reflects the checkbox state.
	Completed bool `json:"completed"`
	// Checkpoint marks delivery work (validate, demo, commit, PR, merge,
	// `wt done`) rather than implementation work.
	Checkpoint bool `json:"checkpoint"`
	// Line is the 1-based source line.
	Line int `json:"line"`
}

// Empty reports whether this item was never populated.
func (i Item) Empty() bool { return i.Ordinal == "" }

// Milestone is one `<N>.0` parent task and the `<N>.<M>` subtasks under it.
type Milestone struct {
	Item
	Subtasks []Item `json:"subtasks"`
	// Implicit is true when subtasks referenced a parent that was never
	// declared, so the milestone was synthesized to keep their counts honest.
	Implicit bool `json:"implicit"`
}

// Done reports whether the milestone and all of its subtasks are checked.
func (m Milestone) Done() bool {
	if !m.Completed && !m.Implicit {
		return false
	}
	for _, subtask := range m.Subtasks {
		if !subtask.Completed {
			return false
		}
	}
	return true
}

// Plan is the hierarchy-aware view of one task list.
type Plan struct {
	// State is the outcome of reading and parsing the file.
	State PlanState `json:"state"`
	// Path is the file that was read.
	Path string `json:"path,omitempty"`
	// Milestones are the parsed parent tasks in source order.
	Milestones []Milestone `json:"milestones,omitempty"`
	// Counts are reported separately: a plan is not "half done" because half
	// its milestones are checked while most subtasks are not.
	MilestonesTotal     int `json:"milestones_total"`
	MilestonesCompleted int `json:"milestones_completed"`
	SubtasksTotal       int `json:"subtasks_total"`
	SubtasksCompleted   int `json:"subtasks_completed"`
	// ActiveMilestone is the milestone work is currently inside.
	ActiveMilestone Item `json:"active_milestone,omitzero"`
	// NextActionable is the next implementation subtask to do. It is empty
	// when only delivery checkpoints remain.
	NextActionable Item `json:"next_actionable,omitzero"`
	// DeliveryCheckpoints are the remaining validation, demo, commit, PR,
	// merge, and `wt done` items, capped at MaxCheckpoints.
	DeliveryCheckpoints []Item `json:"delivery_checkpoints,omitempty"`
	// DeliveryCheckpointsRemaining counts every incomplete checkpoint, so a
	// capped list never understates how much delivery work is left.
	DeliveryCheckpointsRemaining int `json:"delivery_checkpoints_remaining"`
	// ImplementationComplete is true when every non-checkpoint subtask is
	// checked, so only delivery work remains.
	ImplementationComplete bool `json:"implementation_complete"`
	// ParseIssue is a sanitized reason the plan was not fully understood.
	ParseIssue string `json:"parse_issue,omitempty"`
	// Truncated is true when a bound capped parsing.
	Truncated bool `json:"truncated,omitempty"`
}

// ordinalPattern matches exactly two numeric components. Deeper nesting
// ("1.2.3") and free-form prose are deliberately not actionable items.
var ordinalPattern = regexp.MustCompile(`^(\d{1,4})\.(\d{1,4})\s+(\S.*)$`)

// checkpointPattern recognizes the conventional delivery steps that close a
// task group. These are real work, but they are not implementation progress,
// so they are counted and reported separately.
var checkpointPattern = regexp.MustCompile(`(?i)^(commit|demo|prototype demo|validate|verify|open pr|open seam-pr|open a pr|write manual test guide|run ` + "`?" + `wt done|wt done|create branch)\b`)

// checkpointPhrase catches delivery steps whose sentence does not start with
// the keyword, for example "… → squash-merge to dev".
//
// It is deliberately narrow. A bare "wt done" or "→ dev" also appears in tasks
// that merely *describe* delivery steps ("Classify commit, PR, and `wt done`
// items as checkpoints"), and misreading those as checkpoints would understate
// the implementation work left.
var checkpointPhrase = regexp.MustCompile(`(?i)(squash-merge|open pr →|open seam-pr)`)

// ReadPlan parses one task list into milestones, subtasks, and delivery
// checkpoints. It reads only the path it is given and retains no file body
// beyond the bounded display text of each item.
func ReadPlan(path string) Plan {
	plan := Plan{Path: path, State: PlanAvailable}
	info, err := os.Stat(path)
	if err != nil {
		plan.Path = ""
		if os.IsNotExist(err) {
			plan.State = PlanAbsent
			return plan
		}
		plan.State = PlanUnavailable
		plan.ParseIssue = "task list could not be inspected"
		return plan
	}
	if info.IsDir() {
		plan.Path = ""
		plan.State = PlanMalformed
		plan.ParseIssue = "task list path is a directory"
		return plan
	}
	// #nosec G304 -- callers compose this path from a canonical worktree and
	// the exact tasks-<slug>.md filename.
	contents, err := os.ReadFile(path)
	if err != nil {
		plan.State = PlanUnavailable
		plan.ParseIssue = "task list could not be read"
		return plan
	}
	if len(contents) > MaxFileBytes {
		contents = contents[:MaxFileBytes]
		plan.Truncated = true
	}

	parseInto(&plan, string(contents))
	return plan
}

// ParsePlan parses task-list contents already in memory. It exists so tests
// and callers holding a copy do not have to touch the filesystem.
func ParsePlan(contents string) Plan {
	plan := Plan{State: PlanAvailable}
	parseInto(&plan, contents)
	return plan
}

func parseInto(plan *Plan, contents string) {
	byParent := map[int]int{}
	fenced := false
	orphans := 0
	items := 0

	for index, line := range strings.Split(contents, "\n") {
		if index >= MaxLines {
			plan.Truncated = true
			break
		}
		trimmed := strings.TrimSpace(line)
		// Fenced blocks hold worked examples of task syntax. Counting the
		// template in this very repository's guidance as real work would make
		// every plan look larger than it is.
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		checked, body, ok := checklistItem(trimmed)
		if !ok {
			continue
		}
		match := ordinalPattern.FindStringSubmatch(body)
		if match == nil {
			continue
		}
		if items >= MaxItems {
			plan.Truncated = true
			break
		}
		items++

		parent, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		child, err := strconv.Atoi(match[2])
		if err != nil {
			continue
		}
		text := sanitize(match[3], MaxItemRunes)
		item := Item{
			Ordinal:    match[1] + "." + match[2],
			Parent:     parent,
			Index:      child,
			Text:       text,
			Completed:  checked,
			Checkpoint: isCheckpoint(text),
			Line:       index + 1,
		}

		if child == 0 {
			// A milestone may already exist implicitly if its subtasks were
			// listed first; adopt that group rather than splitting it.
			if position, exists := byParent[parent]; exists && plan.Milestones[position].Implicit {
				plan.Milestones[position].Item = item
				plan.Milestones[position].Implicit = false
				continue
			}
			byParent[parent] = len(plan.Milestones)
			plan.Milestones = append(plan.Milestones, Milestone{Item: item})
			continue
		}

		position, exists := byParent[parent]
		if !exists {
			byParent[parent] = len(plan.Milestones)
			position = len(plan.Milestones)
			plan.Milestones = append(plan.Milestones, Milestone{
				Item:     Item{Ordinal: match[1] + ".0", Parent: parent},
				Implicit: true,
			})
		}
		plan.Milestones[position].Subtasks = append(plan.Milestones[position].Subtasks, item)
	}

	if len(plan.Milestones) == 0 {
		plan.State = PlanMalformed
		plan.ParseIssue = "no numbered checklist items were found"
		return
	}
	// Orphans are counted after the whole file is read: a parent declared
	// below its own subtasks is normal Markdown, not a missing parent.
	for _, milestone := range plan.Milestones {
		if milestone.Implicit {
			orphans++
		}
	}
	if orphans > 0 {
		plan.ParseIssue = strconv.Itoa(orphans) + " subtask group(s) had no declared parent task"
	}
	summarize(plan)
}

// summarize computes counts, the active milestone, the next actionable item,
// and the remaining delivery checkpoints.
func summarize(plan *Plan) {
	for _, milestone := range plan.Milestones {
		if !milestone.Implicit {
			plan.MilestonesTotal++
			if milestone.Done() {
				plan.MilestonesCompleted++
			}
		}
		for _, subtask := range milestone.Subtasks {
			plan.SubtasksTotal++
			if subtask.Completed {
				plan.SubtasksCompleted++
			}
		}
	}

	// The active milestone is the first incomplete parent that still has
	// incomplete subtasks. A parent whose subtasks are all done but which is
	// itself unchecked is bookkeeping lag, not where the work is.
	var fallback *Milestone
	for index := range plan.Milestones {
		milestone := &plan.Milestones[index]
		if milestone.Done() {
			continue
		}
		if fallback == nil {
			fallback = milestone
		}
		if hasIncompleteSubtask(*milestone) {
			plan.ActiveMilestone = milestone.Item
			plan.NextActionable = nextActionable(*milestone)
			break
		}
	}
	// Parent-only plans have no subtasks at all, so the first incomplete
	// parent is both the active milestone and the next thing to do.
	if plan.ActiveMilestone.Empty() && fallback != nil {
		plan.ActiveMilestone = fallback.Item
		if len(fallback.Subtasks) == 0 {
			plan.NextActionable = fallback.Item
		}
	}

	implementationTotal, implementationDone := 0, 0
	for _, milestone := range plan.Milestones {
		// The listed checkpoints are scoped to the milestone work is inside.
		// Listing every checkpoint in the plan would bury the two or three
		// that are actually reachable under a dozen from future groups.
		active := !plan.ActiveMilestone.Empty() && milestone.Parent == plan.ActiveMilestone.Parent
		for _, subtask := range milestone.Subtasks {
			if subtask.Checkpoint {
				if !subtask.Completed {
					plan.DeliveryCheckpointsRemaining++
					if active && len(plan.DeliveryCheckpoints) < MaxCheckpoints {
						plan.DeliveryCheckpoints = append(plan.DeliveryCheckpoints, subtask)
					}
				}
				continue
			}
			implementationTotal++
			if subtask.Completed {
				implementationDone++
			}
		}
	}
	plan.ImplementationComplete = implementationTotal > 0 && implementationDone == implementationTotal
}

func hasIncompleteSubtask(milestone Milestone) bool {
	for _, subtask := range milestone.Subtasks {
		if !subtask.Completed {
			return true
		}
	}
	return false
}

// nextActionable prefers real implementation work. When only delivery
// checkpoints remain in a milestone, the first of those is returned so the
// caller still has something concrete to point at.
func nextActionable(milestone Milestone) Item {
	for _, subtask := range milestone.Subtasks {
		if !subtask.Completed && !subtask.Checkpoint {
			return subtask
		}
	}
	for _, subtask := range milestone.Subtasks {
		if !subtask.Completed {
			return subtask
		}
	}
	return Item{}
}

func isCheckpoint(text string) bool {
	return checkpointPattern.MatchString(text) || checkpointPhrase.MatchString(text)
}

// sanitize removes control characters so a task list can never inject escape
// sequences into a terminal, a JSON payload, or a Herdr board cell.
func sanitize(value string, limit int) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	return truncate(strings.TrimSpace(cleaned), limit)
}
