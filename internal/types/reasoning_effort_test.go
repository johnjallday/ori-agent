package types

import (
	"reflect"
	"testing"
)

func TestReasoningEffortLevels_OnlyCodexAndClaudeCode(t *testing.T) {
	cases := []struct {
		provider, model string
		want            []string
	}{
		{"codex", "gpt-5.6-sol", []string{"low", "medium", "high", "xhigh"}},
		{" CODEX ", "", []string{"low", "medium", "high", "xhigh"}},
		{"openai", "gpt-5-codex", []string{"low", "medium", "high", "xhigh"}},
		{"claude_code", "opus", []string{"low", "medium", "high", "xhigh", "max"}},
		{"claude", "claude-opus-5", nil},
		{"openai", "gpt-5.6-sol", nil},
		{"ollama", "llama3.2", nil},
		{"", "", nil},
	}
	for _, tc := range cases {
		if got := ReasoningEffortLevels(tc.provider, tc.model); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ReasoningEffortLevels(%q, %q) = %v, want %v", tc.provider, tc.model, got, tc.want)
		}
		if got := SupportsReasoningEffort(tc.provider, tc.model); got != (tc.want != nil) {
			t.Errorf("SupportsReasoningEffort(%q, %q) = %v", tc.provider, tc.model, got)
		}
	}

	// Callers cannot mutate the shared table through a returned slice.
	levels := ReasoningEffortLevels("codex", "")
	levels[0] = "tampered"
	if ReasoningEffortLevels("codex", "")[0] != "low" {
		t.Fatal("ReasoningEffortLevels returned the shared backing array")
	}
}

func TestNormalizeReasoningEffortFor_AcceptsOnlyTheProvidersLevels(t *testing.T) {
	cases := []struct {
		provider, model, effort, want string
	}{
		{"codex", "gpt-5.6-sol", " High ", "high"},
		{"codex", "gpt-5.6-sol", "max", ""},
		{"claude_code", "opus", "max", "max"},
		{"claude_code", "opus", "XHIGH", "xhigh"},
		{"claude_code", "opus", "extreme", ""},
		{"claude", "claude-opus-5", "high", ""},
		{"openai", "gpt-5.6-sol", "high", ""},
	}
	for _, tc := range cases {
		if got := NormalizeReasoningEffortFor(tc.provider, tc.model, tc.effort); got != tc.want {
			t.Errorf("NormalizeReasoningEffortFor(%q, %q, %q) = %q, want %q", tc.provider, tc.model, tc.effort, got, tc.want)
		}
	}
	if got := NormalizeReasoningEffort("MAX"); got != "max" {
		t.Errorf("NormalizeReasoningEffort(MAX) = %q, want max", got)
	}
}

func TestEffectiveReasoningEffort_DefaultsCodexOnly(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		settings Settings
		want     string
	}{
		{"codex unset defaults to medium", "codex", Settings{Model: "gpt-5.6-sol"}, "medium"},
		{"codex explicit", "codex", Settings{Model: "gpt-5.6-sol", ReasoningEffort: "xhigh"}, "xhigh"},
		{"codex cannot use a claude-only level", "codex", Settings{Model: "gpt-5.6-sol", ReasoningEffort: "max"}, "medium"},
		{"claude code unset sends nothing", "claude_code", Settings{Model: "opus"}, ""},
		{"claude code explicit", "claude_code", Settings{Model: "opus", ReasoningEffort: "max"}, "max"},
		{"api providers ignore it", "claude", Settings{Model: "claude-opus-5", ReasoningEffort: "high"}, ""},
	}
	for _, tc := range cases {
		if got := tc.settings.EffectiveReasoningEffort(tc.provider); got != tc.want {
			t.Errorf("%s: EffectiveReasoningEffort(%q) = %q, want %q", tc.name, tc.provider, got, tc.want)
		}
	}
}
