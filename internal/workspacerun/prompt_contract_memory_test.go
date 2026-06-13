package workspacerun

import (
	"strings"
	"testing"
)

func TestBuildRunExecutionPrompt_MemorySection(t *testing.T) {
	run := &Run{ID: "run-1", Prompt: "do the thing"}

	// No memory section => no Workspace Memory block, user prompt still present.
	bare := BuildRunExecutionPrompt(run, "")
	if strings.Contains(bare, "Workspace Memory") {
		t.Errorf("empty memory section should not appear, got:\n%s", bare)
	}
	if !strings.Contains(bare, "do the thing") {
		t.Errorf("user prompt must always be present, got:\n%s", bare)
	}

	// Provided section is injected before the user prompt.
	section := "## Workspace Memory\n\n- [fact, 2026-06-01, user] staging is at stage.example.com\n"
	withMem := BuildRunExecutionPrompt(run, section)
	if !strings.Contains(withMem, "staging is at stage.example.com") {
		t.Errorf("memory section should be injected, got:\n%s", withMem)
	}
	if idxMem, idxPrompt := strings.Index(withMem, "Workspace Memory"), strings.Index(withMem, "## User Prompt"); idxMem == -1 || idxMem > idxPrompt {
		t.Errorf("memory section must precede the user prompt (mem=%d, prompt=%d)", idxMem, idxPrompt)
	}
}
