package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	if strings.Contains(got, "recurring mission run") {
		t.Error("baseline must not use the recurring framing")
	}
	if !strings.Contains(got, "findings") {
		t.Error("structured-output contract missing")
	}
}

func TestBuildMissionSystemPrompt_RecurringFraming(t *testing.T) {
	got := BuildMissionSystemPrompt(MissionPromptInputs{
		WorkspaceManagerPrompt: "You are the brand manager.",
		Mission:                "Keep brand voice consistent.",
		CycleOrdinal:           5,
	})
	if !strings.Contains(got, "recurring mission run #5") {
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

func TestParseMissionOutput_ObjectForm(t *testing.T) {
	raw := `{
		"findings": [
			{"title": "Brand voice drift", "summary": "3 posts diverge", "priority": "high", "confidence": "medium"},
			{"title": "Missing alt text", "evidence": "hero.png", "priority": "medium"}
		]
	}`
	got, err := ParseMissionOutput("ws-1", "run-42", raw)
	if err != nil {
		t.Fatalf("ParseMissionOutput: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 opportunities; got %d", len(got))
	}
	if got[0].Title != "Brand voice drift" {
		t.Errorf("title[0] = %q", got[0].Title)
	}
	if got[0].WorkspaceID != "ws-1" || got[0].SourceRunID != "run-42" {
		t.Errorf("workspace/run not threaded through: %+v", got[0])
	}
	if got[0].Status != OpportunityNew {
		t.Errorf("status default = %q; want %q", got[0].Status, OpportunityNew)
	}
}

func TestParseMissionOutput_ArrayForm(t *testing.T) {
	raw := `[{"title": "X"}, {"title": "Y"}]`
	got, err := ParseMissionOutput("ws-1", "run-1", raw)
	if err != nil {
		t.Fatalf("ParseMissionOutput: %v", err)
	}
	if len(got) != 2 || got[0].Title != "X" || got[1].Title != "Y" {
		t.Errorf("array form not parsed correctly: %+v", got)
	}
}

func TestParseMissionOutput_StripsCodeFence(t *testing.T) {
	raw := "```json\n{\"findings\": [{\"title\": \"X\"}]}\n```"
	got, err := ParseMissionOutput("ws-1", "run-1", raw)
	if err != nil {
		t.Fatalf("ParseMissionOutput: %v", err)
	}
	if len(got) != 1 || got[0].Title != "X" {
		t.Errorf("fenced JSON not parsed: %+v", got)
	}
}

func TestParseMissionOutput_EmptyInput(t *testing.T) {
	got, err := ParseMissionOutput("ws-1", "run-1", "")
	if err != nil {
		t.Fatalf("empty input must not error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty input; got %v", got)
	}
}

func TestParseMissionOutput_NormalizesPriorityAndConfidence(t *testing.T) {
	raw := `{"findings": [{"title": "X", "priority": "HIGH ", "confidence": " Medium"}]}`
	got, _ := ParseMissionOutput("ws-1", "run-1", raw)
	if got[0].Priority != "high" || got[0].Confidence != "medium" {
		t.Errorf("priority/confidence not normalized: %q / %q", got[0].Priority, got[0].Confidence)
	}
}

func TestParseMissionOutput_SkipsTitlelessFindings(t *testing.T) {
	raw := `{"findings": [{"title": ""}, {"title": "Valid"}, {"summary": "no title"}]}`
	got, _ := ParseMissionOutput("ws-1", "run-1", raw)
	if len(got) != 1 || got[0].Title != "Valid" {
		t.Errorf("titleless findings must be skipped; got %+v", got)
	}
}

func TestParseMissionOutput_ErrorsOnInvalidJSON(t *testing.T) {
	if _, err := ParseMissionOutput("ws-1", "run-1", "{not json"); err == nil {
		t.Error("expected error on invalid JSON")
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
