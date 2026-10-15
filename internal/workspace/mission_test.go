package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestGetMission_OpenFindingsCount(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)

	ws := &Workspace{
		ID:     "ws-find",
		Name:   "Findings",
		Status: StatusActive,
		Opportunities: []Opportunity{
			{ID: "o1", WorkspaceID: "ws-find", Status: OpportunityNew},
			{ID: "o2", WorkspaceID: "ws-find", Status: OpportunitySnoozed},
			{ID: "o3", WorkspaceID: "ws-find", Status: OpportunityResolved},
			{ID: "o4", WorkspaceID: "ws-find", Status: OpportunityDismissed},
		},
	}
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws-find/mission", nil)
	req.SetPathValue("workspaceID", "ws-find")
	rec := httptest.NewRecorder()
	handler.GetMission(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OpenFindingsCount int `json:"open_findings_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Only new + snoozed are open; resolved + dismissed are archived.
	if resp.OpenFindingsCount != 2 {
		t.Fatalf("open_findings_count=%d; want 2", resp.OpenFindingsCount)
	}
}

func TestUpdateMissionRequest_CadenceNullClearsSchedule(t *testing.T) {
	var req UpdateMissionRequest
	if err := json.Unmarshal([]byte(`{"cadence": null}`), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if !req.CadenceSet {
		t.Fatal("expected cadence presence to be tracked")
	}
	if req.Cadence != nil {
		t.Fatalf("expected nil cadence for explicit null, got %#v", req.Cadence)
	}
}

func TestUpdateMissionRequest_CadenceOmittedLeavesScheduleUntouched(t *testing.T) {
	var req UpdateMissionRequest
	if err := json.Unmarshal([]byte(`{"mission": "Keep quality high"}`), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req.CadenceSet {
		t.Fatal("expected omitted cadence to leave cadence unset")
	}
}

func TestBuildMissionSystemPrompt_BaselineFraming(t *testing.T) {
	got := BuildMissionSystemPrompt(MissionPromptInputs{
		WorkspaceManagerPrompt: "You are the brand manager.",
		Mission:                "Keep brand voice consistent.",
		CycleOrdinal:           1,
	})
	if !strings.Contains(got, "You are the brand manager.") {
		t.Error("missing base manager prompt")
	}
	if !strings.Contains(got, "Keep brand voice consistent.") {
		t.Error("missing mission text")
	}
	if !strings.Contains(got, "BASELINE run") {
		t.Error("first cycle must be framed as baseline")
	}
	if strings.Contains(got, "goal run #") {
		t.Error("baseline must not use the recurring framing")
	}
	if !strings.Contains(got, `{"items": [ ... ]}`) || !strings.Contains(got, `"kind": "task"`) || !strings.Contains(got, `"kind": "finding"`) {
		t.Error("structured-output contract missing")
	}
}

// FR14: the contract names both kinds, each in one sentence, with the caps,
// and no longer asks for findings-only output.
func TestBuildMissionSystemPrompt_ItemsContract(t *testing.T) {
	got := BuildMissionSystemPrompt(MissionPromptInputs{Mission: "Keep brand voice consistent.", CycleOrdinal: 1})
	for _, want := range []string{
		"Goal: Keep brand voice consistent.",
		"Answer with ONE JSON object and nothing else",
		`- "task": work that moves the goal forward and that an agent in this workspace could do.`,
		`- "finding": something the user should know that is not itself a task`,
		`"effort": "small"|"medium"|"large"`,
		`"suggested_agent": string`,
		`"first_step": string`,
		`"recommended_action": string`,
		"Suggest at most 5 tasks and 5 findings.",
		`If nothing is worth suggesting, answer {"items": []}.`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Contains(got, `{"findings": [`) {
		t.Error("the findings-only contract must be gone")
	}
}

// FR18/FR19: existing and dismissed work is listed so it is not suggested
// again, agents are named for suggested_agent, and what the run may do while
// it looks follows the autonomy level.
func TestBuildMissionSystemPrompt_ListsExistingWorkAndAutonomy(t *testing.T) {
	watch := BuildMissionSystemPrompt(MissionPromptInputs{
		Mission: "x", CycleOrdinal: 2, AutonomyPolicy: AutonomyWatch,
		AgentNames:         []string{"Writer", "Editor"},
		ExistingWorkTitles: []string{"Draft the style guide"},
		DismissedTitles:    []string{"Audit every old post"},
	})
	for _, want := range []string{
		"Agents in this workspace (use one of these names for suggested_agent, or leave it empty): Writer, Editor",
		"Work that already exists in this workspace (do not suggest it again):\n  - Draft the style guide",
		"Suggestions the user dismissed as not useful (never suggest them again):\n  - Audit every old post",
		"but do not change anything",
	} {
		if !strings.Contains(watch, want) {
			t.Errorf("watch prompt lacks %q", want)
		}
	}
	if strings.Contains(watch, "Do NOT take action") {
		t.Error("the old report-only line must be gone")
	}
	propose := BuildMissionSystemPrompt(MissionPromptInputs{Mission: "x", CycleOrdinal: 2, AutonomyPolicy: AutonomyPropose})
	if !strings.Contains(propose, "write notes or drafts inside it") || strings.Contains(propose, "but do not change anything") {
		t.Errorf("propose prompt must allow workspace writes: %q", propose)
	}
}

func TestBuildMissionSystemPrompt_RecurringFraming(t *testing.T) {
	got := BuildMissionSystemPrompt(MissionPromptInputs{
		WorkspaceManagerPrompt: "You are the brand manager.",
		Mission:                "Keep brand voice consistent.",
		CycleOrdinal:           5,
	})
	if !strings.Contains(got, "goal run #5") {
		t.Error("expected recurring framing with cycle number")
	}
	if strings.Contains(got, "BASELINE") {
		t.Error("recurring run should not be framed as baseline")
	}
}

func TestBuildMissionSystemPrompt_IncludesOpenOpportunitiesOnly(t *testing.T) {
	got := BuildMissionSystemPrompt(MissionPromptInputs{
		Mission:      "x",
		CycleOrdinal: 2,
		OpenOpportunities: []Opportunity{
			{Title: "Open A", Priority: "high", Status: OpportunityNew},
			{Title: "Snoozed B", Priority: "low", Status: OpportunitySnoozed},
			{Title: "Resolved C", Priority: "high", Status: OpportunityResolved},
			{Title: "Dismissed D", Priority: "high", Status: OpportunityDismissed},
		},
	})
	if !strings.Contains(got, "Open A") {
		t.Error("open opportunity missing")
	}
	if !strings.Contains(got, "Snoozed B") {
		t.Error("snoozed opportunity should still be passed (it can re-surface)")
	}
	if strings.Contains(got, "Resolved C") {
		t.Error("resolved opportunity must not be passed back to the run")
	}
	if strings.Contains(got, "Dismissed D") {
		t.Error("dismissed opportunity must not be passed back")
	}
}

func TestAutonomyPolicyRunHints(t *testing.T) {
	cases := []struct {
		policy             AutonomyPolicy
		wantMutation       string
		wantExternalDenied bool
	}{
		{AutonomyWatch, "denied", true},
		{AutonomyPropose, "allowed", true},
		{AutonomyPolicy("unknown"), "denied", true}, // safe fallback
		{AutonomyPolicy(""), "denied", true},
	}
	for _, c := range cases {
		h := AutonomyPolicyRunHints(c.policy)
		if h.Mutation != c.wantMutation {
			t.Errorf("policy=%q mutation=%q; want %q", c.policy, h.Mutation, c.wantMutation)
		}
		if (h.ExternalEffects == "denied") != c.wantExternalDenied {
			t.Errorf("policy=%q external=%q; want denied=%v", c.policy, h.ExternalEffects, c.wantExternalDenied)
		}
		if h.Approval != "none" {
			t.Errorf("policy=%q approval=%q; want none in v1", c.policy, h.Approval)
		}
	}
}

const missionFixtureAnswer = `{"items": [
	{"kind": "task", "title": "Draft a brand voice guide", "why": "Three posts drift in tone", "evidence": "notes/posts.md", "priority": "high", "confidence": "medium", "effort": "small", "suggested_agent": "Writer", "first_step": "Collect five on-brand posts"},
	{"kind": "finding", "title": "Brand voice drift", "why": "3 posts diverge", "evidence": "posts/*.md", "priority": "HIGH ", "confidence": " Medium", "recommended_action": "Review the last three posts"}
]}`

func TestParseMissionRunOutput_ItemsContract(t *testing.T) {
	got, err := ParseMissionRunOutput("ws-1", "run-42", missionFixtureAnswer)
	if err != nil {
		t.Fatalf("ParseMissionRunOutput: %v", err)
	}
	if got.Rejected != 0 || got.ParseStatus() != MissionParseOK || len(got.Tasks) != 1 || len(got.Findings) != 1 {
		t.Fatalf("output = %+v", got)
	}
	task := got.Tasks[0]
	if task.Title != "Draft a brand voice guide" || task.Why != "Three posts drift in tone" || task.Effort != "small" ||
		task.SuggestedAgent != "Writer" || task.FirstStep != "Collect five on-brand posts" || task.Priority != "high" {
		t.Errorf("task = %+v", task)
	}
	finding := got.Findings[0]
	if finding.WorkspaceID != "ws-1" || finding.SourceRunID != "run-42" || finding.Status != OpportunityNew {
		t.Errorf("workspace/run/status not threaded through: %+v", finding)
	}
	if finding.Summary != "3 posts diverge" || finding.RecommendedAction != "Review the last three posts" {
		t.Errorf("finding why/recommended action lost: %+v", finding)
	}
	if finding.Priority != "high" || finding.Confidence != "medium" {
		t.Errorf("priority/confidence not normalized: %q / %q", finding.Priority, finding.Confidence)
	}
}

func TestParseMissionRunOutput_AcceptsOneFencedBlock(t *testing.T) {
	for name, raw := range map[string]string{
		"fence only":         "```json\n" + missionFixtureAnswer + "\n```",
		"prose around fence": "Here is what I found.\n\n```json\n" + missionFixtureAnswer + "\n```\nLet me know.",
	} {
		got, err := ParseMissionRunOutput("ws-1", "run-1", raw)
		if err != nil || len(got.Tasks) != 1 || len(got.Findings) != 1 {
			t.Errorf("%s: got %+v, err %v", name, got, err)
		}
	}
	twoFences := "```json\n{\"items\": []}\n```\nand\n```json\n{\"items\": []}\n```"
	if _, err := ParseMissionRunOutput("ws-1", "run-1", twoFences); err == nil {
		t.Error("two fenced blocks must not be guessed between")
	}
}

// The previous findings-only shapes still read, as findings.
func TestParseMissionRunOutput_LegacyShapesAreFindings(t *testing.T) {
	for _, raw := range []string{
		`{"findings": [{"title": "X", "summary": "why"}, {"title": "Y"}]}`,
		`[{"title": "X", "summary": "why"}, {"title": "Y"}]`,
	} {
		got, err := ParseMissionRunOutput("ws-1", "run-1", raw)
		if err != nil || len(got.Findings) != 2 || len(got.Tasks) != 0 || got.Findings[0].Summary != "why" {
			t.Errorf("legacy %s: got %+v, err %v", raw, got, err)
		}
	}
}

// Unknown kinds, titleless items and invalid enum values are rejected and
// counted; the rest of the answer still counts.
func TestParseMissionRunOutput_RejectsInvalidItems(t *testing.T) {
	raw := `{"items": [
		{"kind": "task", "title": "Keep me"},
		{"kind": "chore", "title": "Unknown kind"},
		{"title": "No kind"},
		{"kind": "task", "title": ""},
		{"kind": "task", "title": "Bad effort", "effort": "huge"},
		{"kind": "finding", "title": "Bad priority", "priority": "urgent"}
	]}`
	got, err := ParseMissionRunOutput("ws-1", "run-1", raw)
	if err != nil {
		t.Fatalf("ParseMissionRunOutput: %v", err)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].Title != "Keep me" || len(got.Findings) != 0 || got.Rejected != 5 {
		t.Fatalf("output = %+v", got)
	}
	if got.ParseStatus() != MissionParsePartial {
		t.Errorf("parse status = %q, want partial", got.ParseStatus())
	}
}

// SM7 at the parser: an unreadable answer is an error, never an empty success.
func TestParseMissionRunOutput_UnreadableAnswersError(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":       "   ",
		"prose":       "I looked at the workspace and everything seems fine.",
		"invalid":     "{not json",
		"no items":    `{"suggestions": []}`,
		"items wrong": `{"items": "none"}`,
	} {
		if _, err := ParseMissionRunOutput("ws-1", "run-1", raw); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := ParseMissionRunOutput("ws-1", "run-1", ""); !errors.Is(err, ErrMissionOutputEmpty) {
		t.Errorf("empty answer err = %v, want ErrMissionOutputEmpty", err)
	}
	if got, err := ParseMissionRunOutput("ws-1", "run-1", `{"items": []}`); err != nil || got.ParseStatus() != MissionParseOK {
		t.Errorf("an empty items list is a valid answer: %+v, %v", got, err)
	}
}

func TestNormalizeSuggestionTitle(t *testing.T) {
	for _, title := range []string{"Draft a brand voice guide", "  draft A  brand voice GUIDE.", "Draft a brand voice guide!!"} {
		if got := NormalizeSuggestionTitle(title); got != "draft a brand voice guide" {
			t.Errorf("NormalizeSuggestionTitle(%q) = %q", title, got)
		}
	}
}

// SM5 core: duplicates (against known work and within the answer) are skipped
// and counted before the cap; whatever is left over the cap is dropped and
// counted.
func TestSelectMissionSuggestions(t *testing.T) {
	known := map[string]struct{}{"draft a brand voice guide": {}}
	suggestions := []MissionTaskSuggestion{
		{Title: "Draft a brand voice guide."}, // known
		{Title: "Plan next week's posts"},
		{Title: "plan next week's posts"}, // repeated in the answer
		{Title: "A"}, {Title: "B"}, {Title: "C"}, {Title: "D"}, {Title: "E"},
	}
	kept, duplicates, dropped := SelectMissionSuggestions(suggestions, known, 5)
	if len(kept) != 5 || duplicates != 2 || dropped != 1 {
		t.Fatalf("kept=%d duplicates=%d dropped=%d", len(kept), duplicates, dropped)
	}
	if kept[0].Title != "Plan next week's posts" || kept[4].Title != "D" {
		t.Errorf("kept = %+v", kept)
	}
	if len(known) != 1 {
		t.Error("known titles must not be modified")
	}
}

func TestOpenWorkTitlesSkipsFinishedWork(t *testing.T) {
	ws := &Workspace{Tasks: []Task{
		{ID: "a", Description: "Open backlog item", TicketState: TicketStateBacklog},
		{ID: "b", Description: "In flight", TicketState: TicketStateReady},
		{ID: "c", Description: "Done already", TicketState: TicketStateDone},
		{ID: "d", Description: "Cancelled", TicketState: TicketStateCancelled},
	}}
	got := OpenWorkTitles(ws)
	if _, ok := got["open backlog item"]; !ok || len(got) != 2 {
		t.Fatalf("OpenWorkTitles = %v", got)
	}
}

func TestRecordMissionRunKeepsTheNewestRuns(t *testing.T) {
	ws := &Workspace{}
	for i := 0; i < MissionRunHistoryLimit+3; i++ {
		RecordMissionRun(ws, MissionRunRecord{RunID: fmt.Sprintf("run-%d", i)})
	}
	if len(ws.MissionRuns) != MissionRunHistoryLimit || ws.MissionRuns[0].RunID != fmt.Sprintf("run-%d", MissionRunHistoryLimit+2) {
		t.Fatalf("runs = %+v", ws.MissionRuns)
	}
}

func TestMissionOutputTailKeepsTheEnd(t *testing.T) {
	long := strings.Repeat("é", MissionOutputTailLimit) + "THE END"
	tail := MissionOutputTail(long)
	if !strings.HasSuffix(tail, "THE END") || !strings.HasPrefix(tail, "…") || len(tail) > MissionOutputTailLimit+len("…") {
		t.Fatalf("tail len=%d", len(tail))
	}
	if !utf8.ValidString(tail) {
		t.Fatal("tail must not split a rune")
	}
	if MissionOutputTail("short") != "short" {
		t.Fatal("a short answer is kept whole")
	}
}

func TestApplyMissionRunOutcome_Success(t *testing.T) {
	start := time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)
	ws := &Workspace{
		Cadence: &ScheduleConfig{Type: ScheduleWeekly, DayOfWeek: int(time.Monday), TimeOfDay: "09:00"},
	}
	ApplyMissionRunOutcome(ws, MissionRunOutcome{StartedAt: start, Succeeded: true})
	if ws.LastMissionRunAt == nil || !ws.LastMissionRunAt.Equal(start) {
		t.Errorf("LastMissionRunAt not set: %v", ws.LastMissionRunAt)
	}
	if ws.MissionExecutionCount != 1 {
		t.Errorf("execution count = %d; want 1", ws.MissionExecutionCount)
	}
	if ws.MissionFailureCount != 0 {
		t.Errorf("failure count = %d; want 0 on success", ws.MissionFailureCount)
	}
	if ws.NextMissionRunAt == nil {
		t.Error("NextMissionRunAt should be computed from cadence")
	}
}

func TestApplyMissionRunOutcome_Failure(t *testing.T) {
	ws := &Workspace{}
	ApplyMissionRunOutcome(ws, MissionRunOutcome{StartedAt: time.Now(), Succeeded: false})
	if ws.MissionExecutionCount != 1 {
		t.Errorf("execution count = %d; want 1 (still counts)", ws.MissionExecutionCount)
	}
	if ws.MissionFailureCount != 1 {
		t.Errorf("failure count = %d; want 1", ws.MissionFailureCount)
	}
	if ws.NextMissionRunAt != nil {
		t.Error("NextMissionRunAt should be nil when no cadence is set")
	}
}

func TestApplyMissionRunOutcome_HoldCadence(t *testing.T) {
	start := time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)
	existingNext := time.Date(2026, 3, 16, 9, 0, 0, 0, time.UTC)
	ws := &Workspace{
		Cadence:          &ScheduleConfig{Type: ScheduleWeekly, DayOfWeek: int(time.Monday), TimeOfDay: "09:00"},
		NextMissionRunAt: &existingNext,
	}
	// An event-fired run with HoldCadence still counts but must NOT move the
	// next cadence run (PRD #5a — cadence heartbeat).
	ApplyMissionRunOutcome(ws, MissionRunOutcome{StartedAt: start, Succeeded: true, HoldCadence: true})
	if ws.MissionExecutionCount != 1 {
		t.Errorf("execution count = %d; want 1 (event run still counts)", ws.MissionExecutionCount)
	}
	if ws.LastMissionRunAt == nil || !ws.LastMissionRunAt.Equal(start) {
		t.Errorf("LastMissionRunAt should advance even with HoldCadence: %v", ws.LastMissionRunAt)
	}
	if ws.NextMissionRunAt == nil || !ws.NextMissionRunAt.Equal(existingNext) {
		t.Errorf("NextMissionRunAt should stay unchanged with HoldCadence; got %v want %v", ws.NextMissionRunAt, existingNext)
	}
}

func TestBuildMissionSystemPrompt_TriggeringEvent(t *testing.T) {
	out := BuildMissionSystemPrompt(MissionPromptInputs{
		Mission:      "Watch the repo",
		CycleOrdinal: 2,
		TriggeringEvent: &TriggerEventContext{
			TriggerName: "pr-opened",
			TriggerType: "webhook",
			FiredAt:     time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC),
			EventCount:  1,
			Summary:     "POST 1.2 KB from 10.0.0.1",
			Payload:     `{"action":"opened"}`,
		},
	})
	for _, want := range []string{"TRIGGERING EVENT", "pr-opened", "webhook", "POST 1.2 KB", `{"action":"opened"}`} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q:\n%s", want, out)
		}
	}
}

