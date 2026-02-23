package chathttp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/skills"
)

type mockSkillsManager struct {
	enabledSkills []skills.Skill
	err           error
}

func (m *mockSkillsManager) GetSkill(string, string) (*skills.Skill, bool, error) {
	return nil, false, nil
}

func (m *mockSkillsManager) ListSkills(string) ([]skills.Skill, error) {
	return nil, nil
}

func (m *mockSkillsManager) ListEnabledSkillsWithPrompts(string) ([]skills.Skill, error) {
	return m.enabledSkills, m.err
}

func TestBuildSystemPromptWithSkills_NilManager(t *testing.T) {
	h := &Handler{}
	ag := &agent.Agent{}
	result := h.buildSystemPromptWithSkills(ag, "test-agent", "default prompt")
	if result != "default prompt" {
		t.Fatalf("expected default prompt, got %q", result)
	}
}

func TestBuildSystemPromptWithSkills_NoEnabledSkills(t *testing.T) {
	h := &Handler{
		skillsManager: &mockSkillsManager{enabledSkills: nil},
	}
	ag := &agent.Agent{}
	result := h.buildSystemPromptWithSkills(ag, "test-agent", "default prompt")
	if result != "default prompt" {
		t.Fatalf("expected default prompt, got %q", result)
	}
}

func TestBuildSystemPromptWithSkills_WithSkills(t *testing.T) {
	h := &Handler{
		skillsManager: &mockSkillsManager{
			enabledSkills: []skills.Skill{
				{Name: "mac-automation", Prompt: "Use osascript for macOS automation."},
			},
		},
	}
	ag := &agent.Agent{}
	result := h.buildSystemPromptWithSkills(ag, "test-agent", "default prompt")

	if !strings.Contains(result, "default prompt") {
		t.Fatalf("expected base prompt to be included")
	}
	if !strings.Contains(result, "# Active Skills") {
		t.Fatalf("expected Active Skills header")
	}
	if !strings.Contains(result, "## mac-automation") {
		t.Fatalf("expected skill name header")
	}
	if !strings.Contains(result, "Use osascript for macOS automation.") {
		t.Fatalf("expected skill prompt content")
	}
}

func TestBuildSystemPromptWithSkills_EmptyAgentName(t *testing.T) {
	h := &Handler{
		skillsManager: &mockSkillsManager{
			enabledSkills: []skills.Skill{
				{Name: "test", Prompt: "Test prompt"},
			},
		},
	}
	ag := &agent.Agent{}
	result := h.buildSystemPromptWithSkills(ag, "", "default prompt")
	if result != "default prompt" {
		t.Fatalf("expected default prompt when agentName is empty, got %q", result)
	}
}

func TestCanonicalizeToolArguments_EquivalentJSON(t *testing.T) {
	first := canonicalizeToolArguments(`{"b":2,"a":1}`)
	second := canonicalizeToolArguments(`{"a":1, "b":2}`)

	if first != second {
		t.Fatalf("expected canonical args to match, got %q vs %q", first, second)
	}
}

func TestRunBoundedToolLoop_MaxTurnsFallback(t *testing.T) {
	h := &Handler{}
	executions := 0

	result := h.runBoundedToolLoop(
		"",
		[]llm.ToolCall{{ID: "tc-1", Name: "echo", Arguments: `{"value":1}`}},
		boundedToolLoopConfig{
			MaxTurns:                2,
			MaxRepeatedFingerprints: 10,
		},
		boundedToolLoopCallbacks{
			ExecuteToolCalls: func(toolCalls []llm.ToolCall) ExecuteToolCallsResult {
				executions++
				return ExecuteToolCallsResult{
					Results: []ToolCallResult{
						{
							Function: toolCalls[0].Name,
							Args:     toolCalls[0].Arguments,
							Result:   fmt.Sprintf("result-%d", executions),
							Success:  true,
						},
					},
				}
			},
			RequestNextResponse: func() (string, []llm.ToolCall, error) {
				return "", []llm.ToolCall{{ID: "tc-next", Name: "echo", Arguments: `{"value":1}`}}, nil
			},
		},
	)

	if executions != 2 {
		t.Fatalf("expected 2 executions before max-turn stop, got %d", executions)
	}
	if result.StopReason != "max_turns" {
		t.Fatalf("expected stop reason max_turns, got %q", result.StopReason)
	}
	if !result.UsedToolFallback {
		t.Fatalf("expected fallback content when max turns reached")
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool results, got %d", len(result.ToolCalls))
	}
}

func TestRunBoundedToolLoop_RepeatedFingerprintStop(t *testing.T) {
	h := &Handler{}
	executions := 0

	result := h.runBoundedToolLoop(
		"",
		[]llm.ToolCall{{ID: "tc-1", Name: "echo", Arguments: `{"value":1}`}},
		boundedToolLoopConfig{
			MaxTurns:                5,
			MaxRepeatedFingerprints: 1,
		},
		boundedToolLoopCallbacks{
			ExecuteToolCalls: func(toolCalls []llm.ToolCall) ExecuteToolCallsResult {
				executions++
				return ExecuteToolCallsResult{
					Results: []ToolCallResult{
						{
							Function: toolCalls[0].Name,
							Args:     toolCalls[0].Arguments,
							Result:   "ok",
							Success:  true,
						},
					},
				}
			},
			RequestNextResponse: func() (string, []llm.ToolCall, error) {
				return "", []llm.ToolCall{{ID: "tc-2", Name: "echo", Arguments: `{"value":1}`}}, nil
			},
		},
	)

	if executions != 1 {
		t.Fatalf("expected repeated-fingerprint guard to stop before second execution, got %d", executions)
	}
	if result.StopReason != "repeated_tool_call" {
		t.Fatalf("expected stop reason repeated_tool_call, got %q", result.StopReason)
	}
}
