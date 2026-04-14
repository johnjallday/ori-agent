package cliagent

import (
	"testing"
)

func TestParseClaudeStreamJSON(t *testing.T) {
	input := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}
{"type":"result","result":"Hello world","usage":{"input_tokens":100,"output_tokens":20},"total_cost_usd":0.005}`

	raw, err := parseClaudeStreamJSON([]byte(input), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if raw.Output != "Hello world" {
		t.Errorf("expected output %q, got %q", "Hello world", raw.Output)
	}
	if len(raw.Events) != 2 {
		t.Errorf("expected 2 events, got %d", len(raw.Events))
	}
	if raw.Usage.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", raw.Usage.InputTokens)
	}
	if raw.Usage.OutputTokens != 20 {
		t.Errorf("expected 20 output tokens, got %d", raw.Usage.OutputTokens)
	}
	if raw.Usage.CostUSD != 0.005 {
		t.Errorf("expected cost 0.005, got %f", raw.Usage.CostUSD)
	}
}

func TestParseClaudeStreamJSON_MalformedLines(t *testing.T) {
	input := `not json
{"type":"result","result":"ok","usage":{"input_tokens":10,"output_tokens":5},"total_cost_usd":0.001}
also not json`

	raw, err := parseClaudeStreamJSON([]byte(input), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw.Output != "ok" {
		t.Errorf("expected output %q, got %q", "ok", raw.Output)
	}
	if len(raw.Events) != 1 {
		t.Errorf("expected 1 event (skipping malformed), got %d", len(raw.Events))
	}
}

func TestParseClaudeStreamJSON_Empty(t *testing.T) {
	raw, err := parseClaudeStreamJSON([]byte(""), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw.Output != "" {
		t.Errorf("expected empty output, got %q", raw.Output)
	}
	if len(raw.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(raw.Events))
	}
}

func TestParseCodexJSONL(t *testing.T) {
	input := `{"type":"thread.started","thread_id":"abc"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Hello from Codex"}}
{"type":"turn.completed","usage":{"input_tokens":15000,"cached_input_tokens":3000,"output_tokens":69}}`

	raw, err := parseCodexJSONL([]byte(input), "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if raw.Output != "Hello from Codex" {
		t.Errorf("expected output %q, got %q", "Hello from Codex", raw.Output)
	}
	if len(raw.Events) != 4 {
		t.Errorf("expected 4 events, got %d", len(raw.Events))
	}
	if raw.Usage.InputTokens != 15000 {
		t.Errorf("expected 15000 input tokens, got %d", raw.Usage.InputTokens)
	}
	if raw.Usage.OutputTokens != 69 {
		t.Errorf("expected 69 output tokens, got %d", raw.Usage.OutputTokens)
	}
}

func TestParseCodexJSONL_Empty(t *testing.T) {
	raw, err := parseCodexJSONL([]byte(""), "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw.Output != "" {
		t.Errorf("expected empty output, got %q", raw.Output)
	}
}

func TestParseGeminiStreamJSON(t *testing.T) {
	input := `{"type":"init","timestamp":"2026-04-14T02:05:47.324Z","session_id":"abc123","model":"gemini-3-flash-preview"}
{"type":"message","timestamp":"2026-04-14T02:05:47.325Z","role":"user","content":"say hello"}
{"type":"message","timestamp":"2026-04-14T02:05:56.059Z","role":"assistant","content":"Hello!","delta":true}
{"type":"result","timestamp":"2026-04-14T02:05:56.069Z","status":"success","stats":{"total_tokens":9668,"input_tokens":9634,"output_tokens":34,"cached":0,"duration_ms":8745,"tool_calls":0}}`

	raw, err := parseGeminiStreamJSON([]byte(input), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if raw.Output != "Hello!" {
		t.Errorf("expected output %q, got %q", "Hello!", raw.Output)
	}
	if len(raw.Events) != 4 {
		t.Errorf("expected 4 events, got %d", len(raw.Events))
	}
	if raw.Usage.InputTokens != 9634 {
		t.Errorf("expected 9634 input tokens, got %d", raw.Usage.InputTokens)
	}
	if raw.Usage.OutputTokens != 34 {
		t.Errorf("expected 34 output tokens, got %d", raw.Usage.OutputTokens)
	}
}

func TestParseGeminiStreamJSON_WithToolUse(t *testing.T) {
	input := `{"type":"init","session_id":"s1","model":"gemini-3-flash-preview"}
{"type":"message","role":"user","content":"create a file"}
{"type":"message","role":"assistant","content":"I will create the file.","delta":true}
{"type":"tool_use","tool_name":"write_file","tool_id":"wf_1","parameters":{"file_path":"test.txt","content":"hello"}}
{"type":"tool_result","tool_id":"wf_1","status":"success"}
{"type":"message","role":"assistant","content":"Done.","delta":true}
{"type":"result","status":"success","stats":{"input_tokens":500,"output_tokens":25,"total_tokens":525,"tool_calls":1}}`

	raw, err := parseGeminiStreamJSON([]byte(input), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if raw.Output != "Done." {
		t.Errorf("expected output %q, got %q", "Done.", raw.Output)
	}
	if len(raw.Events) != 7 {
		t.Errorf("expected 7 events, got %d", len(raw.Events))
	}
	if raw.Usage.InputTokens != 500 {
		t.Errorf("expected 500 input tokens, got %d", raw.Usage.InputTokens)
	}
}

func TestParseGeminiStreamJSON_Empty(t *testing.T) {
	raw, err := parseGeminiStreamJSON([]byte(""), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw.Output != "" {
		t.Errorf("expected empty output, got %q", raw.Output)
	}
	if len(raw.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(raw.Events))
	}
}

func TestMapRawToStepResult(t *testing.T) {
	raw := &RawCLIOutput{
		Output: "done",
		Events: []CLIEvent{{Type: "result"}},
		Usage:  StepUsage{InputTokens: 50, OutputTokens: 10, CostUSD: 0.002},
	}

	result := mapRawToStepResult(3, raw)
	if result.StepNumber != 3 {
		t.Errorf("expected step 3, got %d", result.StepNumber)
	}
	if result.Status != StepCompleted {
		t.Errorf("expected completed, got %s", result.Status)
	}
	if result.Output != "done" {
		t.Errorf("expected output %q, got %q", "done", result.Output)
	}
}

func TestMapRawToStepResult_EmptyOutput(t *testing.T) {
	raw := &RawCLIOutput{}
	result := mapRawToStepResult(1, raw)
	if result.Status != StepFailed {
		t.Errorf("expected failed for empty output, got %s", result.Status)
	}
}