func TestEvaluateMissionToolCall(t *testing.T) {
	overrides := map[string]SideEffect{"http_post": SideEffectExternal}

	// Watch + read = allow.
	d := EvaluateMissionToolCall(AutonomyWatch, SideEffectRead, nil, "read_file")
	if !d.Allowed {
		t.Errorf("Watch + read should allow; reason: %s", d.Reason)
	}
	// Watch + write = deny.
	d = EvaluateMissionToolCall(AutonomyWatch, SideEffectWrite, nil, "write_file")
	if d.Allowed {
		t.Error("Watch + write should deny")
	}
	if !strings.Contains(d.Reason, "denies") {
		t.Errorf("deny reason missing: %q", d.Reason)
	}
	// Propose + write = allow.
	d = EvaluateMissionToolCall(AutonomyPropose, SideEffectWrite, nil, "write_file")
	if !d.Allowed {
		t.Errorf("Propose + write should allow; reason: %s", d.Reason)
	}
	// Propose + override(external) = deny.
	d = EvaluateMissionToolCall(AutonomyPropose, SideEffectWrite, overrides, "http_post")
	if d.Allowed {
		t.Error("Propose must deny external even with permissive binding default")
	}
	if d.Classification != SideEffectExternal {
		t.Errorf("expected classification external; got %q", d.Classification)
	}
	// Unclassified = deny with clear reason.
	d = EvaluateMissionToolCall(AutonomyPropose, "", nil, "anything")
	if d.Allowed {
		t.Error("unclassified must deny")
	}
	if !strings.Contains(d.Reason, "unclassified") {
		t.Errorf("unclassified reason missing: %q", d.Reason)
	}
}
