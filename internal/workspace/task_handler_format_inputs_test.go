package workspace

import (
	"strings"
	"testing"
)

// formatInputResults is a method on *LLMTaskHandler — but it does not touch
// any of the handler's injected dependencies. A zero-value handler is
// sufficient to exercise the prompt-building behavior in isolation.
func newPromptHandlerForTest() *LLMTaskHandler {
	return &LLMTaskHandler{}
}

func TestFormatInputResults_EmitsRawAndStructuredSections(t *testing.T) {
	h := newPromptHandlerForTest()
	var sb strings.Builder
	inputs := &TaskRuntimeInputs{
		TaskResults: map[string]string{
			"task-a": "all done",
		},
		StructuredOutputs: map[string]map[string]interface{}{
			"task-a": {"items": []interface{}{"x", "y"}, "count": 2.0},
		},
	}

	h.formatInputResults(&sb, inputs)
	out := sb.String()

	if !strings.Contains(out, "## Input from Previous Tasks") {
		t.Fatal("missing section header")
	}
	if !strings.Contains(out, "**Task task-a Result:**") {
		t.Fatal("raw text section missing for task-a")
	}
	if !strings.Contains(out, "all done") {
		t.Fatal("raw text content missing")
	}
	if !strings.Contains(out, "**Task task-a Structured Output (JSON):**") {
		t.Fatal("structured-output section header missing")
	}
	if !strings.Contains(out, "```json") {
		t.Fatal("structured output not in a json fence")
	}
	if !strings.Contains(out, `"count": 2`) || !strings.Contains(out, `"items"`) {
		t.Fatalf("structured JSON did not include expected fields: %q", out)
	}
}

func TestFormatInputResults_StructuredOnlyTaskStillEmits(t *testing.T) {
	// Upstream task may have a structured output with no raw text content
	// (e.g. ApplyTaskResultMetadata stripped Result on transition). The
	// structured map alone should still produce a section.
	h := newPromptHandlerForTest()
	var sb strings.Builder
	inputs := &TaskRuntimeInputs{
		StructuredOutputs: map[string]map[string]interface{}{
			"only-structured": {"ok": true},
		},
	}

	h.formatInputResults(&sb, inputs)
	out := sb.String()

	if !strings.Contains(out, "**Task only-structured Structured Output (JSON):**") {
		t.Fatalf("expected structured section even with no raw text, got %q", out)
	}
	if strings.Contains(out, "**Task only-structured Result:**") {
		t.Error("raw-text section should not appear when no TaskResults entry")
	}
}

func TestFormatInputResults_NoOpOnEmpty(t *testing.T) {
	h := newPromptHandlerForTest()

	var sb strings.Builder
	h.formatInputResults(&sb, nil)
	if sb.Len() != 0 {
		t.Errorf("nil inputs should produce no output, got %q", sb.String())
	}

	sb.Reset()
	h.formatInputResults(&sb, &TaskRuntimeInputs{})
	if sb.Len() != 0 {
		t.Errorf("empty inputs should produce no output, got %q", sb.String())
	}

	sb.Reset()
	h.formatInputResults(&sb, &TaskRuntimeInputs{
		TaskResults: map[string]string{"task-a": ""},
	})
	// Empty string result still skips the raw section but produces no header
	// (since there are no other entries either). Section header only emits
	// when at least one ID has emittable content.
	if strings.Contains(sb.String(), "Task task-a Result") {
		t.Errorf("expected empty result to be skipped, got %q", sb.String())
	}
}

func TestFormatInputResults_DeterministicOrder(t *testing.T) {
	h := newPromptHandlerForTest()
	inputs := &TaskRuntimeInputs{
		TaskResults: map[string]string{
			"zeta":  "Z",
			"alpha": "A",
			"mu":    "M",
		},
	}

	// Run a handful of times. Go map iteration is randomized, so without the
	// explicit sort in formatInputResults this would flake.
	var first string
	for i := 0; i < 8; i++ {
		var sb strings.Builder
		h.formatInputResults(&sb, inputs)
		got := sb.String()
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("non-deterministic output across runs:\nrun 0:\n%s\nrun %d:\n%s", first, i, got)
		}
	}

	idxAlpha := strings.Index(first, "Task alpha")
	idxMu := strings.Index(first, "Task mu")
	idxZeta := strings.Index(first, "Task zeta")
	if !(idxAlpha < idxMu && idxMu < idxZeta) {
		t.Fatalf("tasks not in sorted order:\nalpha=%d mu=%d zeta=%d\n%s", idxAlpha, idxMu, idxZeta, first)
	}
}
