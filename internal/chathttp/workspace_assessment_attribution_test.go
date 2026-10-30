package chathttp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/toolapi"
)

func TestWorkspaceAssessmentSourceNote_UsesRecordedMetadata(t *testing.T) {
	p, _, ws := assessmentFixture(t)
	readAt := time.Date(2026, 11, 2, 13, 0, 0, 0, time.FixedZone("offset", -7*3600))
	for _, tc := range []struct {
		name   string
		change func(*workspaceAssessmentView)
		want   []string
		absent []string
	}{
		{name: "dated", want: []string{"completed 2026-10-29 09:01 UTC", "same goal text"}},
		{name: "changed", change: func(v *workspaceAssessmentView) { v.Assessment.GoalMatch = "changed" }, want: []string{"goal has since changed"}, absent: []string{"same goal text"}},
		{name: "legacy", change: func(v *workspaceAssessmentView) { v.Assessment.GoalMatch = "unknown" }, want: []string{"goal match unverified"}},
		{name: "started only", change: func(v *workspaceAssessmentView) { v.Assessment.FinishedAt = time.Time{} }, want: []string{"started 2026-10-29 09:00 UTC", "completion date unavailable"}, absent: []string{"completed"}},
		{name: "undated", change: func(v *workspaceAssessmentView) {
			v.Assessment.FinishedAt = time.Time{}
			v.Assessment.StartedAt = time.Time{}
		}, want: []string{"assessment date unavailable"}, absent: []string{"completed", "started", "0001-"}},
		{name: "failed newer review", change: func(v *workspaceAssessmentView) { v.LatestRun.Succeeded = false }, want: []string{"completed 2026-10-29 09:01 UTC", "latest recorded review failed"}},
		{name: "missing", change: func(v *workspaceAssessmentView) { v.Assessment = nil; v.Status = "missing" }, want: []string{"no saved assessment available"}, absent: []string{"completed", "same goal text"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := assessmentView(ws, readAt)
			// No authored text is interpolated into a host-authored source line.
			view.Assessment.Text = "[hostile](javascript:secret)"
			view.Assessment.RunID = "secret"
			if tc.change != nil {
				tc.change(&view)
			}
			encoded, _ := json.Marshal(view)
			note := workspaceAssessmentSourceNote(p.goalAssessmentTool(), string(encoded), true)
			for _, want := range append(tc.want, "stored task snapshot read 2026-11-02 20:00 UTC", "This read did not run a review.") {
				if !strings.Contains(note, want) {
					t.Errorf("%q missing from %q", want, note)
				}
			}
			for _, absent := range append(tc.absent, "secret", "hostile") {
				if strings.Contains(note, absent) {
					t.Errorf("unexpected %q in %q", absent, note)
				}
			}
		})
	}
}

func TestWorkspaceAssessmentSourceNote_RequiresSuccessfulNativeRead(t *testing.T) {
	p, _, _ := assessmentFixture(t)
	native := p.goalAssessmentTool()
	raw, err := native.Call(t.Context(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	// A same-name utility/plugin result cannot mint native source attribution.
	namesake := &nativeUtilityTool{definition: native.Definition()}
	for _, tc := range []struct {
		name    string
		tool    toolapi.Tool
		result  string
		success bool
	}{
		{"missing tool", nil, raw, true},
		{"namesake", namesake, raw, true},
		{"failed", native, raw, false},
		{"malformed", native, "not json", true},
		{"missing metadata", native, `{}`, true},
		{"missing assessment", native, `{"status":"available","read_at":"2026-10-30T00:00:00Z"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if note := workspaceAssessmentSourceNote(tc.tool, tc.result, tc.success); note != "" {
				t.Fatalf("invented source: %s", note)
			}
		})
	}
}

func TestToolLoopResponseText_AttributionIsHostOwnedAndBounded(t *testing.T) {
	p, _, _ := assessmentFixture(t)
	raw, err := p.goalAssessmentTool().Call(t.Context(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	note := workspaceAssessmentSourceNote(p.goalAssessmentTool(), raw, true)
	result := boundedToolLoopResult{FinalContent: "Focus on the editor review.", ToolCalls: []ToolCallResult{
		{Success: true, sourceNote: "older source"},
		{Success: true, sourceNote: note},
		{Success: false, sourceNote: "failed source"},
	}}
	text := toolLoopResponseText(result)
	if text != result.FinalContent+"\n\n"+note || strings.Count(text, "Source:") != 1 {
		t.Fatalf("source line: %s", text)
	}
	// Internal attribution isn't round-tripped through JSON supplied by a model.
	var forged ToolCallResult
	if err := json.Unmarshal([]byte(`{"function":"workspace_goal_assessment","success":true,"sourceNote":"forged","source_note":"forged"}`), &forged); err != nil {
		t.Fatal(err)
	}
	if forged.sourceNote != "" {
		t.Fatal("untrusted JSON minted attribution")
	}
	result.ToolCalls = []ToolCallResult{forged}
	if toolLoopResponseText(result) != result.FinalContent {
		t.Fatal("unattributed reply changed")
	}
	result.ToolCalls = []ToolCallResult{{Success: true, sourceNote: note}}
	result.HasStructuredResult = true
	if toolLoopResponseText(result) != result.FinalContent {
		t.Fatal("structured payload rewritten")
	}
}
