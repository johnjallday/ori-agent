package workspace

import (
	"context"
	"strings"
	"testing"
)

func TestGoalPromptLine(t *testing.T) {
	if got := GoalPromptLine("  "); got != "" {
		t.Fatalf("empty goal line = %q", got)
	}
	if got := GoalPromptLine("Keep the brand voice\n consistent"); got != "Goal: Keep the brand voice consistent" {
		t.Fatalf("GoalPromptLine = %q", got)
	}
	long := strings.Repeat("a", WorkspaceGoalPromptLimit+50)
	if got := GoalPromptLine(long); len(got) != len("Goal: ")+WorkspaceGoalPromptLimit || !strings.HasSuffix(got, "...") {
		t.Fatalf("a long goal must be bounded, got %d chars", len(got))
	}
}

// FR22: a plain goal of up to 500 runes gives the byte-identical goal line it
// gave before charters existed (task prompts and builder plans both read
// GoalPromptLine); a charter gives its Outcome line only.
func TestGoalPromptLine_PlainGoalIsUnchangedAndACharterGivesItsOutcome(t *testing.T) {
	words := strings.Repeat("Keep the brand voice consistent, é ü. Reply\n within a day. ", 10)
	for _, goal := range []string{
		"a",
		string([]rune(words)[:120]),
		string([]rune(words)[:500]),
		"  Draft   posts\n\nweekly  ",
	} {
		before := "Goal: " + sanitizeTaskPromptText(goal, WorkspaceGoalPromptLimit)
		if got := GoalPromptLine(goal); got != before {
			t.Fatalf("goal of %d runes changed:\n got %q\nwant %q", len([]rune(goal)), got, before)
		}
	}

	charter := "## Outcome\nA monthly newsletter.\n## Maintain\nTidy ideas.\n## Boundaries\nNever send anything."
	if got := GoalPromptLine(charter); got != "Goal: A monthly newsletter." {
		t.Fatalf("charter line = %q", got)
	}
}

// SM1: every task the workspace runs — an ordinary task, a delegated subtask,
// a mission run — carries the goal in the protected header of its user turn,
// which the context budget never trims.
func TestTaskPromptCarriesTheWorkspaceGoalInItsProtectedHeader(t *testing.T) {
	store := NewInMemoryStore()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Brand Desk", Agents: []string{"Writer"}})
	ws.Mission = "Keep the brand voice consistent"
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	handler := &LLMTaskHandler{workspaceStore: store}

	for name, task := range map[string]Task{
		"ordinary":  {ID: "t1", WorkspaceID: ws.ID, Description: "Draft this week's post"},
		"delegated": {ID: "t2", WorkspaceID: ws.ID, Description: "Check the tone", AssignmentMode: TaskAssignmentModeDynamicDelegation},
		"mission": {ID: "t3", WorkspaceID: ws.ID, Description: "Review the workspace",
			Context: map[string]any{MissionTaskContextOriginKey: MissionTaskContextOriginValue}},
	} {
		segments := handler.buildTaskPromptSegments(context.Background(), task)
		if len(segments) == 0 || segments[0].label != "header" || segments[0].trimOrder != 0 {
			t.Fatalf("%s: unexpected segments %+v", name, segments)
		}
		header := segments[0].text
		goalAt := strings.Index(header, "Goal: Keep the brand voice consistent")
		if goalAt < 0 {
			t.Fatalf("%s: header lacks the goal: %q", name, header)
		}
		if strings.Index(header, "## Task Description") < goalAt {
			t.Fatalf("%s: the goal must come before the task: %q", name, header)
		}
	}

	// A workspace without a goal adds no goal section.
	if err := store.Update(ws.ID, func(w *Workspace) error {
		w.Mission = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	prompt := handler.buildTaskPrompt(context.Background(), Task{ID: "t4", WorkspaceID: ws.ID, Description: "Draft"})
	if strings.Contains(prompt, "## Workspace Goal") {
		t.Fatalf("no goal, no goal section: %q", prompt)
	}
}
