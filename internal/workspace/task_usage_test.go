package workspace

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// recordedUsage captures what the task handler booked.
type recordedUsage struct {
	provider  string
	model     string
	agentName string
	usage     llm.Usage
}

type fakeUsageRecorder struct {
	calls []recordedUsage
	err   error
}

func (f *fakeUsageRecorder) TrackUsage(provider, model, agentName string, usage llm.Usage, _ string) error {
	f.calls = append(f.calls, recordedUsage{provider, model, agentName, usage})
	return f.err
}

// A task run's tokens must reach the cost tracker, or the Usage page and the
// Energy bar both stay blind to scheduled work (city-economy FR29). Before this
// existed, only chat and CLI agents recorded anything.
func TestTaskUsageIsRecordedWithTheResponseUsage(t *testing.T) {
	recorder := &fakeUsageRecorder{}
	handler := &LLMTaskHandler{}
	handler.SetUsageRecorder(recorder)

	handler.recordTaskUsage("openai", "gpt-4o-mini", "Farmhand", llm.Usage{
		PromptTokens: 900, CompletionTokens: 120, TotalTokens: 1020,
	})

	if len(recorder.calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(recorder.calls))
	}
	call := recorder.calls[0]
	if call.provider != "openai" || call.model != "gpt-4o-mini" || call.agentName != "Farmhand" {
		t.Fatalf("recorded %+v, want the resolved provider, model, and agent", call)
	}
	if call.usage.TotalTokens != 1020 {
		t.Fatalf("recorded %d total tokens, want 1020", call.usage.TotalTokens)
	}
}

// A provider that reports nothing has nothing to record: a zero row would add a
// request to the Usage page without adding a token.
func TestAnEmptyUsageIsNotRecorded(t *testing.T) {
	recorder := &fakeUsageRecorder{}
	handler := &LLMTaskHandler{}
	handler.SetUsageRecorder(recorder)

	handler.recordTaskUsage("openai", "gpt-4o-mini", "Farmhand", llm.Usage{})

	if len(recorder.calls) != 0 {
		t.Fatalf("recorded %d calls for an empty usage, want 0", len(recorder.calls))
	}
}

// Usage accounting must never be the reason a task fails.
func TestARecorderFailureDoesNotEscape(t *testing.T) {
	recorder := &fakeUsageRecorder{err: errors.New("tracker closed")}
	handler := &LLMTaskHandler{}
	handler.SetUsageRecorder(recorder)

	// The absence of a panic or a return value IS the assertion: recordTaskUsage
	// swallows the error deliberately.
	handler.recordTaskUsage("openai", "gpt-4o-mini", "Farmhand", llm.Usage{TotalTokens: 10})

	if len(recorder.calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(recorder.calls))
	}
}

// No recorder is the pre-feature state and must stay a silent no-op.
func TestNoRecorderIsANoOp(t *testing.T) {
	handler := &LLMTaskHandler{}
	handler.recordTaskUsage("openai", "gpt-4o-mini", "Farmhand", llm.Usage{TotalTokens: 10})

	var nilHandler *LLMTaskHandler
	nilHandler.recordTaskUsage("openai", "gpt-4o-mini", "Farmhand", llm.Usage{TotalTokens: 10})
}
