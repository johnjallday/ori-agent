package workspace

import (
	"strings"
	"testing"
)

func TestResolveAgentBasePrompt(t *testing.T) {
	ws := &Workspace{ID: "w1", Name: "Acme Campaign", Description: "Q3 launch"}
	inst := AgentInstance{Name: "Copywriter", Role: "Voice keeper", CustomInstructions: "Be concise."}

	// A variable-bearing prompt resolves and reports hadVars=true.
	resolved, hadVars := ResolveAgentBasePrompt(
		"You are the copywriter for {{workspace.name}} ({{workspace.description}}). Role: {{agent.role}}. {{workspace.custom_instructions}}",
		PromptVarInputs{Workspace: ws, Instance: inst, AgentName: "Copywriter"},
	)
	if !hadVars {
		t.Fatal("expected hadVars=true")
	}
	for _, want := range []string{"Acme Campaign", "Q3 launch", "Voice keeper", "Be concise."} {
		if !strings.Contains(resolved, want) {
			t.Errorf("resolved prompt missing %q in:\n%s", want, resolved)
		}
	}
	if strings.Contains(resolved, "{{") {
		t.Errorf("no token should survive: %s", resolved)
	}

	// A plain prompt is returned unchanged with hadVars=false.
	plain, hadVars := ResolveAgentBasePrompt("Just a plain prompt.", PromptVarInputs{Workspace: ws})
	if hadVars || plain != "Just a plain prompt." {
		t.Fatalf("plain prompt should be unchanged, got %q hadVars=%v", plain, hadVars)
	}

	// Empty block variable self-omits (no notes supplied).
	out, _ := ResolveAgentBasePrompt("A{{workspace.notes.recent}}B", PromptVarInputs{Workspace: ws})
	if out != "AB" {
		t.Errorf("empty block should self-omit, got %q", out)
	}
}

func TestFormatToolNames(t *testing.T) {
	// De-dupes (case-insensitive), trims, drops blanks, preserves order.
	got := FormatToolNames([]string{"web_search", " notes ", "Web_Search"}, []string{"filesystem", "", "notes"})
	if got != "web_search, notes, filesystem" {
		t.Fatalf("FormatToolNames = %q", got)
	}
	if FormatToolNames(nil, nil) != "" {
		t.Errorf("empty inputs should yield empty string")
	}
}
