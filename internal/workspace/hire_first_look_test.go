package workspace

import (
	"strings"
	"testing"
)

func TestBuildMissionSystemPrompt_HireFirstLookFramingAndContract(t *testing.T) {
	prompt := BuildMissionSystemPrompt(MissionPromptInputs{
		Mission:        "Compare the three note-taking apps I'm considering and recommend one",
		CycleOrdinal:   1,
		AutonomyPolicy: AutonomyWatch,
		AgentNames:     []string{"Rosa"},
		Origin:         MissionOriginHireFirstLook,
	})
	for _, want := range []string{
		"Goal: Compare the three note-taking apps I'm considering and recommend one",
		"You were just hired for this workspace, and this is your FIRST LOOK",
		"There is no folder or project to read",
		"Do not read anything outside this workspace",
		"change nothing",
		`{"summary": string, "next": string, "next_why": string, "items": [ ... ]}`,
		`"next": the exact title of the one task to start first`,
		`"next_why": one sentence on why that task comes first`,
		"Suggest at most 3 tasks and no findings",
		"read this workspace with your tools, but do not change anything",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("first look prompt lacks %q:\n%s", want, prompt)
		}
	}
	for _, absent := range []string{"BASELINE", `"kind": "finding"`, "Suggest at most 5 tasks"} {
		if strings.Contains(prompt, absent) {
			t.Errorf("first look prompt still says %q", absent)
		}
	}

	// Every other run keeps its own framing and contract.
	baseline := BuildMissionSystemPrompt(MissionPromptInputs{Mission: "Ship it", CycleOrdinal: 1, Origin: MissionOriginFirstGoal})
	if !strings.Contains(baseline, "BASELINE") || !strings.Contains(baseline, "Suggest at most 5 tasks and 5 findings") ||
		strings.Contains(baseline, "FIRST LOOK") || strings.Contains(baseline, `"next_why"`) {
		t.Fatalf("a first-goal run changed:\n%s", baseline)
	}
}

func TestParseMissionRunOutput_ReadsAFirstLooksSummaryAndNext(t *testing.T) {
	raw := `{"summary": "You want one app.\n  I found no notes yet, so I start from the brief.",
		"next": "List the three apps and the criteria that matter",
		"next_why": "Every comparison needs the criteria first.",
		"items": [{"kind": "task", "title": "List the three apps and the criteria that matter", "why": "w", "evidence": "the brief", "priority": "high", "effort": "small"}]}`
	out, err := ParseMissionRunOutput("ws-1", "run-1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary != "You want one app. I found no notes yet, so I start from the brief." ||
		out.Next != "List the three apps and the criteria that matter" ||
		out.NextWhy != "Every comparison needs the criteria first." || len(out.Tasks) != 1 {
		t.Fatalf("output %+v", out)
	}
	// An answer without them (every other run) reads as before.
	plain, err := ParseMissionRunOutput("ws-1", "run-1", `{"items": []}`)
	if err != nil || plain.Summary != "" || plain.Next != "" || plain.NextWhy != "" {
		t.Fatalf("plain answer %+v %v", plain, err)
	}
	long, _ := ParseMissionRunOutput("ws-1", "run-1", `{"summary": "`+strings.Repeat("s", 900)+`", "next": "`+strings.Repeat("n", 400)+`", "items": []}`)
	if len([]rune(long.Summary)) > missionSummaryMax || len([]rune(long.Next)) > missionNextMax {
		t.Fatalf("unbounded: %d %d", len(long.Summary), len(long.Next))
	}
}

func TestMissionSuggestionLimitsAndSummary(t *testing.T) {
	if tasks, findings := MissionSuggestionLimits(MissionOriginHireFirstLook); tasks != 3 || findings != 0 {
		t.Fatalf("first look limits %d/%d", tasks, findings)
	}
	if tasks, findings := MissionSuggestionLimits(MissionOriginCadence); tasks != MissionMaxTaskSuggestions || findings != MissionMaxFindings {
		t.Fatalf("cadence limits %d/%d", tasks, findings)
	}
	stored := []MissionTaskSuggestion{{Title: "Stored first"}, {Title: "Stored second"}}
	for name, tc := range map[string]struct {
		output MissionRunOutput
		want   string
	}{
		"named next":          {MissionRunOutput{Summary: "Found it.", Next: "Do the thing"}, "Found it.\nNext: Do the thing"},
		"next from the first": {MissionRunOutput{Summary: "Found it."}, "Found it.\nNext: Stored first"},
		"no summary":          {MissionRunOutput{Next: "Do the thing"}, "Next: Do the thing"},
	} {
		if got := MissionRunSummary(tc.output, stored); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
	if got := MissionRunSummary(MissionRunOutput{Summary: "Nothing yet."}, nil); got != "Nothing yet." {
		t.Errorf("no suggestion: %q", got)
	}
}

func TestMissionGate_HireFirstLookStaysInItsWorkspace(t *testing.T) {
	store := NewInMemoryStore()
	ws := gateWorkspace(t, store)
	h := &LLMTaskHandler{workspaceStore: store}
	look := missionGateTask(ws.ID, AutonomyWatch)
	look.Context[MissionTaskContextRunOriginKey] = MissionOriginHireFirstLook
	ordinary := missionGateTask(ws.ID, AutonomyWatch)

	for _, tool := range []string{"hq_overview", "home_library_search", "home_profile_read", "resource_catalog_search", MemoryWriteToolName} {
		if err := h.evaluateMissionGate(look, tool); err == nil || !strings.Contains(err.Error(), "first look") {
			t.Errorf("a first look used %s: %v", tool, err)
		}
	}
	for _, tool := range []string{"workspace_notes", "workspace_tasks", "read_file", "list_directory"} {
		if err := h.evaluateMissionGate(look, tool); err != nil {
			t.Errorf("a first look could not read its workspace with %s: %v", tool, err)
		}
	}
	// The same tools stay as they were for every other goal run.
	for _, tool := range []string{"hq_overview", MemoryWriteToolName} {
		if err := h.evaluateMissionGate(ordinary, tool); err != nil {
			t.Errorf("an ordinary goal run lost %s: %v", tool, err)
		}
	}
}
